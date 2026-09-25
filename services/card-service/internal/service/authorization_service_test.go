package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/card-service/internal/clients"
	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/repository"
	"github.com/nexora/nexora/shared/schemes"
)

// ── Test doubles ────────────────────────────────────────────────────────────
//
// The authorization path talks to the risk engine and the ledger over HTTP, so
// the tests drive the real clients against stub servers. That keeps the client
// contract (status codes, JSON shape, error mapping) inside the test rather
// than mocking it away.

type harness struct {
	svc        *AuthorizationService
	cards      *fakeCardRepo
	auths      *fakeAuthRepo
	challenges *fakeChallengeRepo
	fraud      *stubFraud
	ledger     *stubLedger
	otp        string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	cards := &fakeCardRepo{}
	auths := &fakeAuthRepo{byID: make(map[uuid.UUID]*domain.CardAuthorization)}
	challenges := newFakeChallengeRepo()
	fraud := newStubFraud(t)
	ledger := newStubLedger(t)

	svc := NewAuthorizationService(
		cards,
		auths,
		clients.NewFraudClient(fraud.server.URL, zerolog.Nop()),
		clients.NewLedgerClient(ledger.server.URL, zerolog.Nop()),
		nil, // no Kafka in tests: the decision path must not depend on it
		zerolog.Nop(),
	)
	h := &harness{svc: svc, cards: cards, auths: auths, challenges: challenges, fraud: fraud, ledger: ledger}

	// PSD2 step-up with a known one-time code, so the challenge tests can
	// answer the challenge a real cardholder would receive.
	h.otp = "424242"
	svc.SetSCAOTPGenerator(func() (string, error) { return h.otp, nil })
	svc.EnableSCA(schemes.DefaultSCAPolicy(), challenges)
	return h
}

// disableSCA leaves the service on the exemption-free path (no step-up).
func (h *harness) disableSCA() {
	h.svc.scaPolicy = schemes.SCAPolicy{}
	h.svc.challenges = nil
}

// lastChallenge returns the most recently raised step-up.
func (h *harness) lastChallenge() *domain.SCAChallenge {
	return h.challenges.last()
}

func (h *harness) withCard(card *domain.Card) *domain.Card {
	h.cards.card = card
	return card
}

type fakeCardRepo struct {
	card *domain.Card
}

func (r *fakeCardRepo) Create(ctx context.Context, card *domain.Card) error {
	r.card = card
	return nil
}
func (r *fakeCardRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Card, error) {
	if r.card == nil || r.card.CardID != id {
		return nil, domain.ErrCardNotFound
	}
	return r.card, nil
}
func (r *fakeCardRepo) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Card, error) {
	if r.card == nil {
		return nil, nil
	}
	return []*domain.Card{r.card}, nil
}
func (r *fakeCardRepo) Update(ctx context.Context, card *domain.Card) error {
	r.card = card
	return nil
}
func (r *fakeCardRepo) Delete(ctx context.Context, id uuid.UUID) error { r.card = nil; return nil }

type fakeAuthRepo struct {
	mu     sync.Mutex
	byID   map[uuid.UUID]*domain.CardAuthorization
	order  []uuid.UUID
	update int
}

func (r *fakeAuthRepo) Create(ctx context.Context, auth *domain.CardAuthorization) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[auth.AuthorizationID] = auth
	r.order = append(r.order, auth.AuthorizationID)
	return nil
}

func (r *fakeAuthRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.CardAuthorization, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ok := r.byID[id]
	if !ok {
		return nil, domain.ErrAuthNotFound
	}
	// A real store returns a snapshot, not a live pointer: the refund claim
	// relies on the write comparing the version that was read.
	clone := *auth
	return &clone, nil
}

// UpdateChallengeSatisfied mirrors the Cassandra LWT: CHALLENGED -> APPROVED.
func (r *fakeAuthRepo) UpdateChallengeSatisfied(ctx context.Context, id uuid.UUID, reservationID uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ok := r.byID[id]
	if !ok {
		return domain.ErrAuthNotFound
	}
	if auth.Status != domain.AuthStatusChallenged {
		return domain.ErrAuthNotChallenged
	}
	auth.Status = domain.AuthStatusApproved
	auth.Decision = domain.AuthDecisionApprove
	auth.ReservationID = reservationID
	auth.UpdatedAt = time.Now().UTC()
	r.update++
	return nil
}

