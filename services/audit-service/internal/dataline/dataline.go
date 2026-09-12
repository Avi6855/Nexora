// Package dataline wires shared/dataline into audit-service:
//
//   - lineage graph (nodes/edges/explain) for value provenance
//   - privacy vault (tokenise/rotate/cohort counts/audit) for
//     aggregate-only analytics
//   - retention engine (policies/classify/due/transitions/holds) for
//     lifecycle governance.
package dataline

import (
	"errors"
	"fmt"
	"time"

	shared "github.com/nexora/nexora/shared/dataline"
)

var (
	// ErrNotFound surfaces unknown values, items, holds or purposes.
	ErrNotFound = errors.New("not found")
	// ErrConflict surfaces duplicate nodes/edges/items, double holds or
	// hold-blocked transitions.
	ErrConflict = errors.New("conflict")
)

// Service is the audit-service view over the shared dataline engines.
type Service struct {
	graph  *shared.Graph
	vault  *shared.Vault
	engine *shared.Engine
}

// NewService builds a service over fresh shared engines.
func NewService() *Service {
	return &Service{
		graph:  shared.NewGraph(),
		vault:  shared.NewVault(),
		engine: shared.NewEngine(),
	}
}

// RecordNode stores one lineage node.
func (s *Service) RecordNode(n shared.Node) (*shared.Node, error) {
	out, err := s.graph.RecordNode(n)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return out, nil
}

// RecordEdge links two lineage nodes.
func (s *Service) RecordEdge(fromID, toID string) error {
	if err := s.graph.RecordEdge(fromID, toID); err != nil {
		return mapSharedErr(err)
	}
	return nil
}

// Explain returns the full chain for a value, oldest → newest.
func (s *Service) Explain(valueID string) ([]shared.Node, error) {
	chain, err := s.graph.Explain(valueID)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return chain, nil
}

// Tokenise HMAC-tokenises one raw value for a purpose.
func (s *Service) Tokenise(purpose, field, raw string) (string, error) {
	tok, err := s.vault.Tokenise(purpose, field, raw)
	if err != nil {
		return "", mapSharedErr(err)
	}
	return tok, nil
}

// RotatePurpose rotates a purpose salt, invalidating its old tokens.
func (s *Service) RotatePurpose(purpose string) error {
	if err := s.vault.RotatePurpose(purpose); err != nil {
		return mapSharedErr(err)
	}
	return nil
}

// CohortCount returns the aggregate live-token count.
func (s *Service) CohortCount(purpose, field string) (int, error) {
	n, err := s.vault.CohortCount(purpose, field)
	if err != nil {
		return 0, mapSharedErr(err)
	}
	return n, nil
}

// AuditLog returns the vault access audit entries.
func (s *Service) AuditLog() []shared.AuditEntry {
	return s.vault.AuditLog()
}

// PutPolicy replaces the retention horizons.
func (s *Service) PutPolicy(p shared.Policy) error {
	if err := s.engine.PutPolicy(p); err != nil {
		return mapSharedErr(err)
	}
	return nil
}

// Classify enrols one item in CREATED.
func (s *Service) Classify(id, category string, createdAt time.Time) (*shared.Item, error) {
	it, err := s.engine.Classify(id, category, createdAt)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return it, nil
}

// DueTransitions lists items whose next step is due.
func (s *Service) DueTransitions(now time.Time) ([]shared.DueTransition, error) {
	due, err := s.engine.DueTransitions(now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return due, nil
}

// ApplyTransition moves one item to its next lifecycle state.
func (s *Service) ApplyTransition(id string, now time.Time) (*shared.Item, error) {
	it, err := s.engine.ApplyTransition(id, now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return it, nil
}

// PlaceHold parks one item under a legal hold.
func (s *Service) PlaceHold(itemID, reason string) (*shared.Hold, error) {
	h, err := s.engine.PlaceHold(itemID, reason)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return h, nil
}

// ReleaseHold lifts one legal hold.
func (s *Service) ReleaseHold(holdID string) error {
	if err := s.engine.ReleaseHold(holdID); err != nil {
		return mapSharedErr(err)
	}
	return nil
}

// ParseOptionalTime parses an optional RFC3339 timestamp; empty means now.
func ParseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp, use RFC3339: %w", err)
	}
	return ts, nil
}

func mapSharedErr(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case errors.Is(err, shared.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	default:
		return err
	}
}
