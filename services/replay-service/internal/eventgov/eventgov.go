// Package eventgov wires shared/eventgov into replay-service:
//
//   - traffic replay lab (capture / runs / diffs) for load-shape regression
//   - producer schema registry (register / compatibility / deprecation /
//     migration plans) guarding event evolution.
package eventgov

import (
	"errors"
	"fmt"
	"time"

	shared "github.com/nexora/nexora/shared/eventgov"
)

var (
	// ErrNotFound surfaces unknown scenarios, runs, topics or versions.
	ErrNotFound = errors.New("not found")
	// ErrConflict surfaces duplicate schema versions.
	ErrConflict = errors.New("conflict")
)

// Service is the replay-service view over the shared governance engines.
type Service struct {
	lab *shared.Lab
	reg *shared.Registry
}

// NewService builds a service over fresh shared engines.
func NewService() *Service {
	return &Service{lab: shared.NewLab(), reg: shared.NewRegistry()}
}

// Capture stores one sanitized envelope under a scenario.
func (s *Service) Capture(scenario string, env shared.Envelope) error {
	if err := s.lab.Capture(scenario, env); err != nil {
		return mapSharedErr(err)
	}
	return nil
}

// StartRun replays a scenario at 1x/10x/100x/1000x fan-out.
func (s *Service) StartRun(scenario string, multiplier int) (*shared.Run, error) {
	run, err := s.lab.StartRun(scenario, multiplier)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return run, nil
}

// GetRun returns one run.
func (s *Service) GetRun(id string) (*shared.Run, error) {
	run, err := s.lab.GetRun(id)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return run, nil
}

// DiffRuns compares a candidate run against a baseline.
func (s *Service) DiffRuns(baseID, candidateID string) (*shared.RunDiff, error) {
	diff, err := s.lab.DiffRuns(baseID, candidateID)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return diff, nil
}

// RegisterSchema stores a producer schema version.
func (s *Service) RegisterSchema(topic string, version int, fields []shared.Field) (*shared.Schema, error) {
	schema, err := s.reg.RegisterSchema(topic, version, fields)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return schema, nil
}

// CheckCompatibility runs a BACKWARD/FORWARD/FULL consumer check.
func (s *Service) CheckCompatibility(topic string, oldVersion, newVersion int, mode shared.CompatibilityMode) (*shared.CompatibilityResult, error) {
	res, err := s.reg.CheckCompatibility(topic, oldVersion, newVersion, mode)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return res, nil
}

// DeprecateField marks one field deprecated.
func (s *Service) DeprecateField(topic string, version int, field string) error {
	if err := s.reg.DeprecateField(topic, version, field); err != nil {
		return mapSharedErr(err)
	}
	return nil
}

// MigrationPlan grades the move between two versions.
func (s *Service) MigrationPlan(topic string, fromVersion, toVersion int) (*shared.MigrationPlan, error) {
	plan, err := s.reg.MigrationPlan(topic, fromVersion, toVersion)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return plan, nil
}

// ParseTime parses an optional RFC3339 timestamp; empty means now.
func ParseTime(raw string) (time.Time, error) {
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
