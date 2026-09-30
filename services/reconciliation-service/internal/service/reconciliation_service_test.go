package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/reconciliation-service/internal/clients"
	"github.com/nexora/nexora/services/reconciliation-service/internal/domain"
)

// ── Test doubles ────────────────────────────────────────────────────────────

// fakeRepo is an in-memory stand-in for the Cassandra repository. Reads hand out
// copies, the way a real scan does, so a service that mutates what it read
// cannot pass a test it would fail against Cassandra.
type fakeRepo struct {
	mu        sync.Mutex
	cases     map[uuid.UUID]*domain.ReconciliationCase
	byPayment map[uuid.UUID]uuid.UUID
	records   map[uuid.UUID]*domain.ReconciliationRecord
	// onListCases fires before every status scan, so a test can observe a sweep
	// without sleeping.
	onListCases func()
}

func newFakeReconRepo() *fakeRepo {
	return &fakeRepo{
		cases:     map[uuid.UUID]*domain.ReconciliationCase{},
		byPayment: map[uuid.UUID]uuid.UUID{},
		records:   map[uuid.UUID]*domain.ReconciliationRecord{},
	}
}

func (r *fakeRepo) CreateCase(ctx context.Context, c *domain.ReconciliationCase) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *c
	r.cases[c.CaseID] = &stored
	r.byPayment[c.PaymentID] = c.CaseID
	return nil
}

func (r *fakeRepo) GetCaseByID(ctx context.Context, id uuid.UUID) (*domain.ReconciliationCase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		return nil, fmt.Errorf("reconciliation case not found")
	}
	copied := *c
	return &copied, nil
}

func (r *fakeRepo) GetCaseByPaymentID(ctx context.Context, paymentID uuid.UUID) (*domain.ReconciliationCase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byPayment[paymentID]
	if !ok {
		return nil, fmt.Errorf("reconciliation case not found for payment")
	}
	c, ok := r.cases[id]
	if !ok {
		return nil, fmt.Errorf("reconciliation case not found for payment")
	}
	copied := *c
	return &copied, nil
}

func (r *fakeRepo) GetCasesByStatus(ctx context.Context, status domain.ReconciliationStatus, limit int) ([]*domain.ReconciliationCase, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.onListCases != nil {
		r.onListCases()
	}
	if limit <= 0 {
		limit = 100
	}
	out := []*domain.ReconciliationCase{}
	for _, c := range r.cases {
		if c.Status != status {
			continue
		}
		copied := *c
		out = append(out, &copied)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *fakeRepo) UpdateCaseStatus(ctx context.Context, id uuid.UUID, status domain.ReconciliationStatus, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		return fmt.Errorf("reconciliation case not found")
	}
	c.Status = status
	c.DiscrepancyReason = reason
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (r *fakeRepo) UpdateCaseResolution(ctx context.Context, id uuid.UUID, resolution domain.ResolutionType, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		return fmt.Errorf("reconciliation case not found")
	}
	now := time.Now().UTC()
	c.Resolution = resolution
	c.Status = domain.ReconciliationStatusResolved
	c.DiscrepancyReason = reason
	c.UpdatedAt = now
	c.ResolvedAt = &now
	return nil
}

func (r *fakeRepo) UpdateCaseExternalState(ctx context.Context, id uuid.UUID, externalState string, externalAmount int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		return fmt.Errorf("reconciliation case not found")
	}
	c.ExternalState = externalState
	c.ExternalAmount = externalAmount
	return nil
}

func (r *fakeRepo) IncrementAttemptCount(ctx context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		return fmt.Errorf("reconciliation case not found")
	}
	c.AttemptCount++
	return nil
}

func (r *fakeRepo) CreateRecord(ctx context.Context, record *domain.ReconciliationRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := *record
	r.records[record.RecordID] = &stored
	return nil
}