// UpdateChallengeDeclined mirrors the Cassandra LWT: CHALLENGED -> DECLINED.
func (r *fakeAuthRepo) UpdateChallengeDeclined(ctx context.Context, id uuid.UUID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ok := r.byID[id]
	if !ok {
		return domain.ErrAuthNotFound
	}
	if auth.Status != domain.AuthStatusChallenged {
		return domain.ErrAuthNotChallenged
	}
	auth.Status = domain.AuthStatusDeclined
	auth.Decision = domain.AuthDecisionDecline
	auth.DeclineReason = reason
	auth.UpdatedAt = time.Now().UTC()
	r.update++
	return nil
}

// SaveRefund mirrors the optimistic-concurrency refund write: it applies only
// when the stored row is still the version the caller read.
func (r *fakeAuthRepo) SaveRefund(ctx context.Context, auth *domain.CardAuthorization, expectedStatus domain.AuthorizationStatus, expectedUpdatedAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored, ok := r.byID[auth.AuthorizationID]
	if !ok {
		return false, domain.ErrAuthNotFound
	}
	if stored.Status != expectedStatus || !stored.UpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	stored.RefundedAmount = auth.RefundedAmount
	stored.RefundedAt = auth.RefundedAt
	stored.Status = auth.Status
	stored.UpdatedAt = auth.UpdatedAt
	r.update++
	return true, nil
}

func (r *fakeAuthRepo) GetByCardID(ctx context.Context, cardID uuid.UUID, limit int) ([]*domain.CardAuthorization, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*domain.CardAuthorization
	for _, id := range r.order {
		if auth := r.byID[id]; auth != nil && auth.CardID == cardID {
			out = append(out, auth)
		}
	}
	return out, nil
}

func (r *fakeAuthRepo) UpdateCapture(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ok := r.byID[id]
	if !ok {
		return domain.ErrAuthNotFound
	}
	auth.Status = domain.AuthStatusCaptured
	r.update++
	return nil
}

func (r *fakeAuthRepo) UpdateVoid(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	auth, ok := r.byID[id]
	if !ok {
		return domain.ErrAuthNotFound
	}
	auth.Status = domain.AuthStatusVoided
	r.update++
	return nil
}

func (r *fakeAuthRepo) stored() []*domain.CardAuthorization {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*domain.CardAuthorization, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

type stubFraud struct {
	mu       sync.Mutex
	server   *httptest.Server
	status   int
	decision string
	score    float64
	level    string
	reasons  []string
	calls    int
}

func newStubFraud(t *testing.T) *stubFraud {
	t.Helper()
	s := &stubFraud{status: http.StatusOK, decision: "APPROVE", score: 12.5, level: "LOW"}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls++
		status, decision, score, level, reasons := s.status, s.decision, s.score, s.level, s.reasons
		s.mu.Unlock()

		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":"risk engine unavailable"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"decision":   decision,
			"risk_score": score,
			"risk_level": level,
			"reasons":    reasons,
		})
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubFraud) set(decision string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decision = decision
}

func (s *stubFraud) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *stubFraud) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type stubLedger struct {
	mu            sync.Mutex
	server        *httptest.Server
	reserveStatus int
	refundStatus  int
	balanceAfter  int64
	reserves      int
	refunds       int
	settles       int
	releases      int
	entryReads    int
}

func newStubLedger(t *testing.T) *stubLedger {
	t.Helper()
	s := &stubLedger{reserveStatus: http.StatusCreated, refundStatus: http.StatusCreated, balanceAfter: 185_000}
	s.server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubLedger) setRefundStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refundStatus = status
}

func (s *stubLedger) refundCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refunds
}

