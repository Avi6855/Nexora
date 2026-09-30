package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/reconciliation-service/internal/clients"
	"github.com/nexora/nexora/services/reconciliation-service/internal/domain"
	"github.com/nexora/nexora/services/reconciliation-service/internal/events"
)

// This file drives the whole loop the way it runs in a deployed stack: a
// payment.unknown event arrives on Kafka, a case is filed, and each sweep talks
// to payment-service over HTTP — reading the payment's real state and, when it
// has evidence, writing the outcome back. The fake payment-service below speaks
// the same contract as payment-service's own handler.

type fakePaymentService struct {
	mu       sync.Mutex
	states   map[string]string
	resolves []resolveCall
	failWith int
	server   *httptest.Server
}

func newFakePaymentService() *fakePaymentService {
	f := &fakePaymentService{states: map[string]string{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/payments/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()

		if f.failWith != 0 {
			w.WriteHeader(f.failWith)
			_, _ = w.Write([]byte(`{"error":"payment-service unavailable"}`))
			return
		}

		paymentID := strings.TrimPrefix(r.URL.Path, "/v1/payments/")

		if strings.HasSuffix(paymentID, "/resolve-unknown") {
			paymentID = strings.TrimSuffix(paymentID, "/resolve-unknown")
			var body struct {
				Outcome    string `json:"outcome"`
				Reason     string `json:"reason"`
				ResolvedBy string `json:"resolved_by"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)

			f.resolves = append(f.resolves, resolveCall{paymentID: paymentID, outcome: body.Outcome, reason: body.Reason})
			f.states[paymentID] = body.Outcome

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"payment_id": paymentID,
				"state":      body.Outcome,
			})
			return
		}

		state := f.states[paymentID]
		if state == "" {
			state = "UNKNOWN"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"payment_id": paymentID,
			"state":      state,
			"amount":     1_000,
			"currency":   "GBP",
		})
	})

	f.server = httptest.NewServer(mux)
	return f
}

func (f *fakePaymentService) setState(paymentID, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[paymentID] = state
}

func (f *fakePaymentService) failRequestsWith(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failWith = status
}

func (f *fakePaymentService) resolveCalls() []resolveCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]resolveCall, len(f.resolves))
	copy(out, f.resolves)
	return out
}

// unknownEventPayload is the shape payment-service publishes on
// nexora.payment.unknown: the domain payload inside the event envelope.
func unknownEventPayload(t *testing.T, paymentID string, amount int64) *events.PaymentEventPayload {
	t.Helper()

	raw := fmt.Appendf(nil, `{
		"event_id": "evt-1",
		"event_type": "payment.unknown",
		"aggregate_id": %q,
		"producer": "payment-service",
		"payload": {
			"payment_id": %q,
			"account_id": "acc-1",
			"amount": %d,
			"currency": "GBP",
			"state": "UNKNOWN"
		}
	}`, paymentID, paymentID, amount)

	var envelope struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decoding envelope: %v", err)
	}

	var payload events.PaymentEventPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		t.Fatalf("decoding payload: %v", err)
	}
	return &payload
}

// The loop, start to finish: the event files a case, the sweep retries while the
// payment is genuinely unknown, and when the provider's late callback concludes
// the payment the case closes against it — no second guess about money that has
// already moved.
func TestUnknownEventBecomesAClosedCaseAgainstThePayment(t *testing.T) {
	paymentService := newFakePaymentService()
	defer paymentService.server.Close()
	t.Setenv("PAYMENT_SERVICE_URL", paymentService.server.URL)
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")

	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	svc.SetPaymentGateway(clients.NewPaymentClient())

	paymentID := uuid.New()
	payload := unknownEventPayload(t, paymentID.String(), 2_500)

	// What the Kafka consumer does with the event.
	parsed, err := uuid.Parse(payload.PaymentID)
	if err != nil {
		t.Fatalf("event carried an unparseable payment id: %v", err)
	}
	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), parsed, payload.Amount, payload.Currency)
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}
	if opened.Status != domain.ReconciliationStatusPending {
		t.Fatalf("status = %s, want PENDING", opened.Status)
	}

	// First sweep: the payment really is unknown, so nothing is concluded yet.
	if _, err := svc.RunScheduledReconciliation(context.Background(), 10); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if status := repo.statusOf(t, opened.CaseID); status == domain.ReconciliationStatusResolved {
		t.Fatal("the first sweep resolved a case while the payment was still UNKNOWN")
	}
	if calls := paymentService.resolveCalls(); len(calls) != 0 {
		t.Fatalf("the first sweep wrote an outcome back with no evidence: %+v", calls)
	}

	// The provider's late callback lands and concludes the payment.
	paymentService.setState(paymentID.String(), "SETTLED")

	if _, err := svc.RunScheduledReconciliation(context.Background(), 10); err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	closed := repo.caseFor(t, paymentID)
	if closed.Status != domain.ReconciliationStatusResolved {
		t.Fatalf("case status = %s, want RESOLVED", closed.Status)
	}
	if closed.ExternalState != "SETTLED" {
		t.Errorf("external state = %s, want SETTLED read from the payment", closed.ExternalState)
	}
	// The payment concluded itself; writing an outcome back would be a second
	// decision about a payment that already made one.
	if calls := paymentService.resolveCalls(); len(calls) != 0 {
		t.Errorf("wrote an outcome back to a payment that had already concluded: %+v", calls)
	}
}

// The case holding the provider's answer is the evidence that concludes the
// payment, and it travels over real HTTP with the service credential.
func TestCaseEvidenceConcludesThePaymentOverHTTP(t *testing.T) {
	paymentService := newFakePaymentService()
	defer paymentService.server.Close()
	t.Setenv("PAYMENT_SERVICE_URL", paymentService.server.URL)
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")

	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	svc.SetPaymentGateway(clients.NewPaymentClient())

	paymentID := uuid.New()
	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 4_100, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}
	// A provider statement says the payment went through after all.
	if err := repo.UpdateCaseExternalState(context.Background(), opened.CaseID, "CONFIRMED", 4_100); err != nil {
		t.Fatalf("recording external state: %v", err)
	}

	if _, err := svc.RunScheduledReconciliation(context.Background(), 10); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	calls := paymentService.resolveCalls()
	if len(calls) != 1 {
		t.Fatalf("write-backs = %d, want exactly 1", len(calls))
	}
	if calls[0].outcome != clients.ResolutionOutcomeConfirmed {
		t.Errorf("outcome = %s, want CONFIRMED", calls[0].outcome)
	}
	if calls[0].paymentID != paymentID.String() {
		t.Errorf("write-back targeted %s, want %s", calls[0].paymentID, paymentID)
	}
	if state := paymentService.states[paymentID.String()]; state != "CONFIRMED" {
		t.Errorf("payment-service state = %s, want CONFIRMED", state)
	}
	if status := repo.statusOf(t, opened.CaseID); status != domain.ReconciliationStatusResolved {
		t.Errorf("case status = %s, want RESOLVED", status)
	}
}

// When payment-service is unreachable the case must stay open and keep counting
// attempts, so an outage escalates instead of quietly resolving cases.
func TestUnreachablePaymentServiceKeepsTheCaseOpen(t *testing.T) {
	paymentService := newFakePaymentService()
	defer paymentService.server.Close()
	t.Setenv("PAYMENT_SERVICE_URL", paymentService.server.URL)
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")

	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	svc.SetPaymentGateway(clients.NewPaymentClient())

	paymentID := uuid.New()
	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 900, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	paymentService.failRequestsWith(http.StatusInternalServerError)

	results, err := svc.RunScheduledReconciliation(context.Background(), 10)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(results) != 1 || results[0].Matched {
		t.Fatalf("an unreadable payment was reported as resolved: %+v", results)
	}
	if attempts := repo.attemptsOf(t, opened.CaseID); attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if status := repo.statusOf(t, opened.CaseID); status == domain.ReconciliationStatusResolved {
		t.Error("an unreadable payment resolved the case")
	}
}