func (r *fakeRepo) GetRecordByID(ctx context.Context, id uuid.UUID) (*domain.ReconciliationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[id]
	if !ok {
		return nil, fmt.Errorf("record not found")
	}
	copied := *rec
	return &copied, nil
}

func (r *fakeRepo) GetRecordsByStatus(ctx context.Context, status domain.ReconciliationStatus) ([]*domain.ReconciliationRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []*domain.ReconciliationRecord{}
	for _, rec := range r.records {
		if rec.Status != status {
			continue
		}
		copied := *rec
		out = append(out, &copied)
	}
	return out, nil
}

func (r *fakeRepo) UpdateRecordStatus(ctx context.Context, id uuid.UUID, status domain.ReconciliationStatus, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[id]
	if !ok {
		return fmt.Errorf("record not found")
	}
	rec.Status = status
	rec.DiscrepancyReason = reason
	return nil
}

func (r *fakeRepo) caseCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cases)
}

func (r *fakeRepo) statusOf(t *testing.T, id uuid.UUID) domain.ReconciliationStatus {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		t.Fatalf("case %s not found", id)
	}
	return c.Status
}

func (r *fakeRepo) attemptsOf(t *testing.T, id uuid.UUID) int {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.cases[id]
	if !ok {
		t.Fatalf("case %s not found", id)
	}
	return c.AttemptCount
}

func (r *fakeRepo) caseFor(t *testing.T, paymentID uuid.UUID) *domain.ReconciliationCase {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byPayment[paymentID]
	if !ok {
		t.Fatalf("no case for payment %s", paymentID)
	}
	copied := *r.cases[id]
	return &copied
}

type resolveCall struct {
	paymentID string
	outcome   string
	reason    string
}

// fakeGateway stands in for payment-service: it reports a payment's state and
// records every outcome written back to it.
type fakeGateway struct {
	mu         sync.Mutex
	states     map[string]string
	getErr     error
	resolveErr error
	calls      []resolveCall
}

func newFakeGateway(states map[string]string) *fakeGateway {
	if states == nil {
		states = map[string]string{}
	}
	return &fakeGateway{states: states}
}

func (g *fakeGateway) GetPayment(ctx context.Context, paymentID string) (*clients.PaymentSnapshot, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.getErr != nil {
		return nil, g.getErr
	}
	state, ok := g.states[paymentID]
	if !ok {
		state = "UNKNOWN"
	}
	return &clients.PaymentSnapshot{PaymentID: paymentID, State: state, Amount: 1_000, Currency: "GBP"}, nil
}

func (g *fakeGateway) ResolveUnknown(ctx context.Context, paymentID, outcome, reason string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.resolveErr != nil {
		return g.resolveErr
	}
	g.calls = append(g.calls, resolveCall{paymentID: paymentID, outcome: outcome, reason: reason})
	g.states[paymentID] = outcome
	return nil
}

func (g *fakeGateway) resolveCalls() []resolveCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]resolveCall, len(g.calls))
	copy(out, g.calls)
	return out
}

func newTestReconService(repo *fakeRepo) *ReconciliationService {
	return NewReconciliationService(repo, nil, zerolog.Nop())
}

// ── Filing the UNKNOWN bucket ───────────────────────────────────────────────

// A payment that goes UNKNOWN must end up in front of the sweep. This is the
// consumer's job (payment.unknown), and it must be safe to redeliver: at-least-
// once delivery means the same event can arrive twice.
func TestUnknownPaymentOpensExactlyOneCase(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()

	first, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 12_500, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}
	if first.Status != domain.ReconciliationStatusPending {
		t.Fatalf("status = %s, want PENDING", first.Status)
	}
	if first.InternalState != "UNKNOWN" {
		t.Fatalf("internal state = %s, want UNKNOWN", first.InternalState)
	}
	if first.MaxAttempts <= 0 {
		t.Fatalf("max attempts = %d: an unfailable case would retry forever", first.MaxAttempts)
	}

	// The same event arrives again.
	second, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 12_500, "GBP")
	if err != nil {
		t.Fatalf("redelivered event: %v", err)
	}
	if second.CaseID != first.CaseID {
		t.Fatalf("redelivery created a second case: %s then %s", first.CaseID, second.CaseID)
	}
	if repo.caseCount() != 1 {
		t.Fatalf("%d cases for one payment, want 1", repo.caseCount())
	}
}

