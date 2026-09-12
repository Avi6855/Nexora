// Package keysec wires shared/keysec into identity-service: versioned key
// lifecycle with an explicit active pointer, usage-anomaly detection,
// staged-secret scanning and security-policy simulation.
package keysec

import (
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/keysec"
)

var (
	// ErrNotFound surfaces unknown keys and services.
	ErrNotFound = errors.New("not found")
	// ErrInvalid surfaces malformed requests.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict surfaces illegal lifecycle transitions.
	ErrConflict = errors.New("conflict")
)

// Service is the identity-service view over the shared keysec store.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds a service over a fresh shared store.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(), logger: logger}
}

// CreateKey creates a CREATED key.
func (s *Service) CreateKey(service, keyType string) (*shared.Key, error) {
	k, err := s.store.CreateKey(service, keyType)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("key", k.ID).Str("service", k.Service).Str("type", k.Type).Int("version", k.Version).Msg("key created")
	return k, nil
}

// ActivateKey moves a key to ACTIVE.
func (s *Service) ActivateKey(id string) (*shared.Key, error) {
	k, err := s.store.ActivateKey(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("key", k.ID).Str("service", k.Service).Msg("key activated")
	return k, nil
}

// RotateKey rotates the active key to a successor version.
func (s *Service) RotateKey(id string) (*shared.Key, error) {
	k, err := s.store.RotateKey(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("key", k.ID).Str("service", k.Service).Int("version", k.Version).Msg("key rotated")
	return k, nil
}

// RevokeKey revokes a key.
func (s *Service) RevokeKey(id string) (*shared.Key, error) {
	k, err := s.store.RevokeKey(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("key", k.ID).Msg("key revoked")
	return k, nil
}

// DestroyKey destroys a revoked key.
func (s *Service) DestroyKey(id string) (*shared.Key, error) {
	k, err := s.store.DestroyKey(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("key", k.ID).Msg("key destroyed")
	return k, nil
}

// ActiveKey resolves the explicit active key for a service.
func (s *Service) ActiveKey(service string) (*shared.Key, error) {
	k, err := s.store.ActiveKey(service)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return k, nil
}

// ObserveUsage records one day's request count with context.
func (s *Service) ObserveUsage(keyID string, day time.Time, count int, service, region, operation string) error {
	if err := s.store.ObserveUsage(keyID, day, count, service, region, operation); err != nil {
		return mapSharedErr(err)
	}
	s.logger.Info().Str("key", keyID).Int("count", count).Msg("key usage observed")
	return nil
}

// Anomalies returns current baseline-breach alerts.
func (s *Service) Anomalies() []shared.Anomaly {
	return s.store.Anomalies()
}

// Scan scans commit text for staged secrets.
func (s *Service) Scan(text string) (string, []shared.Finding) {
	return shared.ScanCommit(text)
}

// Simulate runs a proposed policy over synthetic traffic.
func (s *Service) Simulate(p shared.Policy, traffic []shared.TrafficEvent) shared.SimulationResult {
	return shared.Simulate(p, traffic)
}

func mapSharedErr(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case errors.Is(err, shared.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	case errors.Is(err, shared.ErrInvalid):
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	default:
		return err
	}
}
