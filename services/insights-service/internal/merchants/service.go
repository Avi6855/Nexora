// Package merchants wires the shared merchant identity graph into
// insights-service as a live HTTP surface.
package merchants

import (
	"time"

	"github.com/rs/zerolog"

	sharedm "github.com/nexora/nexora/shared/merchants"
)

// Service is the merchant graph live state: one shared graph.
type Service struct {
	graph  *sharedm.Graph
	logger zerolog.Logger
}

// NewService returns an empty merchant service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{graph: sharedm.NewGraph(), logger: logger}
}

// Observe records an alias observation plus its charge.
func (s *Service) Observe(name, location string, amountMinor int64, refunded bool) (*sharedm.Node, error) {
	n, err := s.graph.Observe(name, location, amountMinor, refunded, time.Now().UTC())
	if err != nil {
		s.logger.Warn().Err(err).Str("name", name).Msg("merchant observe failed")
		return nil, err
	}
	s.logger.Info().Str("merchant_id", n.ID).Str("canonical", n.Canonical).Msg("merchant observed")
	return n, nil
}

// Resolve follows alias → canonical → brand → category → parent.
func (s *Service) Resolve(name string) (*sharedm.Node, error) {
	n, err := s.graph.Resolve(name)
	if err != nil {
		s.logger.Warn().Err(err).Str("name", name).Msg("merchant resolve failed")
		return nil, err
	}
	return n, nil
}

// AddAlias links a name variant to a canonical node.
func (s *Service) AddAlias(merchantID, alias string) (*sharedm.Node, error) {
	n, err := s.graph.AddAlias(merchantID, alias)
	if err != nil {
		s.logger.Warn().Err(err).Str("merchant_id", merchantID).Msg("merchant alias add failed")
		return nil, err
	}
	s.logger.Info().Str("merchant_id", n.ID).Str("alias", alias).Msg("merchant alias added")
	return n, nil
}

// FlagRisk attaches a risk flag.
func (s *Service) FlagRisk(merchantID, flag string) (*sharedm.Node, error) {
	n, err := s.graph.FlagRisk(merchantID, flag)
	if err != nil {
		s.logger.Warn().Err(err).Str("merchant_id", merchantID).Msg("merchant risk flag failed")
		return nil, err
	}
	s.logger.Info().Str("merchant_id", n.ID).Str("flag", flag).Msg("merchant risk flagged")
	return n, nil
}

// Get returns one node.
func (s *Service) Get(merchantID string) (*sharedm.Node, error) {
	n, err := s.graph.Get(merchantID)
	if err != nil {
		s.logger.Warn().Err(err).Str("merchant_id", merchantID).Msg("merchant get failed")
		return nil, err
	}
	return n, nil
}

// RefundStats computes refund-rate stats.
func (s *Service) RefundStats(merchantID string) (sharedm.RefundStats, error) {
	stats, err := s.graph.RefundStats(merchantID)
	if err != nil {
		s.logger.Warn().Err(err).Str("merchant_id", merchantID).Msg("merchant refund stats failed")
		return sharedm.RefundStats{}, err
	}
	return stats, nil
}

// Subscriptions detects recurring merchant charges.
func (s *Service) Subscriptions() []sharedm.Subscription {
	return s.graph.Subscriptions()
}
