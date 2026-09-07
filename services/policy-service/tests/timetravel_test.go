package tests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
	"github.com/nexora/nexora/services/policy-service/internal/service"
)

// ── In-memory fakes (Cassandra-free unit coverage of the replay logic) ──────

type fakeVersionRepo struct {
	versions map[uuid.UUID][]domain.PolicyVersion
}

func (f *fakeVersionRepo) VersionsFor(_ context.Context, id uuid.UUID) ([]domain.PolicyVersion, error) {
	return f.versions[id], nil
}
func (f *fakeVersionRepo) SaveVersion(_ context.Context, v domain.PolicyVersion) error {
	f.versions[v.PolicyID] = append(f.versions[v.PolicyID], v)
	return nil
}
func (f *fakeVersionRepo) CloseSuperseded(_ context.Context, _ uuid.UUID, _ int, _ time.Time) error {
	return nil
}

type fakeSnapshotRepo struct {
	snaps []domain.CustomerStateSnapshot
}

func (f *fakeSnapshotRepo) SaveSnapshot(_ context.Context, s domain.CustomerStateSnapshot) error {
	f.snaps = append(f.snaps, s)
	return nil
}
func (f *fakeSnapshotRepo) LatestAtOrBefore(_ context.Context, userID uuid.UUID, t time.Time) (*domain.CustomerStateSnapshot, error) {
	var best *domain.CustomerStateSnapshot
	for i := range f.snaps {
		s := &f.snaps[i]
		if s.UserID == userID && s.Covers(t) && (best == nil || s.CapturedAt.After(best.CapturedAt)) {
			best = s
		}
	}
	if best == nil {
		// Mirror the Cassandra repository contract: not-found is the sentinel.
		return nil, fmt.Errorf("no snapshot: %w", domain.ErrNoStateSnapshot)
	}
	return best, nil
}

type fakeHistoryRepo struct {
	records []domain.HistoricalDecisionRecord
}

func (f *fakeHistoryRepo) SaveRecord(_ context.Context, r domain.HistoricalDecisionRecord) error {
	f.records = append(f.records, r)
	return nil
}
func (f *fakeHistoryRepo) GetByPayment(_ context.Context, paymentID string) (*domain.HistoricalDecisionRecord, error) {
	for i := range f.records {
		if f.records[i].Request.PaymentID == paymentID {
			return &f.records[i], nil
		}
	}
	return nil, domain.ErrNoPolicyVersionAtTime
}

type fakePolicyRepo struct {
	policies []*domain.Policy
}

func (f *fakePolicyRepo) Create(_ context.Context, _ *domain.Policy) error { return nil }
func (f *fakePolicyRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Policy, error) {
	return nil, nil
}
func (f *fakePolicyRepo) GetByType(_ context.Context, _ domain.PolicyType) ([]*domain.Policy, error) {
	return nil, nil
}
func (f *fakePolicyRepo) GetByStatus(_ context.Context, _ domain.PolicyStatus) ([]*domain.Policy, error) {
	return f.policies, nil
}
func (f *fakePolicyRepo) GetActivePolicies(_ context.Context) ([]*domain.Policy, error) {
	return f.policies, nil
}
func (f *fakePolicyRepo) GetShadowPolicies(_ context.Context) ([]*domain.Policy, error) {
	return nil, nil
}
func (f *fakePolicyRepo) Update(_ context.Context, _ *domain.Policy) error    { return nil }
func (f *fakePolicyRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ domain.PolicyStatus) error {
	return nil
}
func (f *fakePolicyRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }

func newTT(t *testing.T, policies []*domain.Policy, versions map[uuid.UUID][]domain.PolicyVersion, snaps []domain.CustomerStateSnapshot) (*service.TimeTravelService, *fakeHistoryRepo) {
	t.Helper()
	fr := &fakePolicyRepo{policies: policies}
	fv := &fakeVersionRepo{versions: versions}
	fs := &fakeSnapshotRepo{snaps: snaps}
	fh := &fakeHistoryRepo{}
	logger := zerolog.Nop()
	live := service.NewPolicyService(fr, nil, logger)
	return service.NewTimeTravelService(fr, fv, fs, fh, live, logger), fh
}

// ── Version windows ─────────────────────────────────────────────────────────

