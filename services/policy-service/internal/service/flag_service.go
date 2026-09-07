package service

import (
	"context"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/policy-service/internal/domain"
	"github.com/nexora/nexora/services/policy-service/internal/repository"
)

// FlagService runs the progressive-rollout platform: deterministic cohort
// assignment, staged percentage rollout, cohort constraints, and automatic
// rollback when the flag's error rate or latency breaches its guardrails.
type FlagService struct {
	repo   repository.FlagRepository
	logger zerolog.Logger
	nowFunc func() time.Time
}

// NewFlagService builds the service.
func NewFlagService(repo repository.FlagRepository, logger zerolog.Logger) *FlagService {
	return &FlagService{repo: repo, logger: logger, nowFunc: time.Now}
}

// CreateFlag registers a flag at its initial rollout percentage.
func (s *FlagService) CreateFlag(ctx context.Context, req *domain.CreateFlagRequest) (*domain.FeatureFlag, error) {
	if strings.TrimSpace(req.Key) == "" || req.RolloutPct < 0 || req.RolloutPct > 100 {
		return nil, domain.ErrInvalidFlag
	}
	// Refuse duplicate keys.
	if _, err := s.repo.GetFlagByKey(ctx, req.Key); err == nil {
		return nil, fmt.Errorf("flag key %q already exists", req.Key)
	}
	now := s.nowFunc().UTC()
	f := &domain.FeatureFlag{
		FlagID:           uuid.New(),
		Key:              req.Key,
		Description:      req.Description,
		Enabled:          true,
		RolloutPct:       req.RolloutPct,
		CohortConstraint: req.CohortConstraint,
		// Guardrails default to sane banking values if unset.
		MaxErrorPct:  orDefault(req.MaxErrorPct, 5.0),
		MaxLatencyMs: orDefault(req.MaxLatencyMs, 2000),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.UpsertFlag(ctx, f); err != nil {
		return nil, fmt.Errorf("storing flag: %w", err)
	}
	s.logger.Info().Str("key", f.Key).Int("rollout_pct", f.RolloutPct).Msg("feature flag created")
	return f, nil
}

// AdvanceRollout moves a flag to a new percentage (10 → 25 → 50 → 100 ladder
// is caller-managed; any value is accepted but logged).
func (s *FlagService) AdvanceRollout(ctx context.Context, flagID uuid.UUID, pct int) (*domain.FeatureFlag, error) {
	f, err := s.repo.GetFlag(ctx, flagID)
	if err != nil {
		return nil, err
	}
	if pct < 0 || pct > 100 {
		return nil, fmt.Errorf("rollout_pct must be 0-100")
	}
	f.RolloutPct = pct
	f.UpdatedAt = s.nowFunc().UTC()
	if pct == 0 {
		f.Enabled = false
	}
	if err := s.repo.UpsertFlag(ctx, f); err != nil {
		return nil, err
	}
	s.logger.Info().Str("key", f.Key).Int("rollout_pct", pct).Msg("feature flag rollout advanced")
	return f, nil
}

// Evaluate answers "is this user in the flag's cohort?" deterministically:
// hash(flag_key, user_id) % 100 < rollout_pct, and the cohort constraint
// (e.g. app_version>=2.1, country=UK) must hold.
func (s *FlagService) Evaluate(ctx context.Context, key, userID string, attributes map[string]string) (*domain.EvaluateFlagResponse, error) {
	f, err := s.repo.GetFlagByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	resp := &domain.EvaluateFlagResponse{
		Key:        f.Key,
		RolloutPct: f.RolloutPct,
		Reason:     "ok",
	}
	if f.RolledBack {
		resp.Reason = "auto-rolled back: " + f.RollbackReason
		return resp, nil
	}
	if !f.Enabled || f.RolloutPct <= 0 {
		resp.Reason = "flag disabled"
		return resp, nil
	}
	if !cohortMatches(f.CohortConstraint, attributes) {
		resp.Reason = "cohort constraint not met"
		return resp, nil
	}
	if cohortBucket(f.Key, userID) >= f.RolloutPct {
		resp.Reason = "user outside rollout cohort"
		return resp, nil
	}
	resp.Enabled = true
	return resp, nil
}

// RecordMetrics ingests the flag's live error rate / latency and AUTO-ROLLS
// BACK to 0% when a guardrail is breached — the platform's safety net.
func (s *FlagService) RecordMetrics(ctx context.Context, flagID uuid.UUID, req *domain.RecordFlagMetricRequest) (*domain.FeatureFlag, error) {
	f, err := s.repo.GetFlag(ctx, flagID)
	if err != nil {
		return nil, err
	}
	now := s.nowFunc().UTC()
	f.ErrorPct = req.ErrorPct
	f.LatencyMs = req.LatencyMs
	f.UpdatedAt = now

	var rollbackReason string
	switch {
	case f.MaxErrorPct > 0 && req.ErrorPct > f.MaxErrorPct:
		rollbackReason = fmt.Sprintf("error rate %.1f%% exceeded guardrail %.1f%%", req.ErrorPct, f.MaxErrorPct)
	case f.MaxLatencyMs > 0 && req.LatencyMs > f.MaxLatencyMs:
		rollbackReason = fmt.Sprintf("latency %.0fms exceeded guardrail %.0fms", req.LatencyMs, f.MaxLatencyMs)
	}

	if rollbackReason != "" && !f.RolledBack {
		s.logger.Warn().
			Str("key", f.Key).
			Str("reason", rollbackReason).
			Msg("AUTO-ROLLBACK: feature flag rolled back to 0%")
		f.RolledBack = true
		f.RollbackReason = rollbackReason
		f.RolloutPct = 0
		f.Enabled = false
	}
	if err := s.repo.UpsertFlag(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// ListFlags returns all flags.
func (s *FlagService) ListFlags(ctx context.Context, limit int) ([]*domain.FeatureFlag, error) {
	return s.repo.ListFlags(ctx, limit)
}

// ── helpers ─────────────────────────────────────────────────────────────────

// cohortBucket maps (flag, user) to 0..99 deterministically across replicas.
func cohortBucket(flagKey, userID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(flagKey + ":" + userID))
	return int(h.Sum32() % 100)
}

var cohortVersionRe = regexp.MustCompile(`^([a-z_]+)(>=|<=|=|>|<)(.+)$`)

// cohortMatches evaluates simple "field>=value" / "field=value" constraints
// with numeric comparison when both sides parse as numbers.
func cohortMatches(constraint string, attributes map[string]string) bool {
	constraint = strings.TrimSpace(constraint)
	if constraint == "" {
		return true
	}
	m := cohortVersionRe.FindStringSubmatch(constraint)
	if m == nil {
		return true // unparseable constraints fail open (logged upstream)
	}
	field, op, want := m[1], m[2], strings.TrimSpace(m[3])
	got, ok := attributes[field]
	if !ok {
		return false
	}
	if gn, we := strconv.ParseFloat(got, 64); we == nil {
		if wn, e2 := strconv.ParseFloat(want, 64); e2 == nil {
			switch op {
			case ">=":
				return gn >= wn
			case "<=":
				return gn <= wn
			case ">":
				return gn > wn
			case "<":
				return gn < wn
			}
		}
	}
	switch op {
	case "=":
		return got == want
	case ">=":
		return got >= want
	case "<=":
		return got <= want
	case ">":
		return got > want
	case "<":
		return got < want
	}
	return false
}

func orDefault(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
