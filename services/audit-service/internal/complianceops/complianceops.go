// Package complianceops wires shared/complianceops into audit-service:
// evidence bundles, case snapshots, the SLA engine, risk-based sampling,
// coverage analysis and control-test runs.
package complianceops

import (
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/complianceops"
)

var (
	// ErrNotFound surfaces unknown bundles, snapshots, cases, requirements and runs.
	ErrNotFound = errors.New("not found")
	// ErrInvalid surfaces malformed requests.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict surfaces duplicates and illegal transitions.
	ErrConflict = errors.New("conflict")
)

// Service is the audit-service view over the shared compliance-ops store.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds a service over a fresh shared store.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(), logger: logger}
}

// BuildBundle seals an investigation's evidence graph.
func (s *Service) BuildBundle(investigationID string, g shared.EvidenceGraph, now time.Time) (*shared.Bundle, error) {
	b, err := s.store.BuildBundle(investigationID, g, now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("bundle", b.ID).Str("investigation", investigationID).Int("entries", len(b.Entries)).Msg("evidence bundle sealed")
	return b, nil
}

// GetBundle returns one bundle.
func (s *Service) GetBundle(id string) (*shared.Bundle, error) {
	b, err := s.store.GetBundle(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return b, nil
}

// VerifyBundle verifies one bundle.
func (s *Service) VerifyBundle(id string) (string, error) {
	v, err := s.store.Verify(id)
	if err != nil {
		return "", mapSharedErr(err)
	}
	return v, nil
}

// SnapshotCase pins customer state at investigation start.
func (s *Service) SnapshotCase(investigationID, customerID, state string, now time.Time) (*shared.Snapshot, error) {
	snap, err := s.store.SnapshotCase(investigationID, customerID, state, now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("investigation", investigationID).Str("customer", customerID).Msg("case snapshot pinned")
	return snap, nil
}

// GetSnapshot returns the pinned snapshot.
func (s *Service) GetSnapshot(investigationID string) (*shared.Snapshot, error) {
	snap, err := s.store.GetSnapshot(investigationID)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return snap, nil
}

// SetCurrentState records upstream drift of the live customer record.
func (s *Service) SetCurrentState(customerID, state string) {
	s.store.SetCurrentState(customerID, state)
	s.logger.Info().Str("customer", customerID).Msg("current customer state updated")
}

// SetSLAPolicy registers a per-type SLA policy.
func (s *Service) SetSLAPolicy(p shared.SLAPolicy) error {
	if err := s.store.SetSLAPolicy(p); err != nil {
		return mapSharedErr(err)
	}
	s.logger.Info().Str("case_type", p.CaseType).Msg("sla policy updated")
	return nil
}

// OpenCase tracks one investigation under its type policy.
func (s *Service) OpenCase(caseType string, now time.Time) (*shared.SLAItem, error) {
	it, err := s.store.OpenCase(caseType, now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("case", it.ID).Str("case_type", caseType).Msg("sla case opened")
	return it, nil
}

// OpenCaseWithID tracks one investigation with an explicit id/created-at.
func (s *Service) OpenCaseWithID(id, caseType string, createdAt, now time.Time) (*shared.SLAItem, error) {
	it, err := s.store.OpenCaseWithID(id, caseType, createdAt, now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("case", it.ID).Str("case_type", caseType).Msg("sla case opened")
	return it, nil
}

// Tick emits SLA warnings, assignments, breaches and escalations.
func (s *Service) Tick(now time.Time) []shared.SLAEvent {
	return s.store.Tick(now)
}

// Sample draws a risk-based sample.
func (s *Service) Sample(pop []shared.SampleDecision, n int, seed int64) ([]shared.SampleDecision, error) {
	out, err := shared.Sample(pop, n, seed)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return out, nil
}

// RegisterRequirement adds one requirement.
func (s *Service) RegisterRequirement(r shared.Requirement) error {
	if err := s.store.RegisterRequirement(r); err != nil {
		return mapSharedErr(err)
	}
	s.logger.Info().Str("requirement", r.ID).Msg("requirement registered")
	return nil
}

// Coverage reports every requirement.
func (s *Service) Coverage() []shared.CoverageEntry {
	return s.store.Coverage()
}

// RegisterControlTest adds one control expectation.
func (s *Service) RegisterControlTest(t shared.ControlTest) error {
	if err := s.store.RegisterControlTest(t); err != nil {
		return mapSharedErr(err)
	}
	s.logger.Info().Str("control", t.ID).Msg("control test registered")
	return nil
}

// RunControls evaluates every control and records a run report.
func (s *Service) RunControls(now time.Time) (*shared.ControlRun, error) {
	run, err := s.store.RunControls(now)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("run", run.ID).Int("passed", run.Passed).Int("failed", run.Failed).Msg("control run completed")
	return run, nil
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
