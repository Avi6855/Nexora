// Package mldata wires shared/mldata into control-plane-service: the model
// impact simulator, freshness gateway, drift monitor, dependency registry
// and rollback compatibility checks.
package mldata

import (
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/mldata"
)

// Service is the control-plane's ML/data safety controller.
type Service struct {
	graph    *shared.ImpactGraph
	fresh    *shared.FreshnessGateway
	drift    *shared.DriftMonitor
	registry *shared.DependencyRegistry
	logger   zerolog.Logger
}

// NewService builds the service with a 20% drift threshold.
func NewService(logger zerolog.Logger) *Service {
	return &Service{
		graph:    shared.NewImpactGraph(),
		fresh:    shared.NewFreshnessGateway(),
		drift:    shared.NewDriftMonitor(20),
		registry: shared.NewDependencyRegistry(),
		logger:   logger,
	}
}

// AddEdge records a model-graph dependency.
func (s *Service) AddEdge(from, to, kind string) error {
	if err := s.graph.AddEdge(from, to, kind); err != nil {
		return err
	}
	s.logger.Info().Str("from", from).Str("to", to).Msg("mldata graph edge recorded")
	return nil
}

// SimulateImpact returns the blast radius of a proposed change.
func (s *Service) SimulateImpact(node string) (shared.Impact, error) {
	imp, err := s.graph.Simulate(node)
	if err != nil {
		return shared.Impact{}, err
	}
	s.logger.Info().Str("node", node).Int("blast_radius", imp.Count).Msg("mldata impact simulated")
	return imp, nil
}

// SetSLA records a feature freshness ceiling.
func (s *Service) SetSLA(feature string, maxAge time.Duration) error {
	return s.fresh.SetSLA(feature, maxAge)
}

// CheckFreshness grades one feature's age.
func (s *Service) CheckFreshness(feature string, age time.Duration) (shared.FreshnessResult, error) {
	res, err := s.fresh.Check(feature, age)
	if err != nil {
		return shared.FreshnessResult{}, err
	}
	if res.Verdict == shared.FreshBlock {
		s.logger.Info().Str("feature", feature).Str("verdict", string(res.Verdict)).Msg("mldata freshness blocked stale feature")
	}
	return res, nil
}

// SetBaseline records a drift baseline.
func (s *Service) SetBaseline(key string, d shared.Distribution) error {
	return s.drift.SetBaseline(key, d)
}

// CheckDrift compares current against the baseline, logging alerts.
func (s *Service) CheckDrift(key string, cur shared.Distribution) (shared.DriftResult, error) {
	res, err := s.drift.Check(key, cur)
	if err != nil {
		return shared.DriftResult{}, err
	}
	if res.Alert {
		s.logger.Info().Str("key", key).Float64("drift_pct", res.DriftPct).Msg("mldata drift alert")
	}
	return res, nil
}

// RegisterModel adds a model and its dependencies.
func (s *Service) RegisterModel(m shared.ModelDeps) error {
	if err := s.registry.Register(m); err != nil {
		return err
	}
	s.logger.Info().Str("model", m.ID).Msg("mldata model registered")
	return nil
}

// VerifyDeps runs the pre-deploy health check.
func (s *Service) VerifyDeps(id string, datasetHealth map[string]bool) (shared.VerifyResult, error) {
	res, err := s.registry.VerifyDeps(id, datasetHealth)
	if err != nil {
		return shared.VerifyResult{}, err
	}
	if !res.Healthy {
		s.logger.Info().Str("model", id).Strs("missing", res.Missing).Msg("mldata pre-deploy verify unhealthy")
	}
	return res, nil
}

// CheckRollback verifies the rollback schema against the pipeline.
func (s *Service) CheckRollback(expected, current map[string]string) shared.RollbackCheck {
	chk := shared.VerifyRollbackCompat(expected, current)
	if chk.Verdict == shared.RollbackUnsafe {
		s.logger.Info().Int("mismatches", len(chk.Mismatches)).Msg("mldata rollback unsafe")
	}
	return chk
}
