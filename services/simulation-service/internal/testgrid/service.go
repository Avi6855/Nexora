package testgrid

import (
	"time"

	"github.com/rs/zerolog"

	sharedgrid "github.com/nexora/nexora/shared/testgrid"
)

// Service is the simulation-service's in-memory test-grid store backed by
// shared/testgrid.
type Service struct {
	gen    *sharedgrid.Generator
	policy *sharedgrid.PolicyEngine
	grids  *sharedgrid.GridStore
	logger zerolog.Logger
}

// NewService creates a test-grid service with the default chaos policy.
func NewService(logger zerolog.Logger) *Service {
	return &Service{
		gen:    sharedgrid.NewGenerator(logger),
		policy: sharedgrid.NewPolicyEngine(sharedgrid.DefaultChaosPolicy(), logger),
		grids:  sharedgrid.NewGridStore(logger),
		logger: logger,
	}
}

// Generator exposes the scenario generator.
func (s *Service) Generator() *sharedgrid.Generator { return s.gen }

// Grids exposes the journey store.
func (s *Service) Grids() *sharedgrid.GridStore { return s.grids }

// Generate creates synthetic regression cases.
func (s *Service) Generate(kind sharedgrid.IncidentKind, seed int64, count int) ([]*sharedgrid.RegressionCase, error) {
	cases, err := s.gen.Generate(kind, seed, count, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("kind", string(kind)).Int("count", len(cases)).Msg("test scenarios generated")
	return cases, nil
}

// ListScenarios lists generated cases.
func (s *Service) ListScenarios(filter sharedgrid.IncidentKind) []*sharedgrid.RegressionCase {
	return s.gen.List(filter)
}

// EvaluateChaos checks an experiment against policy.
func (s *Service) EvaluateChaos(req sharedgrid.ExperimentRequest) (*sharedgrid.GuardReport, error) {
	rep, err := s.policy.Evaluate(req, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("target", req.Target).Str("verdict", string(rep.Verdict)).Msg("chaos experiment evaluated")
	return rep, nil
}

// CreateJourney stores a journey definition.
func (s *Service) CreateJourney(steps, faults []string, expectations map[string]string) (*sharedgrid.Journey, error) {
	j, err := s.grids.CreateJourney(steps, faults, expectations, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("journey_id", j.ID).Msg("journey created")
	return j, nil
}

// RunGrid executes the journey×fault matrix.
func (s *Service) RunGrid(id string) (*sharedgrid.GridResult, error) {
	res, err := s.grids.RunGrid(id, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("journey_id", id).Int("passed", res.Passed).Msg("journey grid run")
	return res, nil
}

// GridResults returns the latest run.
func (s *Service) GridResults(id string) (*sharedgrid.GridResult, error) {
	return s.grids.Results(id)
}
