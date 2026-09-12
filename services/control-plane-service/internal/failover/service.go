// Package failover wires shared/failover into control-plane-service: region
// registration, health ingestion, routing decisions, epoch fencing, the
// cross-region deduped op log and the post-recovery reconciliation report.
package failover

import (
	"errors"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/failover"
)

// Service is the control-plane's in-memory DR controller.
type Service struct {
	ctrl   *shared.Controller
	logger zerolog.Logger
}

// NewService builds the service with the given RPO/RTO policy.
func NewService(cfg shared.Config, logger zerolog.Logger) *Service {
	return &Service{ctrl: shared.NewController(cfg), logger: logger}
}

// RegisterRegion adds a region.
func (s *Service) RegisterRegion(name string, role shared.Role) error {
	if err := s.ctrl.RegisterRegion(name, role); err != nil {
		return err
	}
	s.logger.Info().Str("region", name).Str("role", string(role)).Msg("failover region registered")
	return nil
}

// ReportHealth records health + lag.
func (s *Service) ReportHealth(region string, ok bool, lagMs int64) error {
	if err := s.ctrl.ReportHealth(region, ok, lagMs); err != nil {
		return err
	}
	s.logger.Info().Str("region", region).Bool("healthy", ok).Int64("lag_ms", lagMs).Msg("failover health reported")
	return nil
}

// Route computes the routing decision (and logs failover/degraded moves).
func (s *Service) Route() shared.RouteDecision {
	d := s.ctrl.Route()
	if d.Route != shared.RoutePrimary {
		s.logger.Info().Str("route", string(d.Route)).Str("target", d.Target).Str("reason", d.Reason).Msg("failover routing decision")
	}
	return d
}

// Regions snapshots region health.
func (s *Service) Regions() []shared.RegionHealth {
	return s.ctrl.Regions()
}

// CurrentEpoch returns the fencing epoch.
func (s *Service) CurrentEpoch() uint64 {
	return s.ctrl.CurrentEpoch()
}

// AdvanceEpoch fences old writers.
func (s *Service) AdvanceEpoch() uint64 {
	epoch := s.ctrl.AdvanceEpoch()
	s.logger.Info().Uint64("epoch", epoch).Msg("failover epoch advanced (old writers fenced)")
	return epoch
}

// ExecuteOp records an op exactly once across regions.
func (s *Service) ExecuteOp(region, opID string, epoch uint64) (bool, error) {
	executed, err := s.ctrl.ExecuteOp(region, opID, epoch)
	if err != nil {
		if errors.Is(err, shared.ErrFenced) {
			s.logger.Info().Str("op", opID).Uint64("epoch", epoch).Msg("failover op fenced (stale epoch)")
		}
		return false, err
	}
	if executed {
		s.logger.Info().Str("op", opID).Str("region", region).Msg("failover op executed")
	} else {
		s.logger.Info().Str("op", opID).Str("region", region).Msg("failover duplicate op deduped")
	}
	return executed, nil
}

// RecoveryDiff builds the reconciliation report.
func (s *Service) RecoveryDiff(primary, standby string) (*shared.ReconciliationReport, error) {
	return s.ctrl.RecoveryDiff(primary, standby)
}