func TestPolicyVersionCoversWindow(t *testing.T) {
	v1From := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	v1To := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	v1 := domain.PolicyVersion{PolicyID: uuid.New(), Version: 1, ValidFrom: v1From, ValidTo: v1To}

	if !v1.Covers(time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("mid-window instant must be covered")
	}
	if v1.Covers(v1To) {
		t.Fatal("window is [from, to) — valid_to itself must NOT be covered")
	}
	if v1.Covers(v1From.Add(-time.Second)) {
		t.Fatal("instant before valid_from must not be covered")
	}
	vOpen := domain.PolicyVersion{PolicyID: uuid.New(), Version: 2, ValidFrom: v1To}
	if !vOpen.Covers(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("open-ended version must cover future instants")
	}
}

// ── Historical reconstruction ───────────────────────────────────────────────

func TestTimeTravelReconstructsHistoricalDecision(t *testing.T) {
	policyID := uuid.New()
	// v1 (in force June 2024): blocks amounts > £100.
	v1 := domain.PolicyVersion{
		PolicyID: policyID, Version: 1, Status: domain.PolicyStatusActive,
		Name: "high-value-block", Enabled: true,
		ValidFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		ValidTo:   time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
		Rules: []domain.PolicyRule{
			{RuleID: "r1", Name: "block over 100", Condition: "amount_gt:10000", Action: domain.PolicyDecisionBlock, Enabled: true},
		},
	}
	// v2 (in force from Sep 2024): threshold raised to £500.
	v2 := domain.PolicyVersion{
		PolicyID: policyID, Version: 2, Status: domain.PolicyStatusActive,
		Name: "high-value-block", Enabled: true,
		ValidFrom: time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
		Rules: []domain.PolicyRule{
			{RuleID: "r1", Name: "block over 500", Condition: "amount_gt:50000", Action: domain.PolicyDecisionBlock, Enabled: true},
		},
	}
	policy := &domain.Policy{PolicyID: policyID, Status: domain.PolicyStatusActive, Enabled: true}

	svc, _ := newTT(t, []*domain.Policy{policy}, map[uuid.UUID][]domain.PolicyVersion{policyID: {v1, v2}}, nil)

	pctx := domain.PaymentContext{
		UserID:  uuid.New(),
		Amount:  20000, // £200: blocked under v1, allowed under v2
		Currency: "GBP",
	}

	// June 2024 replay: £200 blocked (v1 threshold £100).
	june, err := svc.EvaluateAtTime(context.Background(), "pay-1", time.Date(2024, 6, 12, 10, 0, 0, 0, time.UTC), pctx)
	if err != nil {
		t.Fatalf("june replay: %v", err)
	}
	if june.FinalDecision != domain.PolicyDecisionBlock {
		t.Fatalf("June 2024: £200 must be BLOCKED under v1, got %s", june.FinalDecision)
	}
	if len(june.PolicyVersions) != 1 || june.PolicyVersions[0].Version != 1 {
		t.Fatalf("June replay must bind policy v1, got %+v", june.PolicyVersions)
	}

	// October 2024 replay: £200 allowed (v2 threshold £500).
	oct, err := svc.EvaluateAtTime(context.Background(), "pay-1", time.Date(2024, 10, 12, 10, 0, 0, 0, time.UTC), pctx)
	if err != nil {
		t.Fatalf("october replay: %v", err)
	}
	if oct.FinalDecision != domain.PolicyDecisionAllow {
		t.Fatalf("Oct 2024: £200 must be ALLOWED under v2, got %s", oct.FinalDecision)
	}
	if oct.PolicyVersions[0].Version != 2 {
		t.Fatalf("October replay must bind policy v2, got v%d", oct.PolicyVersions[0].Version)
	}

	// The two reconstructions must hash differently — different inputs, different answers.
	if june.ReplayHash == oct.ReplayHash {
		t.Fatal("different governing versions must produce different replay hashes")
	}
}

