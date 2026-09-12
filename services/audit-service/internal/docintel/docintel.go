// Package docintel wires shared/docintel into audit-service: document
// ingestion with keyword classification, metadata extraction,
// encrypted-at-rest markers and full-text search.
package docintel

import (
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/docintel"
)

var (
	// ErrNotFound surfaces unknown documents.
	ErrNotFound = errors.New("not found")
	// ErrInvalid surfaces bad kinds, filenames, content or queries.
	ErrInvalid = errors.New("invalid request")
)

// Service is the audit-service view over the shared docintel store.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds a service over a fresh shared store.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(), logger: logger}
}

// Ingest stores one parser-output text with classification + metadata.
func (s *Service) Ingest(kind, filename, contentText string) (*shared.Document, error) {
	doc, err := s.store.Ingest(kind, filename, contentText)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("doc_id", doc.ID).Str("kind", doc.Classification.Kind).Float64("confidence", doc.Classification.Confidence).Msg("document ingested")
	return doc, nil
}

// Classify returns the stored classification for one document.
func (s *Service) Classify(id string) (*shared.Classification, error) {
	c, err := s.store.Classify(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return c, nil
}

// Get returns one document.
func (s *Service) Get(id string) (*shared.Document, error) {
	doc, err := s.store.Get(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return doc, nil
}

// Search runs a structured query over the full-text index.
func (s *Service) Search(q shared.Query) ([]shared.Document, error) {
	res, err := s.store.Search(q)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("text", q.Text).Int("results", len(res)).Msg("document search executed")
	return res, nil
}

// Totals aggregates every kind.
func (s *Service) Totals() []shared.KindTotal {
	totals := s.store.Totals()
	s.logger.Info().Int("kinds", len(totals)).Msg("document totals reported")
	return totals
}

// TotalByKind aggregates one kind.
func (s *Service) TotalByKind(kind string) (*shared.KindTotal, error) {
	t, err := s.store.TotalByKind(kind)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return t, nil
}

func mapSharedErr(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case errors.Is(err, shared.ErrInvalid):
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	default:
		return err
	}
}
