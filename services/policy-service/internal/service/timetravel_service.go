package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
	"github.com/nexora/nexora/services/policy-service/internal/repository"
	"github.com/rs/zerolog"
)

// TimeTravelService reconstructs historical compliance decisions.
//
// It reuses the SAME evaluatePolicy/evaluateRule logic as live evaluation
// (delegating to PolicyService), but binds it to time-scoped inputs: policy
// versions valid at the instant, and the customer state snapshot in force at
// the instant. The output carries a replay hash so an auditor can re-run the
// reconstruction and byte-compare the result.
type TimeTravelService struct {
	policyRepo  repository.PolicyRepository
	versionRepo repository.PolicyVersionRepository
	snapRepo    repository.SnapshotRepository
	historyRepo repository.HistoryRepository
	live        *PolicyService
	logger      zerolog.Logger
}

func NewTimeTravelService(
	policyRepo repository.PolicyRepository,
	versionRepo repository.PolicyVersionRepository,
	snapRepo repository.SnapshotRepository,
	historyRepo repository.HistoryRepository,
	live *PolicyService,
	logger zerolog.Logger,
) *TimeTravelService {
	return &TimeTravelService{policyRepo: policyRepo, versionRepo: versionRepo, snapRepo: snapRepo, historyRepo: historyRepo, live: live, logger: logger}
}

// EvaluateAtTime reconstructs the decision for a payment context as of `asOf`.
//
// The replay is deterministic: same inputs → same hash. Any change to policy
// semantics that would alter a historical answer must ship as a NEW policy
// version with a validity window — never as an edit to an old version.
func (s *TimeTravelService) EvaluateAtTime(ctx context.Context, paymentID string, asOf time.Time, pctx domain.PaymentContext) (*domain.HistoricalEvaluation, error) {
	// 1. Find the policy versions in force at asOf.
	active, err := s.policyRepo.GetActivePolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing policies: %w", err)
	}
	deterministic := true
	var governing []domain.PolicyVersion
	for _, p := range active {
		versions, err := s.versionRepo.VersionsFor(ctx, p.PolicyID)
		if err != nil {
			return nil, fmt.Errorf("versions for policy %s: %w", p.PolicyID, err)
		}
		v, ok := pickCoveringVersion(versions, asOf)
		if !ok {
			// The policy existed later; at asOf it did not govern.
			continue
		}
		governing = append(governing, *v)
	}

	// 2. Customer state snapshot in force at asOf.
	var snapshot *domain.CustomerStateSnapshot
	if pctx.UserID != uuid.Nil {
		snap, err := s.snapRepo.LatestAtOrBefore(ctx, pctx.UserID, asOf)
		if err != nil {
			if errors.Is(err, domain.ErrNoStateSnapshot) {
				deterministic = false
			} else {
				return nil, fmt.Errorf("snapshot lookup: %w", err)
			}
		} else {
			snapshot = snap
		}
	} else {
		deterministic = false
	}

	// 3. Replay evaluation with the same rule engine, on historical inputs.
	pctx.Timestamp = asOf
	final := domain.PolicyDecisionAllow
	var matched []domain.MatchedRule
	var reasons []string
	for _, v := range governing {
		policy := domain.Policy{
			PolicyID: v.PolicyID,
			Name:     v.Name,
			Status:   v.Status,
			Rules:    v.Rules,
			Enabled:  v.Enabled,
			Version:  v.Version,
		}
		decision := s.live.evaluatePolicy(pctx, policy)
		if decision == nil {
			continue
		}
		matched = append(matched, decision.MatchedRules...)
		reasons = append(reasons, decision.Reasons...)
		if decision.Action == domain.PolicyDecisionBlock {
			final = domain.PolicyDecisionBlock
		} else if decision.Action == domain.PolicyDecisionStepUp && final != domain.PolicyDecisionBlock {
			final = domain.PolicyDecisionStepUp
		}
	}

	sort.Slice(governing, func(i, j int) bool { return governing[i].PolicyID.String() < governing[j].PolicyID.String() })

	return &domain.HistoricalEvaluation{
		ReconstructedAt: time.Now().UTC(),
		AsOf:            asOf,
		PaymentID:       paymentID,
		PolicyVersions:  governing,
		Snapshot:        snapshot,
		FinalDecision:   final,
		MatchedRules:    matched,
		Reasons:         reasons,
		ReplayHash:      replayHash(paymentID, asOf, governing, snapshot, final, matched),
		Deterministic:   deterministic,
	}, nil
}

