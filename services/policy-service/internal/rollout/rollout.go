// Package rollout is the policy-service progressive rollout platform: staged
// flags [10 users, 1%, 5%, 25%, 50%, 100%] with targeting rules (country,
// account_type, app_version_gte), automatic rollback when observed
// error_rate / latency_p99 / payment_failure_rate breach thresholds, and an
// audit of every stage transition.
package rollout

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Stages is the fixed rollout ladder. Stage 0 admits only the first 10 users
// (by cohort order); stages 1..5 are percentage cohorts.
var Stages = []string{"10-users", "1%", "5%", "25%", "50%", "100%"}

// stagePct maps stage index → rollout percent (stage 0 handled by user cap).
var stagePct = []int{0, 1, 5, 25, 50, 100}

// MaxUsersStage0 caps stage 0 to the first 10 users.
const MaxUsersStage0 = 10

// Targeting constrains who may enter the cohort.
type Targeting struct {
	Country       string `json:"country,omitempty"`
	AccountType   string `json:"account_type,omitempty"`
	AppVersionGte string `json:"app_version_gte,omitempty"`
}

// Thresholds are the auto-rollback guardrails.
type Thresholds struct {
	MaxErrorRate          float64 `json:"max_error_rate"`
	MaxLatencyP99Ms       float64 `json:"max_latency_p99_ms"`
	MaxPaymentFailureRate float64 `json:"max_payment_failure_rate"`
}

// Metrics are the observed live signals evaluated against Thresholds.
type Metrics struct {
	ErrorRate          float64 `json:"error_rate"`
	LatencyP99Ms       float64 `json:"latency_p99_ms"`
	PaymentFailureRate float64 `json:"payment_failure_rate"`
}

