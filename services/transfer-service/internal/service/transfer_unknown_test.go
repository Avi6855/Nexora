package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/transfer-service/internal/clients"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
)

// These tests cover the account-to-account half of ADR-007. A booking that
// timed out is not a booking that was refused: the ledger may have moved the
// money and only the answer was lost. The transfer must therefore end up
// UNKNOWN with a way back, not FAILED with the customer told their money never
// moved.

// ── Test doubles ────────────────────────────────────────────────────────────

type fakeTransferRepo struct {
	mu        sync.Mutex
	transfers map[uuid.UUID]*domain.Transfer
	byKey     map[string]uuid.UUID
}

func newFakeTransferRepo() *fakeTransferRepo {
	return &fakeTransferRepo{
		transfers: map[uuid.UUID]*domain.Transfer{},
		byKey:     map[string]uuid.UUID{},
	}
}

func (r *fakeTransferRepo) Create(ctx context.Context, transfer *domain.Transfer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *transfer
	r.transfers[transfer.TransferID] = &stored
	r.byKey[transfer.IdempotencyKey] = transfer.TransferID
	return nil
}

func (r *fakeTransferRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	transfer, ok := r.transfers[id]
	if !ok {
		return nil, errNotFound
	}
	copied := *transfer
	return &copied, nil
}

func (r *fakeTransferRepo) GetByIdempotencyKey(ctx context.Context, key string) (*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byKey[key]
	if !ok {
		return nil, nil
	}
	copied := *r.transfers[id]
	return &copied, nil
}

func (r *fakeTransferRepo) GetByFromAccount(ctx context.Context, accountID uuid.UUID) ([]*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []*domain.Transfer{}
	for _, transfer := range r.transfers {
		if transfer.FromAccountID == accountID {
			copied := *transfer
			out = append(out, &copied)
		}
	}
	return out, nil
}

func (r *fakeTransferRepo) GetByStatus(ctx context.Context, status domain.TransferStatus, limit int) ([]*domain.Transfer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []*domain.Transfer{}
	for _, transfer := range r.transfers {
		if transfer.Status != status {
			continue
		}
		copied := *transfer
		out = append(out, &copied)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *fakeTransferRepo) Update(ctx context.Context, transfer *domain.Transfer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copied := *transfer
	r.transfers[transfer.TransferID] = &copied
	return nil
}

// backdate makes a transfer look older than it is, so the sweep's escalation
// window can be exercised without waiting.
func (r *fakeTransferRepo) backdate(id uuid.UUID, age time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.transfers[id].CreatedAt = time.Now().Add(-age)
}

var errNotFound = &notFoundError{}

type notFoundError struct{}

func (e *notFoundError) Error() string { return "transfer not found" }

type recordingSink struct {
	mu     sync.Mutex
	events []string
}

func (s *recordingSink) Publish(ctx context.Context, eventType string, payload interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, eventType)
	return nil
}

func (s *recordingSink) published() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.events))
	copy(out, s.events)
	return out
}

func (s *recordingSink) has(eventType string) bool {
	for _, e := range s.published() {
		if e == eventType {
			return true
		}
	}
	return false
}

// fakeLedger doubles as the ledger of record: it records the idempotency key of
// every booking attempt and can be told to answer 201, 402 or a 5xx (the
// indeterminate case: the answer was lost, the booking may have committed).
type fakeLedger struct {
	mu      sync.Mutex
	keys    []string
	refuse  bool
	unavail bool
}

func (l *fakeLedger) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
		}
		raw, _ := json.Marshal(map[string]interface{}{})
		_ = json.Unmarshal(raw, &body)
		_ = json.NewDecoder(r.Body).Decode(&body)

		l.mu.Lock()
		l.keys = append(l.keys, body.IdempotencyKey)
		refuse, unavail := l.refuse, l.unavail
		l.mu.Unlock()

		if unavail {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"ledger unavailable"}`))
			return
		}
		if refuse {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write([]byte(`{"error":"insufficient funds"}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"transaction": map[string]interface{}{"transaction_id": uuid.New().String(), "status": "POSTED"},
			"entries":     []interface{}{},
		})
	})
}

func (l *fakeLedger) calls() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.keys))
	copy(out, l.keys)
	return out
}

func (l *fakeLedger) setMode(refuse, unavail bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.refuse = refuse
	l.unavail = unavail
}

