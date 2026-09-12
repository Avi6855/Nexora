package txpolicy

import (
	"sync"

	sharedtx "github.com/nexora/nexora/shared/txpolicy"
)

// Service is the payment-service's in-memory transaction-policy store backed
// by shared/txpolicy. The mutex guards the mutex-free shared engine.
type Service struct {
	mu     sync.RWMutex
	engine *sharedtx.Engine
}

// NewService creates an empty transaction-policy service.
func NewService() *Service {
	return &Service{engine: sharedtx.NewEngine()}
}

// PutRule stores a rule, bumping its version.
func (s *Service) PutRule(rule sharedtx.Rule) (sharedtx.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.PutRule(rule)
}

// Rollback drops the latest version of a rule, restoring the prior one.
func (s *Service) Rollback(id string) (sharedtx.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Rollback(id)
}

// Evaluate matches a context against the current policy.
func (s *Service) Evaluate(ctx sharedtx.Context) sharedtx.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Evaluate(ctx)
}

// Audit returns the evaluation log, oldest first.
func (s *Service) Audit() []sharedtx.AuditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.engine.Audit()
}

// Simulate runs a candidate rule set alongside the current policy over a
// batch of historical contexts.
func (s *Service) Simulate(candidate []sharedtx.Rule, contexts []sharedtx.Context) (sharedtx.SimulationResult, error) {
	cand := sharedtx.NewEngine()
	for _, r := range candidate {
		if _, err := cand.PutRule(r); err != nil {
			return sharedtx.SimulationResult{}, err
		}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return sharedtx.Simulate(s.engine, cand, contexts), nil
}
