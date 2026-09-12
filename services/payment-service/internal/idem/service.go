// Package idem wires shared/idempotency's gateway into payment-service: an
// idempotent API platform where POST /execute dedupes by key, replays stored
// responses, and re-executes after TTL expiry.
package idem

import (
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/idempotency"
)

// Service is the payment-service idempotency gateway.
type Service struct {
	gw     *shared.Gateway
	logger zerolog.Logger
}

// NewService builds the service with the given TTL.
func NewService(ttl time.Duration, logger zerolog.Logger) *Service {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Service{gw: shared.NewGateway(ttl), logger: logger}
}

// Execute runs fn exactly once per key, replaying or retrying as needed.
// Returns the response, whether it was replayed, and any error.
func (s *Service) Execute(key, method, path string, body []byte, fn func() ([]byte, error)) ([]byte, bool, error) {
	resp, replayed, err := s.gw.Execute(key, method, path, body, fn)
	if err != nil {
		return nil, false, err
	}
	if replayed {
		s.logger.Info().Str("key", key).Msg("idem response replayed")
	} else {
		s.logger.Info().Str("key", key).Str("method", method).Str("path", path).Msg("idem request executed")
	}
	return resp, replayed, nil
}

// Get returns the stored request.
func (s *Service) Get(key string) (*shared.GatewayRequest, error) {
	return s.gw.Get(key)
}

// Sweep expires TTL-old slots and returns how many transitioned.
func (s *Service) Sweep() int {
	n := s.gw.Sweep()
	if n > 0 {
		s.logger.Info().Int("expired", n).Msg("idem TTL sweep expired requests")
	}
	return n
}