func (s *stubLedger) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	reserveStatus := s.reserveStatus
	refundStatus := s.refundStatus
	balanceAfter := s.balanceAfter
	switch {
	case r.URL.Path == "/v1/ledger/reserve":
		s.reserves++
	case r.URL.Path == "/v1/ledger/refunds":
		s.refunds++
		refundStatus = s.refundStatus
	case strings.HasSuffix(r.URL.Path, "/settle"):
		s.settles++
	case strings.HasSuffix(r.URL.Path, "/release"):
		s.releases++
	case strings.Contains(r.URL.Path, "/entries"):
		s.entryReads++
	}
	s.mu.Unlock()

	// The ledger books the customer credit + suspense debit pair and returns
	// both legs; card-service picks the customer's own CREDIT leg.
	if r.URL.Path == "/v1/ledger/refunds" {
		var body struct {
			AccountID uuid.UUID `json:"account_id"`
			Amount    int64     `json:"amount"`
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		if refundStatus != http.StatusCreated {
			w.WriteHeader(refundStatus)
			_, _ = io.WriteString(w, `{"error":"refund could not be booked"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"entries": []map[string]interface{}{
				{"account_id": uuid.New().String(), "entry_type": "DEBIT", "amount": body.Amount, "balance_after": -body.Amount},
				{"account_id": body.AccountID.String(), "entry_type": "CREDIT", "amount": body.Amount, "balance_after": balanceAfter},
			},
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/v1/ledger/reserve":
		if reserveStatus != http.StatusCreated {
			w.WriteHeader(reserveStatus)
			_, _ = io.WriteString(w, `{"error":"insufficient_funds","message":"available balance too low"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"reservation_id": uuid.New().String(),
			"status":         "ACTIVE",
			"amount":         1200,
			"currency":       "GBP",
		})
	case strings.HasSuffix(r.URL.Path, "/settle"):
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"entry_id":      uuid.New().String(),
			"entry_type":    "DEBIT",
			"amount":        1200,
			"balance_after": balanceAfter,
			"created_at":    time.Now().UTC(),
		})
	case strings.HasSuffix(r.URL.Path, "/release"):
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "RELEASED"})
	default:
		_ = json.NewEncoder(w).Encode([]interface{}{})
	}
}

func (s *stubLedger) counts() (reserves, settles, releases int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserves, s.settles, s.releases
}

func (s *stubLedger) setReserveStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reserveStatus = status
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func activeCard() *domain.Card {
	card := domain.NewCard(uuid.New(), uuid.New(), "4242", domain.CardTypePhysical, 0, 500_00, 2_000_00, "GBP")
	return card
}

func presentment() *domain.AuthorizeCardRequest {
	return &domain.AuthorizeCardRequest{
		Amount:           1200,
		Currency:         "GBP",
		Merchant:         "Tesco",
		MerchantCategory: "5411",
		MerchantCity:     "London",
		MerchantCountry:  "GB",
		TerminalID:       "POS-4471",
	}
}

// ── The decision path ───────────────────────────────────────────────────────

func TestApprovedPresentmentTakesARealHoldInTheLedger(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusApproved || auth.Decision != domain.AuthDecisionApprove {
		t.Fatalf("decision = %s/%s, want APPROVE/APPROVED", auth.Decision, auth.Status)
	}
	if auth.ReservationID == uuid.Nil {
		t.Fatal("approved authorization has no ledger reservation: the customer's money was never held")
	}
	if auth.RiskScore == 0 {
		t.Fatal("approved authorization carries no risk score")
	}
	if reserves, settles, _ := h.ledger.counts(); reserves != 1 || settles != 0 {
		t.Fatalf("ledger saw reserves=%d settles=%d, want 1 reserve and no settle at authorization time", reserves, settles)
	}
	stored := h.auths.stored()
	if len(stored) != 1 {
		t.Fatalf("persisted %d authorization rows, want 1 (every presentment must be audited, approved or not)", len(stored))
	}
}

func TestRiskEngineUnavailableFailsClosed(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	h.fraud.setStatus(http.StatusServiceUnavailable)

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusDeclined {
		t.Fatalf("status = %s, want DECLINED when the risk engine cannot answer", auth.Status)
	}
	if auth.DeclineReason != "risk_service_unavailable" {
		t.Fatalf("decline reason = %q, want risk_service_unavailable", auth.DeclineReason)
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatalf("ledger held funds (%d reservations) without a risk decision", reserves)
	}
	if len(h.auths.stored()) != 1 {
		t.Fatal("a fail-closed decline was not persisted for audit")
	}
}

