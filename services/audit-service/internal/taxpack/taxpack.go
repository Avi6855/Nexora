// Package taxpack wires shared/taxpack into audit-service: tax-year entry
// posting, pack building, finalization and JSON/CSV/manifest exports.
package taxpack

import (
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/taxpack"
)

var (
	// ErrNotFound surfaces unknown packs.
	ErrNotFound = errors.New("not found")
	// ErrInvalid surfaces bad categories, amounts or dates.
	ErrInvalid = errors.New("invalid request")
	// ErrFinalized surfaces mutations of finalized packs.
	ErrFinalized = errors.New("conflict")
)

// Service is the audit-service view over the shared tax-pack store.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds a service over a fresh shared store.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(), logger: logger}
}

// PostEntry records one tax-relevant line.
func (s *Service) PostEntry(category string, amount int64, date time.Time, docID string) (*shared.Entry, error) {
	e, err := s.store.PostEntry(category, amount, date, docID)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("category", e.Category).Int64("amount", e.Amount).Msg("tax entry posted")
	return e, nil
}

// BuildPack aggregates one UK tax year into a DRAFT pack.
func (s *Service) BuildPack(year int) (*shared.Pack, error) {
	p, err := s.store.BuildPack(year)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Int("year", year).Int("count", p.Count).Msg("tax pack built")
	return p, nil
}

// Finalize locks one pack as FINALIZED.
func (s *Service) Finalize(year int) (*shared.Pack, error) {
	p, err := s.store.Finalize(year)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Int("year", year).Msg("tax pack finalized")
	return p, nil
}

// GetPack returns one pack.
func (s *Service) GetPack(year int) (*shared.Pack, error) {
	p, err := s.store.GetPack(year)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return p, nil
}

// RenderJSON renders one pack plus entries as JSON.
func (s *Service) RenderJSON(year int) ([]byte, error) {
	raw, err := s.store.RenderJSON(year)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return raw, nil
}

// RenderCSV renders one pack's totals as CSV.
func (s *Service) RenderCSV(year int) (string, error) {
	out, err := s.store.RenderCSV(year)
	if err != nil {
		return "", mapSharedErr(err)
	}
	return out, nil
}

// Manifest lists the accountant-package contents.
func (s *Service) Manifest(year int) (*shared.Manifest, error) {
	m, err := s.store.Manifest(year)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return m, nil
}

// ParseDate parses an RFC3339 or YYYY-MM-DD date.
func ParseDate(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w: date is required", ErrInvalid)
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%w: date must be RFC3339 or YYYY-MM-DD", ErrInvalid)
}

func mapSharedErr(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case errors.Is(err, shared.ErrFinalized):
		return fmt.Errorf("%w: %s", ErrFinalized, err.Error())
	case errors.Is(err, shared.ErrInvalid):
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	default:
		return err
	}
}