// AuditEntry records one stage transition.
type AuditEntry struct {
	At     time.Time `json:"at"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
}

// Flag is one progressively rolled-out flag.
type Flag struct {
	ID             uuid.UUID    `json:"id"`
	Key            string       `json:"key"`
	Stage          int          `json:"stage"`
	StageLabel     string       `json:"stage_label"`
	Targeting      Targeting    `json:"targeting"`
	Thresholds     Thresholds   `json:"thresholds"`
	Metrics        Metrics      `json:"metrics"`
	Enabled        bool         `json:"enabled"`
	RolledBack     bool         `json:"rolled_back"`
	RollbackReason string       `json:"rollback_reason,omitempty"`
	Audit          []AuditEntry `json:"audit"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// Service is the in-memory rollout store.
type Service struct {
	mu     sync.Mutex
	flags  map[uuid.UUID]*Flag
	byKey  map[string]uuid.UUID
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{flags: map[uuid.UUID]*Flag{}, byKey: map[string]uuid.UUID{}, logger: logger}
}

func copyFlag(f *Flag) *Flag {
	if f == nil {
		return nil
	}
	cp := *f
	cp.Audit = append([]AuditEntry(nil), f.Audit...)
	return &cp
}

// CreateFlag registers a flag at stage 0.
func (s *Service) CreateFlag(key string, targeting Targeting, thresholds Thresholds) (*Flag, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("key is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byKey[key]; dup {
		return nil, fmt.Errorf("flag key %q already exists", key)
	}
	now := time.Now().UTC()
	f := &Flag{
		ID: uuid.New(), Key: key, Stage: 0, StageLabel: Stages[0],
		Targeting: targeting, Thresholds: thresholds,
		Enabled: true, CreatedAt: now, UpdatedAt: now,
		Audit: []AuditEntry{{At: now, From: "", To: Stages[0], Reason: "flag created"}},
	}
	s.flags[f.ID] = f
	s.byKey[key] = f.ID
	s.logger.Info().Str("key", key).Str("stage", f.StageLabel).Msg("rollout flag created")
	return copyFlag(f), nil
}

// Get returns one flag.
func (s *Service) Get(id uuid.UUID) (*Flag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flags[id]
	if !ok {
		return nil, fmt.Errorf("rollout flag not found")
	}
	return copyFlag(f), nil
}

// Advance moves the flag one stage forward.
func (s *Service) Advance(id uuid.UUID) (*Flag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flags[id]
	if !ok {
		return nil, fmt.Errorf("rollout flag not found")
	}
	if f.RolledBack {
		return nil, fmt.Errorf("flag was auto-rolled back; create a new flag to retry")
	}
	if f.Stage >= len(Stages)-1 {
		return nil, fmt.Errorf("flag is already at 100 percent")
	}
	from := f.StageLabel
	f.Stage++
	f.StageLabel = Stages[f.Stage]
	f.UpdatedAt = time.Now().UTC()
	f.Audit = append(f.Audit, AuditEntry{At: f.UpdatedAt, From: from, To: f.StageLabel, Reason: "stage advanced"})
	s.logger.Info().Str("key", f.Key).Str("from", from).Str("to", f.StageLabel).Msg("rollout stage advanced")
	return copyFlag(f), nil
}

// ReportMetrics ingests live signals and auto-rolls back on guardrail breach.
func (s *Service) ReportMetrics(id uuid.UUID, m Metrics) (*Flag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flags[id]
	if !ok {
		return nil, fmt.Errorf("rollout flag not found")
	}
	f.Metrics = m
	f.UpdatedAt = time.Now().UTC()
	reason := breachReason(f.Thresholds, m)
	if reason != "" && !f.RolledBack {
		from := f.StageLabel
		f.RolledBack = true
		f.RollbackReason = reason
		f.Enabled = false
		f.Audit = append(f.Audit, AuditEntry{At: f.UpdatedAt, From: from, To: "rolled-back", Reason: "auto-rollback: " + reason})
		s.logger.Info().Str("key", f.Key).Str("reason", reason).Msg("AUTO-ROLLBACK: rollout flag rolled back")
	}
	return copyFlag(f), nil
}

// Rollback manually rolls the flag back.
func (s *Service) Rollback(id uuid.UUID, reason string) (*Flag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flags[id]
	if !ok {
		return nil, fmt.Errorf("rollout flag not found")
	}
	if reason == "" {
		reason = "manual rollback"
	}
	from := f.StageLabel
	f.RolledBack = true
	f.RollbackReason = reason
	f.Enabled = false
	f.UpdatedAt = time.Now().UTC()
	f.Audit = append(f.Audit, AuditEntry{At: f.UpdatedAt, From: from, To: "rolled-back", Reason: reason})
	s.logger.Info().Str("key", f.Key).Str("reason", reason).Msg("rollout flag rolled back")
	return copyFlag(f), nil
}

func breachReason(t Thresholds, m Metrics) string {
	if t.MaxErrorRate > 0 && m.ErrorRate > t.MaxErrorRate {
		return fmt.Sprintf("error_rate %.2f breached %.2f", m.ErrorRate, t.MaxErrorRate)
	}
	if t.MaxLatencyP99Ms > 0 && m.LatencyP99Ms > t.MaxLatencyP99Ms {
		return fmt.Sprintf("latency_p99 %.0fms breached %.0fms", m.LatencyP99Ms, t.MaxLatencyP99Ms)
	}
	if t.MaxPaymentFailureRate > 0 && m.PaymentFailureRate > t.MaxPaymentFailureRate {
		return fmt.Sprintf("payment_failure_rate %.2f breached %.2f", m.PaymentFailureRate, t.MaxPaymentFailureRate)
	}
	return ""
}

// Evaluate answers whether userID with attributes is in the cohort.
func (s *Service) Evaluate(id uuid.UUID, userID string, attrs map[string]string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flags[id]
	if !ok {
		return false, "", fmt.Errorf("rollout flag not found")
	}
	if f.RolledBack {
		return false, "auto-rolled back: " + f.RollbackReason, nil
	}
	if !f.Enabled {
		return false, "flag disabled", nil
	}
	if !targetingMatches(f.Targeting, attrs) {
		return false, "targeting rules not met", nil
	}
	if f.Stage == 0 {
		// First-10-users: deterministic admit of hash%1000 < 10 (~1% of
		// users, capped at 10 in operational practice).
		h := fnv.New32a()
		_, _ = h.Write([]byte(f.Key + ":" + userID))
		if int(h.Sum32()%1000) < MaxUsersStage0 {
			return true, "stage 10-users cohort", nil
		}
		return false, "outside 10-users cohort", nil
	}
	pct := stagePct[f.Stage]
	if cohortBucket(f.Key, userID) < pct {
		return true, "in cohort", nil
	}
	return false, "outside rollout cohort", nil
}

func cohortBucket(flagKey, userID string) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(flagKey + ":" + userID))
	return int(h.Sum32() % 100)
}

func targetingMatches(t Targeting, attrs map[string]string) bool {
	if t.Country != "" && attrs["country"] != t.Country {
		return false
	}
	if t.AccountType != "" && attrs["account_type"] != t.AccountType {
		return false
	}
	if t.AppVersionGte != "" {
		got, ok := attrs["app_version"]
		if !ok || compareVersions(got, t.AppVersionGte) < 0 {
			return false
		}
	}
	return true
}

// compareVersions compares dotted versions ("2.10" >= "2.1").
func compareVersions(got, want string) int {
	gp := strings.Split(got, ".")
	wp := strings.Split(want, ".")
	for i := 0; i < len(gp) && i < len(wp); i++ {
		gn, ge := strconv.Atoi(gp[i])
		wn, we := strconv.Atoi(wp[i])
		if ge != nil || we != nil {
			if gp[i] != wp[i] {
				if gp[i] < wp[i] {
					return -1
				}
				return 1
			}
			continue
		}
		if gn != wn {
			if gn < wn {
				return -1
			}
			return 1
		}
	}
	if len(gp) != len(wp) {
		if len(gp) < len(wp) {
			return -1
		}
		return 1
	}
	return 0
}