func TestInsufficientFundsDeclinesWithoutReservation(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	h.ledger.setReserveStatus(http.StatusConflict)

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusDeclined || auth.DeclineReason != "insufficient_funds" {
		t.Fatalf("got %s/%q, want DECLINED/insufficient_funds", auth.Status, auth.DeclineReason)
	}
	if auth.ReservationID != uuid.Nil {
		t.Fatalf("declined authorization carries reservation %s", auth.ReservationID)
	}
}

func TestLedgerUnavailableDeclinesRatherThanApprovingUnfunded(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	h.ledger.setReserveStatus(http.StatusInternalServerError)

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusDeclined || auth.DeclineReason != "ledger_unavailable" {
		t.Fatalf("got %s/%q, want DECLINED/ledger_unavailable", auth.Status, auth.DeclineReason)
	}
}

func TestRiskReviewIsNotTreatedAsApproval(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	h.fraud.set("REVIEW")

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusDeclined || auth.DeclineReason != "flagged_for_review" {
		t.Fatalf("got %s/%q, want DECLINED/flagged_for_review", auth.Status, auth.DeclineReason)
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatal("a REVIEW decision held funds before a human looked at it")
	}
}

// ── Cheap declines: no downstream calls ─────────────────────────────────────

func TestInactiveCardDeclinesWithoutCallingDownstream(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	card.Status = domain.CardStatusFrozen

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusDeclined || auth.DeclineReason != "card_not_active" {
		t.Fatalf("got %s/%q, want DECLINED/card_not_active", auth.Status, auth.DeclineReason)
	}
	if h.fraud.callCount() != 0 {
		t.Fatal("frozen card still called the risk engine")
	}
	if reserves, _, _ := h.ledger.counts(); reserves != 0 {
		t.Fatal("frozen card still held funds")
	}
}

func TestCurrencyMismatchDeclines(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())
	req := presentment()
	req.Currency = "USD"

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, req)
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	if auth.Status != domain.AuthStatusDeclined || auth.DeclineReason != "currency_mismatch" {
		t.Fatalf("got %s/%q, want DECLINED/currency_mismatch", auth.Status, auth.DeclineReason)
	}
}

func TestCustomerSpendingControlsDeclineBeforeTheRiskEngine(t *testing.T) {
	off := false
	on := true

	cases := []struct {
		name       string
		configure  func(*domain.Card)
		request    func(*domain.AuthorizeCardRequest)
		wantReason string
	}{
		{
			name: "online payments switched off",
			configure: func(c *domain.Card) {
				c.UpdateChannelControls(&off, nil, nil)
			},
			request: func(r *domain.AuthorizeCardRequest) {
				r.Merchant = "Amazon"
				r.TerminalID = "ECOM"
			},
			wantReason: "online_payments_disabled",
		},
		{
			name: "ATM withdrawals switched off",
			configure: func(c *domain.Card) {
				c.UpdateChannelControls(nil, &off, nil)
			},
			request: func(r *domain.AuthorizeCardRequest) {
				r.Merchant = "Cash Machine"
				r.TerminalID = "ATM"
			},
			wantReason: "atm_withdrawals_disabled",
		},
		{
			name: "gambling block switched on",
			configure: func(c *domain.Card) {
				c.UpdateChannelControls(nil, nil, &on)
			},
			request: func(r *domain.AuthorizeCardRequest) {
				r.Merchant = "Bet365"
			},
			wantReason: "gambling_block_active",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			card := h.withCard(activeCard())
			tc.configure(card)

			req := presentment()
			tc.request(req)

			auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, req)
			if err != nil {
				t.Fatalf("AuthorizeCard: %v", err)
			}
			if auth.Status != domain.AuthStatusDeclined || auth.DeclineReason != tc.wantReason {
				t.Fatalf("got %s/%q, want DECLINED/%s", auth.Status, auth.DeclineReason, tc.wantReason)
			}
			if h.fraud.callCount() != 0 {
				t.Fatal("the user's own spending control did not short-circuit the risk engine")
			}
			if reserves, _, _ := h.ledger.counts(); reserves != 0 {
				t.Fatal("a channel-blocked presentment still held funds")
			}
		})
	}
}