// Two sides that both say "we do not know" are not a match. Treating them as one
// would close every case the instant it was opened, which is the difference
// between a reconciliation sweep and a rubber stamp.
func TestSweepDoesNotMatchTwoUnknownStates(t *testing.T) {
	repo := newFakeReconRepo()
	gateway := newFakeGateway(nil)
	svc := newTestReconService(repo)
	svc.SetPaymentGateway(gateway)

	paymentID := uuid.New()
	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 900, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	results, err := svc.RunScheduledReconciliation(context.Background(), 10)
	if err != nil {
		t.Fatalf("RunScheduledReconciliation: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("processed %d cases, want 1", len(results))
	}
	if results[0].Matched {
		t.Fatal("an unresolved UNKNOWN case was reported as matched")
	}
	if status := repo.statusOf(t, opened.CaseID); status != domain.ReconciliationStatusPending {
		t.Fatalf("status = %s, want the case left PENDING", status)
	}
	if calls := gateway.resolveCalls(); len(calls) != 0 {
		t.Fatalf("nothing was established about this payment, yet %d outcomes were written back", len(calls))
	}
}

// When nothing can establish what happened, the case must stop retrying and go
// to a human. A case that retries forever is a case nobody ever notices.
func TestSweepEscalatesAfterMaxAttempts(t *testing.T) {
	repo := newFakeReconRepo()
	gateway := newFakeGateway(nil) // payment stays UNKNOWN
	svc := newTestReconService(repo)
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), uuid.New(), 4_000, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	for i := 0; i < opened.MaxAttempts; i++ {
		if _, err := svc.RunScheduledReconciliation(context.Background(), 10); err != nil {
			t.Fatalf("sweep %d: %v", i+1, err)
		}
	}

	if status := repo.statusOf(t, opened.CaseID); status != domain.ReconciliationStatusEscalated {
		t.Fatalf("status after %d sweeps = %s, want ESCALATED", opened.MaxAttempts, status)
	}
	if attempts := repo.attemptsOf(t, opened.CaseID); attempts < opened.MaxAttempts {
		t.Fatalf("attempts = %d, want at least %d before escalating", attempts, opened.MaxAttempts)
	}
}

// The sweep reads the payment rather than trusting a copy of it: a late provider
// callback may have settled the payment between sweeps, and the case must close
// against the payment instead of retrying work that is already done.
func TestSweepClosesCaseWhenPaymentAlreadyConcluded(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(map[string]string{paymentID.String(): "SETTLED"})
	svc.SetPaymentGateway(gateway)

	if _, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 2_000, "GBP"); err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	results, err := svc.RunScheduledReconciliation(context.Background(), 10)
	if err != nil {
		t.Fatalf("RunScheduledReconciliation: %v", err)
	}
	if len(results) != 1 || !results[0].Matched {
		t.Fatalf("case was not closed against a settled payment: %+v", results)
	}
	if results[0].ExternalState != "SETTLED" {
		t.Errorf("external state = %s, want SETTLED recorded from the payment", results[0].ExternalState)
	}
	// Closing is recorded the way the Cassandra repository records a
	// resolution: RESOLVED with an AUTO_MATCH resolution.
	closed := repo.caseFor(t, paymentID)
	if closed.Status != domain.ReconciliationStatusResolved {
		t.Errorf("status = %s, want RESOLVED (closed against the payment)", closed.Status)
	}
	if closed.Resolution != domain.ResolutionTypeAutoMatch {
		t.Errorf("resolution = %s, want AUTO_MATCH", closed.Resolution)
	}
	if closed.ExternalState != "SETTLED" {
		t.Errorf("recorded external state = %s, want SETTLED", closed.ExternalState)
	}
	// The payment concluded itself, so there is nothing to write back.
	if calls := gateway.resolveCalls(); len(calls) != 0 {
		t.Errorf("wrote an outcome back to a payment that had already concluded: %+v", calls)
	}
}

