// Package salaryplus extends insights-service with salary raise-routing:
//
//   - raise plans allocate {percent_of_raise → pot_id} slices executed when
//     a pay increase is detected (lateness/increase alerts already exist in
//     the intelligence service — this package adds plan CRUD + evaluation
//     producing scheduled transfer intents).
package salaryplus

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

var (
	// ErrNotFound surfaces unknown raise plans.
	ErrNotFound = errors.New("not found")
	// ErrInvalid surfaces bad allocations or evaluation input.
	ErrInvalid = errors.New("invalid request")
)

// RaiseAllocation routes percent_of_raise of a detected increase to one pot.
type RaiseAllocation struct {
	PotID   string  `json:"pot_id"`
	Percent float64 `json:"percent_of_raise"`
}

// RaisePlan is one stored raise-routing plan for an account.
type RaisePlan struct {
	ID          string            `json:"id"`
	AccountID   string            `json:"account_id"`
	Allocations []RaiseAllocation `json:"allocations"`
	CreatedAt   time.Time         `json:"created_at"`
}

// TransferIntent is one scheduled pot transfer produced by evaluation.
type TransferIntent struct {
	PotID       string  `json:"pot_id"`
	AmountMinor int64   `json:"amount_minor"`
	Percent     float64 `json:"percent_of_raise"`
}

// RaiseEvaluation is the outcome of evaluating a plan against a detected
// increase (last salary vs blended average).
type RaiseEvaluation struct {
	PlanID      string           `json:"plan_id"`
	AccountID   string           `json:"account_id"`
	LastAmount  int64            `json:"last_amount"`
	MonthlyAvg  int64            `json:"monthly_avg"`
	RaiseMinor  int64            `json:"raise_minor"`
	Intents     []TransferIntent `json:"intents"`
	Note        string           `json:"note"`
	EvaluatedAt time.Time        `json:"evaluated_at"`
}

// Service stores raise plans and evaluates them.
type Service struct {
	mu     sync.RWMutex
	plans  map[string]*RaisePlan
	logger zerolog.Logger
}

// NewService builds an empty raise-plan service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{plans: map[string]*RaisePlan{}, logger: logger}
}

// CreatePlan stores one raise-routing plan. Allocation percents must each be
// positive and sum to at most 100.
func (s *Service) CreatePlan(accountID string, allocations []RaiseAllocation) (*RaisePlan, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("%w: account_id is required", ErrInvalid)
	}
	if len(allocations) == 0 {
		return nil, fmt.Errorf("%w: at least one allocation is required", ErrInvalid)
	}
	total := 0.0
	for i, a := range allocations {
		if strings.TrimSpace(a.PotID) == "" {
			return nil, fmt.Errorf("%w: allocations[%d].pot_id is required", ErrInvalid, i)
		}
		if a.Percent <= 0 || a.Percent > 100 {
			return nil, fmt.Errorf("%w: allocations[%d].percent_of_raise must be within (0, 100]", ErrInvalid, i)
		}
		total += a.Percent
	}
	if total > 100.0+1e-9 {
		return nil, fmt.Errorf("%w: allocations sum to %.2f%%, must not exceed 100%%", ErrInvalid, total)
	}
	plan := &RaisePlan{
		ID:          uuid.NewString(),
		AccountID:   accountID,
		Allocations: append([]RaiseAllocation(nil), allocations...),
		CreatedAt:   time.Now().UTC(),
	}
	s.mu.Lock()
	s.plans[plan.ID] = plan
	s.mu.Unlock()
	s.logger.Info().Str("plan_id", plan.ID).Str("account_id", accountID).Msg("raise plan created")
	out := *plan
	out.Allocations = append([]RaiseAllocation(nil), plan.Allocations...)
	return &out, nil
}

// ListPlans returns plans, optionally filtered by account.
func (s *Service) ListPlans(accountID string) []RaisePlan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []RaisePlan
	for _, p := range s.plans {
		if accountID != "" && p.AccountID != accountID {
			continue
		}
		cp := *p
		cp.Allocations = append([]RaiseAllocation(nil), p.Allocations...)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if out == nil {
		out = []RaisePlan{}
	}
	return out
}

// DeletePlan removes one plan.
func (s *Service) DeletePlan(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[strings.TrimSpace(id)]; !ok {
		return fmt.Errorf("raise plan %q: %w", id, ErrNotFound)
	}
	delete(s.plans, strings.TrimSpace(id))
	s.logger.Info().Str("plan_id", id).Msg("raise plan deleted")
	return nil
}

// EvaluatePlan computes the raise (last − average) and fans it out into
// scheduled transfer intents. A non-positive raise yields zero intents with
// an explanatory note rather than an error.
func (s *Service) EvaluatePlan(id string, lastAmount, monthlyAvg int64) (*RaiseEvaluation, error) {
	if lastAmount < 0 || monthlyAvg < 0 {
		return nil, fmt.Errorf("%w: last_amount and monthly_avg must be >= 0", ErrInvalid)
	}
	s.mu.RLock()
	plan, ok := s.plans[strings.TrimSpace(id)]
	var cp RaisePlan
	if ok {
		cp = *plan
		cp.Allocations = append([]RaiseAllocation(nil), plan.Allocations...)
	}
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("raise plan %q: %w", id, ErrNotFound)
	}
	raise := lastAmount - monthlyAvg
	ev := &RaiseEvaluation{
		PlanID:      cp.ID,
		AccountID:   cp.AccountID,
		LastAmount:  lastAmount,
		MonthlyAvg:  monthlyAvg,
		RaiseMinor:  raise,
		EvaluatedAt: time.Now().UTC(),
	}
	if raise <= 0 {
		ev.Intents = []TransferIntent{}
		ev.Note = "no increase detected — no transfers scheduled"
		s.logger.Info().Str("plan_id", id).Int64("raise", raise).Msg("raise plan evaluated with no increase")
		return ev, nil
	}
	for _, a := range cp.Allocations {
		amount := int64(float64(raise)*a.Percent/100.0 + 0.5)
		ev.Intents = append(ev.Intents, TransferIntent{PotID: a.PotID, AmountMinor: amount, Percent: a.Percent})
	}
	ev.Note = fmt.Sprintf("routing %d of raise across %d pots", raise, len(ev.Intents))
	s.logger.Info().Str("plan_id", id).Int64("raise", raise).Int("intents", len(ev.Intents)).Msg("raise plan evaluated")
	return ev, nil
}