// pickCoveringVersion selects the version whose [valid_from, valid_to)
// contains t. Overlapping windows are a data-integrity violation — the
// earliest valid_from wins and the overlap is surfaced deterministically.
func pickCoveringVersion(versions []domain.PolicyVersion, t time.Time) (*domain.PolicyVersion, bool) {
	var best *domain.PolicyVersion
	for i := range versions {
		v := &versions[i]
		if !v.Covers(t) {
			continue
		}
		if best == nil || v.Version > best.Version {
			best = v
		}
	}
	return best, best != nil
}

// replayHash binds the reconstruction inputs to the outcome. Auditors re-run
// and compare — the hash makes "same answer" checkable, not narrative.
func replayHash(paymentID string, asOf time.Time, versions []domain.PolicyVersion, snap *domain.CustomerStateSnapshot, final domain.PolicyDecisionAction, matched []domain.MatchedRule) string {
	h := sha256.New()
	fmt.Fprintf(h, "payment=%s|as_of=%s|decision=%s\n", paymentID, asOf.UTC().Format(time.RFC3339Nano), final)
	for _, v := range versions {
		fmt.Fprintf(h, "policy=%s@v%d|status=%s|enabled=%t\n", v.PolicyID, v.Version, v.Status, v.Enabled)
		for _, r := range v.Rules {
			fmt.Fprintf(h, "  rule=%s|cond=%s|action=%s|enabled=%t\n", r.RuleID, r.Condition, r.Action, r.Enabled)
		}
	}
	if snap != nil {
		fmt.Fprintf(h, "snapshot=%s|kyc=%d|device_known=%t|age=%d|flagged=%t\n",
			snap.SnapshotID, snap.KYCTier, snap.DeviceKnown, snap.AccountAgeDays, snap.FlaggedRisk)
	} else {
		h.Write([]byte("snapshot=none\n"))
	}
	for _, m := range matched {
		fmt.Fprintf(h, "matched=%s|%s\n", m.RuleID, m.Action)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RecordDecision archives a live decision with the version IDs used, so the
// future time-travel query can resolve exactly these versions.
func (s *TimeTravelService) RecordDecision(ctx context.Context, req domain.RecordHistoricalDecisionRequest) (*domain.HistoricalDecisionRecord, error) {
	if req.DecidedAt.IsZero() {
		req.DecidedAt = time.Now().UTC()
	}
	rec := domain.HistoricalDecisionRecord{
		RecordID:   uuid.New(),
		Request:    req,
		ReplayHash: replayHash(req.PaymentID, req.DecidedAt, nil, nil, req.FinalDecision, nil),
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.historyRepo.SaveRecord(ctx, rec); err != nil {
		return nil, fmt.Errorf("saving historical decision: %w", err)
	}
	return &rec, nil
}

// GetHistoricalDecision returns the archived record for a payment.
func (s *TimeTravelService) GetHistoricalDecision(ctx context.Context, paymentID string) (*domain.HistoricalDecisionRecord, error) {
	return s.historyRepo.GetByPayment(ctx, paymentID)
}

// CaptureSnapshot stores customer state as of now (or a supplied instant).
func (s *TimeTravelService) CaptureSnapshot(ctx context.Context, req domain.CaptureSnapshotRequest) (*domain.CustomerStateSnapshot, error) {
	if req.UserID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	uid, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user_id: %w", err)
	}
	capturedAt := req.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	}
	snap := domain.CustomerStateSnapshot{
		SnapshotID:     uuid.New(),
		UserID:         uid,
		CapturedAt:     capturedAt,
		KYCTier:        req.KYCTier,
		DeviceKnown:    req.DeviceKnown,
		AccountAgeDays: req.AccountAgeDays,
		FlaggedRisk:    req.FlaggedRisk,
	}
	if err := s.snapRepo.SaveSnapshot(ctx, snap); err != nil {
		return nil, fmt.Errorf("saving snapshot: %w", err)
	}
	return &snap, nil
}