func TestTimeTravelDeterministicHashStable(t *testing.T) {
	policyID := uuid.New()
	v1 := domain.PolicyVersion{
		PolicyID: policyID, Version: 1, Name: "p", Enabled: true,
		ValidFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Rules: []domain.PolicyRule{
			{RuleID: "r1", Condition: "always", Action: domain.PolicyDecisionStepUp, Enabled: true},
		},
	}
	svc, _ := newTT(t, []*domain.Policy{{PolicyID: policyID, Status: domain.PolicyStatusActive}},
		map[uuid.UUID][]domain.PolicyVersion{policyID: {v1}}, nil)

	asOf := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	pctx := domain.PaymentContext{UserID: uuid.New(), Amount: 5, Currency: "GBP"}

	a, err := svc.EvaluateAtTime(context.Background(), "pay-9", asOf, pctx)
	if err != nil {
		t.Fatalf("first replay: %v", err)
	}
	b, err := svc.EvaluateAtTime(context.Background(), "pay-9", asOf, pctx)
	if err != nil {
		t.Fatalf("second replay: %v", err)
	}
	if a.ReplayHash != b.ReplayHash {
		t.Fatal("same inputs must reconstruct to the identical replay hash")
	}
	if a.FinalDecision != domain.PolicyDecisionStepUp {
		t.Fatalf("always-rule must step up, got %s", a.FinalDecision)
	}
}

func TestTimeTravelUsesSnapshotInForceAtTime(t *testing.T) {
	policyID := uuid.New()
	v1 := domain.PolicyVersion{
		PolicyID: policyID, Version: 1, Name: "flagged-risk", Enabled: true,
		ValidFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Rules: []domain.PolicyRule{
			{RuleID: "r1", Condition: "always", Action: domain.PolicyDecisionStepUp, Enabled: true},
		},
	}
	userID := uuid.New()
	// Early 2024: customer clean. Late 2024: flagged.
	snaps := []domain.CustomerStateSnapshot{
		{SnapshotID: uuid.New(), UserID: userID, CapturedAt: time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC), KYCTier: 2},
		{SnapshotID: uuid.New(), UserID: userID, CapturedAt: time.Date(2024, 11, 1, 0, 0, 0, 0, time.UTC), KYCTier: 2, FlaggedRisk: true},
	}
	svc, _ := newTT(t, []*domain.Policy{{PolicyID: policyID, Status: domain.PolicyStatusActive}},
		map[uuid.UUID][]domain.PolicyVersion{policyID: {v1}}, snaps)

	early, _ := svc.EvaluateAtTime(context.Background(), "p", time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		domain.PaymentContext{UserID: userID})
	late, _ := svc.EvaluateAtTime(context.Background(), "p", time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),
		domain.PaymentContext{UserID: userID})

	if early.Snapshot == nil || late.Snapshot == nil {
		t.Fatalf("both replays must bind snapshots (early=%v late=%v)", early.Snapshot, late.Snapshot)
	}
	if early.Snapshot.SnapshotID == late.Snapshot.SnapshotID {
		t.Fatal("replays at different times must bind different snapshots")
	}
	if !late.Snapshot.FlaggedRisk {
		t.Fatal("December replay must see the flagged-risk snapshot")
	}
}

func TestTimeTravelNonDeterministicWhenNoSnapshot(t *testing.T) {
	policyID := uuid.New()
	v1 := domain.PolicyVersion{
		PolicyID: policyID, Version: 1, Name: "p", Enabled: true,
		ValidFrom: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Rules:     []domain.PolicyRule{{RuleID: "r", Condition: "always", Action: domain.PolicyDecisionAllow, Enabled: true}},
	}
	svc, _ := newTT(t, []*domain.Policy{{PolicyID: policyID, Status: domain.PolicyStatusActive}},
		map[uuid.UUID][]domain.PolicyVersion{policyID: {v1}}, nil)

	ev, err := svc.EvaluateAtTime(context.Background(), "p", time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		domain.PaymentContext{UserID: uuid.New()})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if ev.Deterministic {
		t.Fatal("replay without a state snapshot must be labelled non-deterministic")
	}
}

func TestRecordAndFetchHistoricalDecision(t *testing.T) {
	svc, fh := newTT(t, nil, nil, nil)
	rec, err := svc.RecordDecision(context.Background(), domain.RecordHistoricalDecisionRequest{
		PaymentID:     "pay-42",
		FinalDecision: domain.PolicyDecisionAllow,
		PolicyVersions: []uuid.UUID{uuid.New()},
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := svc.GetHistoricalDecision(context.Background(), "pay-42")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.RecordID != rec.RecordID {
		t.Fatal("fetched record must match the stored one")
	}
	if len(fh.records) != 1 {
		t.Fatalf("expected 1 stored record, got %d", len(fh.records))
	}
}
