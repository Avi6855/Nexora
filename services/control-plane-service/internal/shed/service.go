// Package shed wires shared/shedding's adaptive tier controller into
// control-plane-service: dependency samples drive NORMAL → DEGRADED →
// CRITICAL, and per-class decisions protect critical traffic first.
package shed

import (
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/shedding"
)

// Service is the control-plane's adaptive shedding controller.
type Service struct {
	ctrl   *shared.Controller
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(cfg shared.AdaptiveConfig, logger zerolog.Logger) *Service {
	return &Service{ctrl: shared.NewAdaptiveController(cfg), logger: logger}
}

// Observe ingests one dependency sample.
func (s *Service) Observe(sample shared.Sample) shared.Tier {
	tier := s.ctrl.Observe(sample)
	if tier != shared.TierNormal {
		s.logger.Info().Str("tier", string(tier)).Float64("p99_ms", sample.LatencyP99Ms).
			Float64("error_pct", sample.ErrorRatePct).Float64("rps", sample.RPS).
			Msg("shed tier changed")
	}
	return tier
}

// Decide records the sample and applies the per-class policy.
func (s *Service) Decide(class shared.Class, sample shared.Sample) shared.Decision {
	d := s.ctrl.Decide(class, sample)
	if d.Action != shared.ActionAllow {
		s.logger.Info().Str("class", string(class)).Str("action", string(d.Action)).
			Str("tier", string(d.Tier)).Str("reason", d.Reason).Msg("shed decision")
	}
	return d
}

// Tier returns the current tier.
func (s *Service) Tier() shared.Tier {
	return s.ctrl.Tier()
}

// Config returns the active config.
func (s *Service) Config() shared.AdaptiveConfig {
	return s.ctrl.Config()
}

// UpdatePolicy replaces thresholds/queue caps.
func (s *Service) UpdatePolicy(cfg shared.AdaptiveConfig) shared.AdaptiveConfig {
	s.ctrl.UpdateConfig(cfg)
	updated := s.ctrl.Config()
	s.logger.Info().Interface("policy", updated).Msg("shed policy updated")
	return updated
}
