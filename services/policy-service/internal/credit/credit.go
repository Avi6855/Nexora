package credit

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	shared "github.com/nexora/nexora/shared/credit"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Service wires shared/credit into policy-service with mutex-guarded state.
type Service struct {
	mu          sync.Mutex
	corrections *shared.CorrectionEngine
	monitor     *shared.FairnessMonitor
	policies    map[string]shared.Policy
	sandboxes   map[string]*shared.SandboxResult
}

// NewService builds the service with default fairness gates.
func NewService() *Service {
	return &Service{
		corrections: shared.CorrectionEngineNew(),
		monitor:     shared.NewFairnessMonitor(0.08, 3, 24*time.Hour),
		policies:    map[string]shared.Policy{},
		sandboxes:   map[string]*shared.SandboxResult{},
	}
}

// SimulateLimit projects a limit change.
func (s *Service) SimulateLimit(profile shared.LimitProfile, newLimitMinor int64) (*shared.LimitProjection, error) {
	return shared.SimulateLimit(profile, newLimitMinor)
}

// RepaymentRecommendation is the optimiser output with its headline.
type RepaymentRecommendation struct {
	Results []shared.StrategyResult `json:"results"`
	Best    shared.StrategyResult   `json:"best"`
	Saved   int64                   `json:"saved_minor"`
	Found   bool                    `json:"found"`
}

// RecommendRepayment ranks payoff strategies for the same budget.
func (s *Service) RecommendRepayment(debts []shared.Debt, budgetMinor int64, maxMonths int) (*RepaymentRecommendation, error) {
	if maxMonths <= 0 {
		maxMonths = 60
	}
	results, err := shared.OptimiseRepayment(debts, budgetMinor, maxMonths)
	if err != nil {
		return nil, err
	}
	best, saved, ok := shared.CompareStrategies(results)
	return &RepaymentRecommendation{Results: results, Best: best, Saved: saved, Found: ok}, nil
}

// OpenCorrection starts a bureau dispute case.
func (s *Service) OpenCorrection(id, customerID, field, detail, reportedBy string, now time.Time) (*shared.CorrectionCase, error) {
	if strings.TrimSpace(customerID) == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	if strings.TrimSpace(field) == "" {
		return nil, fmt.Errorf("field is required")
	}
	if strings.TrimSpace(id) == "" {
		id = uuid.NewString()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrections.Open(id, customerID, field, detail, reportedBy, now), nil
}

// AttachEvidence adds evidence to a case.
func (s *Service) AttachEvidence(id, item string) error {
	if strings.TrimSpace(item) == "" {
		return fmt.Errorf("evidence item is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrections.AttachEvidence(id, item)
}

// SubmitCorrection sends a case to the bureau.
func (s *Service) SubmitCorrection(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrections.Submit(id, now)
}

// TickCorrections auto-escalates overdue awaiting cases.
func (s *Service) TickCorrections(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrections.Tick(now)
}

// ResolveCorrection records the provider outcome.
func (s *Service) ResolveCorrection(id string, updated bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrections.Resolve(id, updated)
}

// RunDecisionSandbox replays a candidate policy and stores the result.
func (s *Service) RunDecisionSandbox(candidate, baseline shared.Policy, population []shared.Applicant) (*shared.SandboxResult, error) {
	if strings.TrimSpace(candidate.Name) == "" {
		return nil, fmt.Errorf("candidate policy name is required")
	}
	res := shared.RunSandbox(candidate, baseline, population)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[candidate.Name] = candidate
	key := candidate.Name
	if _, ok := s.sandboxes[key]; ok {
		key = candidate.Name + ":" + uuid.NewString()
	}
	s.sandboxes[key] = res
	return res, nil
}

// ObserveDecision records one production decision.
func (s *Service) ObserveDecision(o shared.FairnessObs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.monitor.Observe(o)
}

// EvaluateFairness returns breaching segments inside the window.
func (s *Service) EvaluateFairness(now time.Time) []shared.FairnessAlert {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.monitor.Evaluate(now)
}
