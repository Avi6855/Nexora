// Package fxtrack wires the shared international payment corridor tracker
// into transfer-service as a live HTTP surface.
package fxtrack

import (
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/shared/fxtrack"
)

// Service is the fx tracking live state: one shared tracker.
type Service struct {
	tracker *fxtrack.Tracker
	logger  zerolog.Logger
}

// NewService returns an empty fx service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{tracker: fxtrack.NewTracker(), logger: logger}
}

// StartTransfer begins tracking a corridor transfer.
func (s *Service) StartTransfer(id, corridor string, amountMinor int64, now time.Time) (*fxtrack.Transfer, error) {
	if id == "" {
		id = uuid.NewString()
	}
	tr, err := s.tracker.StartTransfer(id, corridor, amountMinor, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("corridor", corridor).Msg("fx transfer start failed")
		return nil, err
	}
	s.logger.Info().Str("transfer_id", tr.ID).Str("corridor", tr.Corridor).Msg("fx transfer tracking started")
	return tr, nil
}

// Advance moves the transfer one corridor stage forward.
func (s *Service) Advance(id string, now time.Time) (*fxtrack.Transfer, error) {
	tr, err := s.tracker.AdvanceStage(id, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("transfer_id", id).Msg("fx transfer advance failed")
		return nil, err
	}
	s.logger.Info().Str("transfer_id", id).Str("stage", string(tr.CurrentStage)).Msg("fx transfer advanced")
	return tr, nil
}

// Get returns one transfer.
func (s *Service) Get(id string) (*fxtrack.Transfer, error) {
	tr, err := s.tracker.Get(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("transfer_id", id).Msg("fx transfer get failed")
		return nil, err
	}
	return tr, nil
}

// ETAWindow computes the p50/p95 ETA window.
func (s *Service) ETAWindow(id string, now time.Time) (time.Time, time.Time, error) {
	earliest, latest, err := s.tracker.ETAWindow(id, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("transfer_id", id).Msg("fx eta failed")
		return time.Time{}, time.Time{}, err
	}
	return earliest, latest, nil
}

// DelayedAt attributes delay to the current stage plus overrun.
func (s *Service) DelayedAt(id string, now time.Time) (fxtrack.DelayInfo, error) {
	info, err := s.tracker.DelayedAt(id, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("transfer_id", id).Msg("fx delay lookup failed")
		return fxtrack.DelayInfo{}, err
	}
	return info, nil
}

// Timeline returns the stage visit history.
func (s *Service) Timeline(id string) ([]fxtrack.StageVisit, error) {
	tl, err := s.tracker.Timeline(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("transfer_id", id).Msg("fx timeline failed")
		return nil, err
	}
	return tl, nil
}
