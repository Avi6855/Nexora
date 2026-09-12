package testgrid

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Verdict is the chaos policy decision.
type Verdict string

const (
	VerdictApprove Verdict = "APPROVE"
	VerdictDeny    Verdict = "DENY"
)

// ExperimentRequest is one chaos experiment proposal.
type ExperimentRequest struct {
	ID              string    `json:"id,omitempty"`
	Target          string    `json:"target"`
	Fault           string    `json:"fault"`
	BlastRadiusPct  int       `json:"blast_radius_pct"`
	Env             string    `json:"env"`
	HasRollbackPlan bool      `json:"has_rollback_plan"`
	At              time.Time `json:"at"`
}

// ChaosPolicy gates experiments.
type ChaosPolicy struct {
	ApprovedEnvs      []string `json:"approved_envs"`
	RadiusCapPct      int      `json:"radius_cap_pct"`
	RollbackRequired  bool     `json:"rollback_required"`
	BusinessStartHour int      `json:"business_start_hour"` // inclusive, UTC
	BusinessEndHour   int      `json:"business_end_hour"`   // exclusive, UTC
}

// DefaultChaosPolicy is the safe baseline: staging/simulation only,
// small blast radius, rollback plan required, business hours.
func DefaultChaosPolicy() ChaosPolicy {
	return ChaosPolicy{
		ApprovedEnvs:      []string{"staging", "simulation"},
		RadiusCapPct:      10,
		RollbackRequired:  true,
		BusinessStartHour: 9,
		BusinessEndHour:   17,
	}
}

// GuardReport is the policy evaluation outcome.
type GuardReport struct {
	Verdict   Verdict   `json:"verdict"`
	Reasons   []string  `json:"reasons"`
	CheckedAt time.Time `json:"checked_at"`
}

// PolicyEngine evaluates experiments against a policy.
type PolicyEngine struct {
	policy ChaosPolicy
	logger zerolog.Logger
}

// NewPolicyEngine returns an engine with the given policy.
func NewPolicyEngine(policy ChaosPolicy, logger zerolog.Logger) *PolicyEngine {
	if policy.RadiusCapPct == 0 {
		policy.RadiusCapPct = 10
	}
	return &PolicyEngine{policy: policy, logger: logger}
}

// Policy returns a copy of the active policy.
func (e *PolicyEngine) Policy() ChaosPolicy {
	cp := e.policy
	cp.ApprovedEnvs = append([]string(nil), e.policy.ApprovedEnvs...)
	return cp
}

// Evaluate checks approved_env_only, radius_cap, rollback_plan_required
// and business_hours. All violations are reported; any violation DENYs.
func (e *PolicyEngine) Evaluate(req ExperimentRequest, now time.Time) (*GuardReport, error) {
	if strings.TrimSpace(req.Target) == "" {
		return nil, fmt.Errorf("%w: target is required", ErrGridInvalidInput)
	}
	if strings.TrimSpace(req.Fault) == "" {
		return nil, fmt.Errorf("%w: fault is required", ErrGridInvalidInput)
	}
	if req.BlastRadiusPct < 0 || req.BlastRadiusPct > 100 {
		return nil, fmt.Errorf("%w: blast_radius_pct must be 0..100", ErrGridInvalidInput)
	}
	if strings.TrimSpace(req.Env) == "" {
		return nil, fmt.Errorf("%w: env is required", ErrGridInvalidInput)
	}
	at := req.At
	if at.IsZero() {
		at = now
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var reasons []string
	envOK := false
	for _, env := range e.policy.ApprovedEnvs {
		if strings.EqualFold(strings.TrimSpace(env), strings.TrimSpace(req.Env)) {
			envOK = true
			break
		}
	}
	if !envOK {
		reasons = append(reasons, fmt.Sprintf("env %q not in approved list %v (approved_env_only)", req.Env, e.policy.ApprovedEnvs))
	}
	if req.BlastRadiusPct > e.policy.RadiusCapPct {
		reasons = append(reasons, fmt.Sprintf("blast radius %d%% exceeds cap %d%%", req.BlastRadiusPct, e.policy.RadiusCapPct))
	}
	if e.policy.RollbackRequired && !req.HasRollbackPlan {
		reasons = append(reasons, "rollback plan required but missing")
	}
	h := at.UTC().Hour()
	if h < e.policy.BusinessStartHour || h >= e.policy.BusinessEndHour {
		reasons = append(reasons, fmt.Sprintf("outside business hours %02d:00-%02d:00 UTC (at %02d:00)", e.policy.BusinessStartHour, e.policy.BusinessEndHour, h))
	}
	rep := &GuardReport{CheckedAt: time.Now().UTC()}
	if len(reasons) == 0 {
		rep.Verdict = VerdictApprove
		rep.Reasons = []string{"all guards passed"}
	} else {
		rep.Verdict = VerdictDeny
		rep.Reasons = reasons
	}
	e.logger.Info().Str("target", req.Target).Str("fault", req.Fault).Str("verdict", string(rep.Verdict)).Int("guards", len(reasons)).Msg("chaos experiment evaluated")
	return rep, nil
}