// The case holding the provider's answer is the evidence that concludes the
// payment. Closing the case while the payment stays UNKNOWN would leave the
// customer's hold in place for a decision already taken — the exact gap between
// a "decline path" and a reconciliation sweep.
func TestSweepWritesCaseEvidenceBackToThePayment(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(nil) // payment still UNKNOWN
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 3_300, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}
	// A provider statement says the payment failed after all.
	if err := repo.UpdateCaseExternalState(context.Background(), opened.CaseID, "FAILED", 3_300); err != nil {
		t.Fatalf("recording external state: %v", err)
	}

	if _, err := svc.RunScheduledReconciliation(context.Background(), 10); err != nil {
		t.Fatalf("RunScheduledReconciliation: %v", err)
	}

	calls := gateway.resolveCalls()
	if len(calls) != 1 {
		t.Fatalf("write-backs = %d, want exactly 1", len(calls))
	}
	if calls[0].outcome != clients.ResolutionOutcomeFailed {
		t.Errorf("outcome = %s, want FAILED", calls[0].outcome)
	}
	if calls[0].paymentID != paymentID.String() {
		t.Errorf("write-back targeted %s, want %s", calls[0].paymentID, paymentID)
	}

	resolved := repo.caseFor(t, paymentID)
	if resolved.Status != domain.ReconciliationStatusResolved {
		t.Errorf("status = %s, want RESOLVED", resolved.Status)
	}
	if resolved.Resolution != domain.ResolutionTypeProviderOverride {
		t.Errorf("resolution = %s, want PROVIDER_OVERRIDE", resolved.Resolution)
	}
}

// A case must not be closed while the payment is still unknown because
// payment-service was unreachable: the case stays open and keeps its attempts
// counting, so an outage escalates instead of silently "resolving" cases.
func TestSweepCountsAnAttemptWhenThePaymentCannotBeRead(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(nil)
	gateway.getErr = errors.New("payment service unreachable")
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 700, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	results, err := svc.RunScheduledReconciliation(context.Background(), 10)
	if err != nil {
		t.Fatalf("RunScheduledReconciliation: %v", err)
	}
	if len(results) != 1 || results[0].Matched {
		t.Fatalf("an unreadable payment was reported as resolved: %+v", results)
	}
	if results[0].Discrepancy == "" {
		t.Error("an unreadable payment left no discrepancy on the result")
	}
	if attempts := repo.attemptsOf(t, opened.CaseID); attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if status := repo.statusOf(t, opened.CaseID); status == domain.ReconciliationStatusResolved {
		t.Error("an unreadable payment resolved the case")
	}
}

// ── Resolving a case by hand ────────────────────────────────────────────────

func TestResolveCaseWritesTheOutcomeBackBeforeClosing(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(nil)
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 6_600, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	resolved, err := svc.ResolveCase(context.Background(), opened.CaseID, "CONFIRMED", domain.ResolutionTypeProviderOverride, "ops@nexora", "provider statement matched")
	if err != nil {
		t.Fatalf("ResolveCase: %v", err)
	}
	if resolved.Status != domain.ReconciliationStatusResolved {
		t.Fatalf("status = %s, want RESOLVED", resolved.Status)
	}

	calls := gateway.resolveCalls()
	if len(calls) != 1 || calls[0].outcome != clients.ResolutionOutcomeConfirmed {
		t.Fatalf("write-backs = %+v, want one CONFIRMED", calls)
	}
}