func TestInvalidPresentmentIsRejectedBeforeAnySideEffect(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	req := presentment()
	req.Amount = 0

	if _, err := h.svc.AuthorizeCard(context.Background(), card.CardID, req); err == nil {
		t.Fatal("zero-amount presentment was accepted")
	}
	if h.fraud.callCount() != 0 {
		t.Fatal("invalid presentment reached the risk engine")
	}
	if len(h.auths.stored()) != 0 {
		t.Fatal("invalid presentment was persisted")
	}
}

// ── Capture / void ──────────────────────────────────────────────────────────

func TestCaptureSettlesTheHoldExactlyOnce(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	auth, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard: %v", err)
	}

	captured, err := h.svc.CaptureAuthorization(context.Background(), card.CardID, auth.AuthorizationID)
	if err != nil {
		t.Fatalf("CaptureAuthorization: %v", err)
	}
	if captured.Status != domain.AuthStatusCaptured || captured.CapturedAt == nil {
		t.Fatalf("status = %s (captured_at=%v), want CAPTURED with a timestamp", captured.Status, captured.CapturedAt)
	}
	if _, settles, _ := h.ledger.counts(); settles != 1 {
		t.Fatalf("ledger settles = %d, want 1", settles)
	}

	// A merchant that presents the same capture twice must not move money twice.
	if _, err := h.svc.CaptureAuthorization(context.Background(), card.CardID, auth.AuthorizationID); !errors.Is(err, domain.ErrAuthNotCapturable) {
		t.Fatalf("second capture error = %v, want ErrAuthNotCapturable", err)
	}
	if _, settles, _ := h.ledger.counts(); settles != 1 {
		t.Fatalf("ledger settles = %d after duplicate capture, want 1", settles)
	}

	// A different card may not capture this authorization.
	otherCard := uuid.New()
	if _, err := h.svc.CaptureAuthorization(context.Background(), otherCard, auth.AuthorizationID); !errors.Is(err, domain.ErrAuthCardMismatch) {
		t.Fatalf("cross-card capture error = %v, want ErrAuthCardMismatch", err)
	}
}

func TestVoidReleasesTheHoldAndIsGuarded(t *testing.T) {
	h := newHarness(t)
	card := h.withCard(activeCard())

	// A declined presentment has no hold, so it cannot be voided.
	h.fraud.set("DECLINE")
	declined, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard (declined): %v", err)
	}
	if _, err := h.svc.VoidAuthorization(context.Background(), card.CardID, declined.AuthorizationID); !errors.Is(err, domain.ErrAuthNotVoidable) {
		t.Fatalf("voiding a declined authorization error = %v, want ErrAuthNotVoidable", err)
	}

	h.fraud.set("APPROVE")
	approved, err := h.svc.AuthorizeCard(context.Background(), card.CardID, presentment())
	if err != nil {
		t.Fatalf("AuthorizeCard (approved): %v", err)
	}

	voided, err := h.svc.VoidAuthorization(context.Background(), card.CardID, approved.AuthorizationID)
	if err != nil {
		t.Fatalf("VoidAuthorization: %v", err)
	}
	if voided.Status != domain.AuthStatusVoided || voided.VoidedAt == nil {
		t.Fatalf("status = %s (voided_at=%v), want VOIDED with a timestamp", voided.Status, voided.VoidedAt)
	}
	if _, _, releases := h.ledger.counts(); releases != 1 {
		t.Fatalf("ledger releases = %d, want 1", releases)
	}
	if _, settles, _ := h.ledger.counts(); settles != 0 {
		t.Fatal("voiding an authorization settled money")
	}
}

var _ repository.CardRepository = (*fakeCardRepo)(nil)
var _ repository.CardAuthorizationRepository = (*fakeAuthRepo)(nil)
