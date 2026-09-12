// Package eventsourcing wires shared/eventsourcing into replay-service:
//
//   - append-only per-aggregate event store with optimistic concurrency
//   - named projector registry with read-model rebuild to any seq
//   - deterministic pinned debug replay (ReplayAt) with rule-version
//     mismatch detection.
//
// Type names are deliberately distinct from internal/domain (ReplayRequest,
// ReplayStep, TimeTravel*) to avoid collisions in the same module.
package eventsourcing

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/eventsourcing"
)

var (
	// ErrNotFound surfaces unknown aggregates or projectors.
	ErrNotFound = errors.New("not found")
	// ErrConflict surfaces optimistic-concurrency mismatches and
	// duplicate projector registrations.
	ErrConflict = errors.New("conflict")
	// ErrBlocked surfaces payments attempted while blocked.
	ErrBlocked = errors.New("conflict")
	// ErrInvalid surfaces malformed append/project/replay input.
	ErrInvalid = errors.New("invalid request")
)

// PinnedReplay pins a deterministic debug replay to an instant + rule
// versions.
type PinnedReplay struct {
	At           time.Time         `json:"at"`
	RuleVersions map[string]string `json:"rule_versions"`
}

// Service is the replay-service view over the shared event-sourcing store.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds a service over a fresh shared store (customer
// projectors balance/cards/pots pre-registered).
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(), logger: logger}
}

// Append adds one event with optimistic concurrency.
func (s *Service) Append(aggregateID string, expectedSeq int, eventType string, payload json.RawMessage, at time.Time) (*shared.CSEvent, error) {
	ev, err := s.store.Append(aggregateID, expectedSeq, eventType, payload, at)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("aggregate_id", aggregateID).Int("seq", ev.Seq).Str("type", eventType).Msg("event appended")
	return ev, nil
}

// LoadStream returns one aggregate stream in seq order.
func (s *Service) LoadStream(aggregateID string) ([]shared.CSEvent, error) {
	events, err := s.store.Load(aggregateID)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	return events, nil
}

// ProjectTo rebuilds the read model to toSeq (0 means latest).
func (s *Service) ProjectTo(aggregateID string, toSeq int) (*shared.CustomerState, error) {
	state, err := s.store.Project(aggregateID, toSeq)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("aggregate_id", aggregateID).Int("cursor", state.CursorSeq).Msg("read model projected")
	return state, nil
}

// RegisterNamedProjector records one named projector over event types. The
// projector folds with a no-op so registration is observable without
// changing the customer read model.
func (s *Service) RegisterNamedProjector(name string, eventTypes []string) error {
	if err := s.store.RegisterProjector(name, eventTypes, func(state *shared.CustomerState, ev shared.CSEvent) error {
		return nil
	}); err != nil {
		return mapSharedErr(err)
	}
	s.logger.Info().Str("projector", name).Msg("projector registered")
	return nil
}

// ReplayAt deterministically replays the stored stream up to pin.At with
// pinned rule versions, returning state + cursor + rules hash.
func (s *Service) ReplayAt(aggregateID string, pin PinnedReplay) (*shared.DebugReplayResult, error) {
	res, err := s.store.ReplayAt(aggregateID, pin.At, pin.RuleVersions)
	if err != nil {
		return nil, mapSharedErr(err)
	}
	s.logger.Info().Str("aggregate_id", aggregateID).Int("cursor", res.CursorSeq).Str("rules_hash", res.RulesHash).Msg("pinned debug replay complete")
	return res, nil
}

// ParseOptionalTime parses an RFC3339 timestamp; empty means now.
func ParseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Now().UTC(), nil
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid timestamp, use RFC3339", ErrInvalid)
	}
	return ts, nil
}

func mapSharedErr(err error) error {
	switch {
	case errors.Is(err, shared.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, err.Error())
	case errors.Is(err, shared.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, err.Error())
	case errors.Is(err, shared.ErrBlocked):
		return fmt.Errorf("%w: %s", ErrBlocked, err.Error())
	case errors.Is(err, shared.ErrUnknownRule):
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	case errors.Is(err, shared.ErrInvalid):
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	default:
		return err
	}
}