// If the write-back fails, the case must stay open. A case marked resolved while
// the payment is still UNKNOWN hides the fact that the customer's money is still
// held, which is worse than an obviously stuck case.
func TestResolveCaseStaysOpenWhenWriteBackFails(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(nil)
	gateway.resolveErr = errors.New("payment service unreachable")
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 5_000, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	if _, err := svc.ResolveCase(context.Background(), opened.CaseID, "FAILED", domain.ResolutionTypeProviderOverride, "ops@nexora", "rail rejected"); err == nil {
		t.Fatal("ResolveCase reported success even though the payment was never updated")
	}
	if status := repo.statusOf(t, opened.CaseID); status == domain.ReconciliationStatusResolved {
		t.Fatalf("case status = %s, want the case kept open", status)
	}
}

// Re-running a resolution must not touch the payment again: every write-back of
// a CONFIRMED outcome settles a payment, and settling twice would double-book
// the money.
func TestResolveCaseIsIdempotent(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(nil)
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 1_800, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	if _, err := svc.ResolveCase(context.Background(), opened.CaseID, "CONFIRMED", domain.ResolutionTypeProviderOverride, "ops@nexora", "provider statement matched"); err != nil {
		t.Fatalf("first ResolveCase: %v", err)
	}
	if _, err := svc.ResolveCase(context.Background(), opened.CaseID, "CONFIRMED", domain.ResolutionTypeProviderOverride, "ops@nexora", "provider statement matched"); err != nil {
		t.Fatalf("repeated ResolveCase: %v", err)
	}

	if calls := gateway.resolveCalls(); len(calls) != 1 {
		t.Fatalf("write-backs = %d, want exactly 1 across two resolutions", len(calls))
	}
}

// An accounting decision is not evidence about the rail. Marking a case resolved
// as a write-off must not claim the payment settled.
func TestResolutionWithoutPaymentEvidenceDoesNotTouchThePayment(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	paymentID := uuid.New()
	gateway := newFakeGateway(nil)
	svc.SetPaymentGateway(gateway)

	opened, err := svc.OpenCaseForUnknownPayment(context.Background(), paymentID, 2_400, "GBP")
	if err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	if _, err := svc.ResolveCase(context.Background(), opened.CaseID, "", domain.ResolutionTypeWriteOff, "finance@nexora", "written off to the suspense account"); err != nil {
		t.Fatalf("ResolveCase: %v", err)
	}
	if calls := gateway.resolveCalls(); len(calls) != 0 {
		t.Fatalf("a write-off claimed a payment outcome: %+v", calls)
	}
	if status := repo.statusOf(t, opened.CaseID); status != domain.ReconciliationStatusResolved {
		t.Errorf("status = %s, want RESOLVED", status)
	}
}

// ── The sweep runs by itself ────────────────────────────────────────────────

// A sweep nobody calls is not a reconciliation system. Scheduler start-up must
// work the backlog immediately rather than waiting a full interval.
func TestSchedulerSweepsAtStartup(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)
	svc.SetPaymentGateway(newFakeGateway(nil))

	if _, err := svc.OpenCaseForUnknownPayment(context.Background(), uuid.New(), 1_000, "GBP"); err != nil {
		t.Fatalf("OpenCaseForUnknownPayment: %v", err)
	}

	swept := make(chan struct{}, 1)
	repo.onListCases = func() {
		select {
		case swept <- struct{}{}:
		default:
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.RunScheduler(ctx, time.Hour, 10)
	}()

	select {
	case <-swept:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("the scheduler did not sweep at startup")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the scheduler did not stop when its context was cancelled")
	}
}

// Disabling the interval must disable the scheduler rather than sweep in a tight
// loop.
func TestSchedulerDisabledWithZeroInterval(t *testing.T) {
	repo := newFakeReconRepo()
	svc := newTestReconService(repo)

	swept := false
	repo.onListCases = func() { swept = true }

	svc.RunScheduler(context.Background(), 0, 10)

	if swept {
		t.Fatal("a disabled scheduler still swept")
	}
}