type transferFixture struct {
	service *TransferService
	repo    *fakeTransferRepo
	ledger  *fakeLedger
	sink    *recordingSink
	userID  uuid.UUID
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()

	userID := uuid.New()

	ledger := &fakeLedger{}
	ledgerServer := httptest.NewServer(ledger.handler())
	t.Cleanup(ledgerServer.Close)
	t.Setenv("LEDGER_SERVICE_URL", ledgerServer.URL)

	accountServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"account_id":       strings.TrimPrefix(r.URL.Path, "/v1/accounts/"),
			"user_id":          userID.String(),
			"status":           "ACTIVE",
			"lockdown_enabled": false,
		})
	}))
	t.Cleanup(accountServer.Close)
	t.Setenv("ACCOUNT_SERVICE_URL", accountServer.URL)

	repo := newFakeTransferRepo()
	sink := &recordingSink{}
	svc := NewTransferService(repo, clients.NewLedgerClient(), clients.NewAccountClient(), sink, zerolog.Nop())

	return &transferFixture{service: svc, repo: repo, ledger: ledger, sink: sink, userID: userID}
}

func (f *transferFixture) create(t *testing.T, key string, amount int64) *domain.Transfer {
	t.Helper()
	transfer, err := f.service.CreateTransfer(context.Background(), f.userID, &domain.CreateTransferRequest{
		IdempotencyKey: key,
		FromAccountID:  uuid.New().String(),
		ToAccountID:    uuid.New().String(),
		Amount:         amount,
		Currency:       "GBP",
		Description:    "rent",
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	return transfer
}

func (f *transferFixture) stored(t *testing.T, id uuid.UUID) *domain.Transfer {
	t.Helper()
	transfer, err := f.repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reading stored transfer: %v", err)
	}
	return transfer
}

// ── The indeterminate booking ───────────────────────────────────────────────

// A ledger timeout is not a refusal. Recording FAILED here would tell the
// customer their transfer did not happen while the ledger may have booked it —
// and would also make the retry path refuse to resume, because a failed
// transfer is not resumable.
func TestIndeterminateBookingIsRecordedAsUnknown(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	transfer := f.create(t, "indeterminate-key", 12_000)

	if transfer.Status != domain.TransferStatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN", transfer.Status)
	}
	if stored := f.stored(t, transfer.TransferID); stored.Status != domain.TransferStatusUnknown {
		t.Fatalf("stored status = %s, want UNKNOWN", stored.Status)
	}
	if !f.sink.has("transfer.unknown") {
		t.Errorf("the indeterminate transfer was not published for reconciliation: %v", f.sink.published())
	}
	if f.sink.has("transfer.failed") {
		t.Errorf("an indeterminate booking was published as failed: %v", f.sink.published())
	}
}

// A definite refusal is different: the ledger said no, the money did not move.
func TestRefusedBookingIsRecordedAsFailed(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(true, false)

	_, err := f.service.CreateTransfer(context.Background(), f.userID, &domain.CreateTransferRequest{
		IdempotencyKey: "refused-key",
		FromAccountID:  uuid.New().String(),
		ToAccountID:    uuid.New().String(),
		Amount:         500,
		Currency:       "GBP",
	})
	if err == nil {
		t.Fatal("a refused booking was reported as accepted")
	}
	if !isInsufficientFunds(err) {
		t.Fatalf("error = %v, want an insufficient-funds refusal", err)
	}

	stored, lookupErr := f.repo.GetByIdempotencyKey(context.Background(), "refused-key")
	if lookupErr != nil || stored == nil {
		t.Fatalf("stored transfer lookup: %v", lookupErr)
	}
	if stored.Status != domain.TransferStatusFailed {
		t.Fatalf("stored status = %s, want FAILED", stored.Status)
	}
	if f.sink.has("transfer.unknown") {
		t.Errorf("a definite refusal was recorded as indeterminate: %v", f.sink.published())
	}
}

// The retry that makes the whole thing recoverable: the same idempotency key
// goes to the ledger again, and the ledger's own exactly-once booking decides
// whether that completes the original transfer or books it now.
func TestRetryResumesAnIndeterminateTransferWithTheSameLedgerKey(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	first := f.create(t, "resume-key", 7_500)
	if first.Status != domain.TransferStatusUnknown {
		t.Fatalf("setup failed: status = %s, want UNKNOWN", first.Status)
	}

	// The ledger recovers.
	f.ledger.setMode(false, false)

	resumed := f.create(t, "resume-key", 7_500)
	if resumed.TransferID != first.TransferID {
		t.Fatalf("the retry created a second transfer: %s then %s", first.TransferID, resumed.TransferID)
	}
	if resumed.Status != domain.TransferStatusCompleted {
		t.Fatalf("status = %s, want COMPLETED after the retry", resumed.Status)
	}
	if resumed.CompletedAt == nil {
		t.Error("a completed transfer has no completion time")
	}

	keys := f.ledger.calls()
	if len(keys) != 2 {
		t.Fatalf("ledger saw %d booking attempts, want 2", len(keys))
	}
	if keys[0] != keys[1] {
		t.Fatalf("the retry used a different ledger key (%q then %q): that is how money moves twice", keys[0], keys[1])
	}
}

