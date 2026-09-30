package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/clients"
	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/provider"
	"github.com/nexora/nexora/services/payment-service/internal/service"
	"github.com/nexora/nexora/services/payment-service/internal/transport"
)

// This file is the end-to-end version of the UNKNOWN story, driven through the
// real HTTP surface and a real (fake-shaped) ledger of record: a payment whose
// provider answer is indeterminate keeps the customer's hold, and every way out
// of that state — a late provider callback, or reconciliation deciding — gives
// the hold back exactly once.

// fakeLedger is a stand-in ledger-service: it hands out reservation ids and
// records every hold taken and released. Money held here is money the customer
// cannot spend, which is the whole point of asserting on it.
type fakeLedger struct {
	mu       sync.Mutex
	reserves []string
	releases []string
}

func (l *fakeLedger) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/ledger/reserve", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AccountID     string `json:"account_id"`
			Amount        int64  `json:"amount"`
			Currency      string `json:"currency"`
			TransactionID string `json:"transaction_id"`
			TTL           string `json:"ttl"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		l.mu.Lock()
		reservationID := uuid.New()
		l.reserves = append(l.reserves, req.TransactionID)
		l.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"reservation_id": reservationID.String(),
			"status":         "ACTIVE",
			"amount":         req.Amount,
			"currency":       req.Currency,
			"expires_at":     time.Now().Add(time.Hour).UTC(),
		})
	})

	mux.HandleFunc("/v1/ledger/reservations/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/release") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/ledger/reservations/"), "/release")

		l.mu.Lock()
		l.releases = append(l.releases, id)
		l.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "RELEASED"})
	})

	return mux
}

func (l *fakeLedger) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.reserves), len(l.releases)
}

type unknownLoop struct {
	server *httptest.Server
	ledger *fakeLedger
	repo   *InMemoryPaymentRepository
	// user is the owner of the payment under test: reading a payment back
	// means presenting the same identity that created it.
	user string
}

func newUnknownLoop(t *testing.T, behavior provider.Behavior) *unknownLoop {
	t.Helper()

	ledger := &fakeLedger{}
	ledgerServer := httptest.NewServer(ledger.handler())
	t.Cleanup(ledgerServer.Close)

	// The saga takes its holds against the configured ledger endpoint.
	t.Setenv("LEDGER_SERVICE_URL", ledgerServer.URL)

	repo := NewInMemoryPaymentRepository()
	mockProvider := provider.NewMockProvider(provider.MockProviderConfig{
		ProviderID:      "test-provider",
		DefaultBehavior: behavior,
		Delay:           5 * time.Millisecond,
	})
	paymentService := service.NewPaymentService(repo, mockProvider, NewInMemoryEventPublisher(), zerolog.Nop())
	paymentService.SetFundHolder(clients.NewLedgerClient(zerolog.Nop()))

	handlers := transport.NewHandlers(paymentService, zerolog.Nop())
	router := mux.NewRouter()
	handlers.RegisterRoutes(router)

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &unknownLoop{server: server, ledger: ledger, repo: repo}
}

func (h *unknownLoop) do(t *testing.T, method, path string, body interface{}, headers map[string]string) (int, map[string]interface{}) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshaling request: %v", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var decoded map[string]interface{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return resp.StatusCode, decoded
}

// drive sends a payment all the way to UNKNOWN through the public API and
// asserts the hold is still in place afterwards.
func (h *unknownLoop) drive(t *testing.T, key string, amount int64) string {
	t.Helper()

	userID := uuid.New().String()
	h.user = userID
	status, created := h.do(t, http.MethodPost, "/v1/payments", map[string]interface{}{
		"idempotency_key": key,
		"account_id":      uuid.New().String(),
		"user_id":         userID,
		"payment_type":    "CARD",
		"amount":          amount,
		"currency":        "GBP",
		"counterparty_id": "merchant-1",
		"reference":       "unknown loop",
	}, nil)
	if status != http.StatusCreated {
		t.Fatalf("create payment: status %d (%v)", status, created)
	}
	paymentID, _ := created["payment_id"].(string)
	if paymentID == "" {
		t.Fatalf("create payment returned no payment_id: %v", created)
	}

	// The caller is the payment's own user, the way the app and the gateway
	// present them (X-User-ID from the JWT).
	if status, body := h.do(t, http.MethodPost, "/v1/payments/"+paymentID+"/authorize", nil, map[string]string{"X-User-ID": userID}); status != http.StatusOK {
		t.Fatalf("authorize: status %d (%v)", status, body)
	}
	if status, body := h.do(t, http.MethodPost, "/v1/payments/"+paymentID+"/process", map[string]interface{}{}, nil); status != http.StatusOK {
		t.Fatalf("process: status %d (%v)", status, body)
	}

	status, payment := h.do(t, http.MethodGet, "/v1/payments/"+paymentID, nil, h.ownHeaders())
	if status != http.StatusOK {
		t.Fatalf("get payment: status %d", status)
	}
	if state, _ := payment["state"].(string); state != "UNKNOWN" {
		t.Fatalf("state = %v, want UNKNOWN", payment["state"])
	}

	reserves, releases := h.ledger.counts()
	if reserves != 1 {
		t.Fatalf("ledger took %d holds, want 1", reserves)
	}
	if releases != 0 {
		t.Fatalf("an UNKNOWN payment released its hold %d times, want 0: the money may still have moved", releases)
	}

	return paymentID
}

func (h *unknownLoop) ownHeaders() map[string]string {
	return map[string]string{"X-User-ID": h.user}
}

func paymentState(t *testing.T, h *unknownLoop, paymentID string) string {
	t.Helper()
	status, payment := h.do(t, http.MethodGet, "/v1/payments/"+paymentID, nil, h.ownHeaders())
	if status != http.StatusOK {
		t.Fatalf("get payment: status %d", status)
	}
	state, _ := payment["state"].(string)
	return state
}

// A provider that answered UNKNOWN and then sends SUCCESS is telling us the
// money moved. The callback used to be rejected ("must be PROCESSING"), which
// left the payment UNKNOWN and the customer's money held with the provider
// having actually taken it.
func TestLateProviderCallbackConcludesTheUnknownPayment(t *testing.T) {
	h := newUnknownLoop(t, provider.BehaviorUnknown)
	paymentID := h.drive(t, "late-callback-key", 4_200)

	status, body := h.do(t, http.MethodPost, "/v1/webhooks/provider", map[string]interface{}{
		"payment_id":      paymentID,
		"provider_id":     "test-provider",
		"transaction_ref": "TXN-late-1",
		"status":          "SUCCESS",
		"response_code":   "00",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("late SUCCESS callback: status %d (%v)", status, body)
	}

	if state := paymentState(t, h, paymentID); state != "SETTLED" {
		t.Fatalf("state = %s, want SETTLED", state)
	}

	reserves, releases := h.ledger.counts()
	if reserves != 1 || releases != 1 {
		t.Fatalf("holds taken/released = %d/%d, want 1/1", reserves, releases)
	}
}

// Reconciliation deciding the payment never left is the other way out: the
// customer's hold comes back instead of expiring on its own, and a retried
// decision must not release it a second time.
func TestReconciliationResolvesUnknownOverHTTPExactlyOnce(t *testing.T) {
	h := newUnknownLoop(t, provider.BehaviorUnknown)
	paymentID := h.drive(t, "resolve-endpoint-key", 3_300)

	// Without the service-to-service secret nobody may decide the outcome of
	// somebody else's payment.
	if status, _ := h.do(t, http.MethodPost, "/v1/payments/"+paymentID+"/resolve-unknown", map[string]interface{}{
		"outcome": "FAILED",
		"reason":  "forged",
	}, nil); status != http.StatusForbidden {
		t.Fatalf("unauthenticated resolve-unknown: status %d, want 403", status)
	}
	if _, releases := h.ledger.counts(); releases != 0 {
		t.Fatalf("a rejected caller released the hold %d times", releases)
	}

	token := map[string]string{"X-Internal-Token": "test-internal-token"}
	for attempt := 1; attempt <= 2; attempt++ {
		status, body := h.do(t, http.MethodPost, "/v1/payments/"+paymentID+"/resolve-unknown", map[string]interface{}{
			"outcome":     "FAILED",
			"reason":      "rail rejected the payment after the timeout",
			"resolved_by": "reconciliation-service",
		}, token)
		if status != http.StatusOK {
			t.Fatalf("resolve attempt %d: status %d (%v)", attempt, status, body)
		}
		if state, _ := body["state"].(string); state != "FAILED" {
			t.Fatalf("resolve attempt %d: state %v, want FAILED", attempt, body["state"])
		}
	}

	if state := paymentState(t, h, paymentID); state != "FAILED" {
		t.Fatalf("state = %s, want FAILED", state)
	}
	if _, releases := h.ledger.counts(); releases != 1 {
		t.Fatalf("hold releases = %d across two identical resolutions, want exactly 1", releases)
	}
}

// An outcome nobody defined is a bug in the caller, not permission to guess at
// whether money moved.
func TestResolveUnknownRejectsAnUndefinedOutcome(t *testing.T) {
	h := newUnknownLoop(t, provider.BehaviorUnknown)
	paymentID := h.drive(t, "bad-outcome-key", 1_100)

	status, _ := h.do(t, http.MethodPost, "/v1/payments/"+paymentID+"/resolve-unknown", map[string]interface{}{
		"outcome": "MAYBE",
	}, map[string]string{"X-Internal-Token": "test-internal-token"})
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if state := paymentState(t, h, paymentID); state != "UNKNOWN" {
		t.Fatalf("state = %s, want it left at UNKNOWN", state)
	}
	if _, releases := h.ledger.counts(); releases != 0 {
		t.Fatalf("a rejected outcome released the hold %d times", releases)
	}
}

// The hold is the reason UNKNOWN is not simply "declined": the money stays
// unusable until somebody establishes what happened. This asserts the state the
// reconciliation sweep depends on, including that nothing else quietly
// concluded the payment while it was unknown.
func TestUnknownPaymentStaysUnknownAndHeldUntilItIsResolved(t *testing.T) {
	h := newUnknownLoop(t, provider.BehaviorUnknown)
	paymentID := h.drive(t, "stays-unknown-key", 2_500)

	// Re-drive the callback the provider never sent: an UNKNOWN answer again
	// must not move the payment on.
	status, _ := h.do(t, http.MethodPost, "/v1/webhooks/provider", map[string]interface{}{
		"payment_id":  paymentID,
		"provider_id": "test-provider",
		"status":      "UNKNOWN",
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("second UNKNOWN callback: status %d", status)
	}

	if state := paymentState(t, h, paymentID); state != "UNKNOWN" {
		t.Fatalf("state = %s, want UNKNOWN", state)
	}

	// The customer can still see the payment, so the app can show "we are
	// checking with your bank" instead of a wrong answer.
	stored, err := h.repo.GetByID(context.Background(), uuid.MustParse(paymentID))
	if err != nil || stored == nil {
		t.Fatalf("payment not readable while unknown: %v", err)
	}
	if stored.ReservationID == "" {
		t.Fatal("the hold was dropped while the outcome was unknown: the money would be spendable twice")
	}
	if stored.State != domain.PaymentStateUnknown {
		t.Fatalf("stored state = %s, want UNKNOWN", stored.State)
	}
}