// ── The sweep ───────────────────────────────────────────────────────────────

func TestSweepSettlesAnIndeterminateTransfer(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	transfer := f.create(t, "sweep-settle-key", 3_000)
	f.ledger.setMode(false, false)

	summary, err := f.service.RunUnknownSweep(context.Background(), 10)
	if err != nil {
		t.Fatalf("RunUnknownSweep: %v", err)
	}
	if summary.Examined != 1 || summary.Completed != 1 {
		t.Fatalf("summary = %+v, want one transfer examined and completed", summary)
	}
	if stored := f.stored(t, transfer.TransferID); stored.Status != domain.TransferStatusCompleted {
		t.Fatalf("stored status = %s, want COMPLETED", stored.Status)
	}
	if !f.sink.has("transfer.completed") {
		t.Errorf("the settled transfer was not published: %v", f.sink.published())
	}
}

func TestSweepFailsAnIndeterminateTransferTheLedgerRefuses(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	transfer := f.create(t, "sweep-refuse-key", 3_000)
	f.ledger.setMode(true, false)

	summary, err := f.service.RunUnknownSweep(context.Background(), 10)
	if err != nil {
		t.Fatalf("RunUnknownSweep: %v", err)
	}
	if summary.Failed != 1 {
		t.Fatalf("summary = %+v, want the transfer resolved as failed", summary)
	}
	if stored := f.stored(t, transfer.TransferID); stored.Status != domain.TransferStatusFailed {
		t.Fatalf("stored status = %s, want FAILED", stored.Status)
	}
}

// Retrying is only sensible while the ledger might still answer. Past the
// escalation window the sweep stops guessing and asks for a human, which is what
// keeps a stuck transfer from being retried forever.
func TestSweepEscalatesPastTheEscalationWindow(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	transfer := f.create(t, "escalate-key", 1_400)
	f.repo.backdate(transfer.TransferID, 30*time.Minute)
	f.ledger.setMode(false, false) // a retry would now succeed

	before := len(f.ledger.calls())
	summary, err := f.service.RunUnknownSweep(context.Background(), 10)
	if err != nil {
		t.Fatalf("RunUnknownSweep: %v", err)
	}

	if summary.Escalated != 1 {
		t.Fatalf("summary = %+v, want one escalation", summary)
	}
	if after := len(f.ledger.calls()); after != before {
		t.Errorf("the sweep re-booked a transfer past its escalation window (%d -> %d attempts)", before, after)
	}
	if stored := f.stored(t, transfer.TransferID); stored.Status != domain.TransferStatusUnknown {
		t.Errorf("stored status = %s, want it left UNKNOWN for the human", stored.Status)
	}
	if !f.sink.has("transfer.reconciliation.required") {
		t.Errorf("the escalation was not published: %v", f.sink.published())
	}
}

// A sweep with no interval must not run, so a misconfigured deployment cannot
// spin the ledger client.
func TestSweepSchedulerDisabledWithZeroInterval(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)
	f.create(t, "disabled-scheduler-key", 900)

	before := len(f.ledger.calls())
	f.service.RunUnknownScheduler(context.Background(), 0, 10)

	if after := len(f.ledger.calls()); after != before {
		t.Errorf("a disabled scheduler still re-booked transfers (%d -> %d)", before, after)
	}
}

// ── Resolution by reconciliation ────────────────────────────────────────────

func TestResolveUnknownIsIdempotent(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	transfer := f.create(t, "resolve-key", 2_100)

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := f.service.ResolveUnknown(context.Background(), transfer.TransferID, domain.ResolutionOutcomeConfirmed, "ledger statement matched"); err != nil {
			t.Fatalf("ResolveUnknown attempt %d: %v", attempt, err)
		}
	}

	stored := f.stored(t, transfer.TransferID)
	if stored.Status != domain.TransferStatusCompleted {
		t.Fatalf("status = %s, want COMPLETED", stored.Status)
	}
	if stored.CompletedAt == nil {
		t.Error("a completed transfer has no completion time")
	}
}

func TestResolveUnknownRefusesAnUndefinedOutcome(t *testing.T) {
	f := newTransferFixture(t)
	f.ledger.setMode(false, true)

	transfer := f.create(t, "bad-outcome-key", 800)

	if _, err := f.service.ResolveUnknown(context.Background(), transfer.TransferID, domain.ResolutionOutcome("MAYBE"), ""); err == nil {
		t.Fatal("an undefined resolution outcome was accepted")
	}
	if stored := f.stored(t, transfer.TransferID); stored.Status != domain.TransferStatusUnknown {
		t.Errorf("status = %s, want it left UNKNOWN", stored.Status)
	}
}

func isInsufficientFunds(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "insufficient")
}
