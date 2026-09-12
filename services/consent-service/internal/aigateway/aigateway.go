// Package aigateway wires shared/aigateway into consent-service: AI agents
// act only under delegated consent scopes, with limit + risk gates, human
// confirmation for mutating actions, and an executed/skipped stub outcome.
package aigateway

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/aigateway"
)

// Service enforces delegated consent around AI intents.
type Service struct {
	gw     *shared.Gateway
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{gw: shared.NewGateway(), logger: logger}
}

// GrantScope delegates consent for recipient+action.
func (s *Service) GrantScope(recipient, action string, limitMinor int64, maxRisk int, now time.Time) error {
	if err := s.gw.GrantScope(recipient, action, limitMinor, maxRisk, now); err != nil {
		s.logger.Warn().Err(err).Str("recipient", recipient).Str("action", action).Msg("ai scope grant failed")
		return err
	}
	s.logger.Info().Str("recipient", recipient).Str("action", action).Int64("limit_minor", limitMinor).Msg("ai scope granted")
	return nil
}

// RevokeScope removes a delegated scope.
func (s *Service) RevokeScope(recipient, action string) error {
	if err := s.gw.RevokeScope(recipient, action); err != nil {
		s.logger.Warn().Err(err).Str("recipient", recipient).Str("action", action).Msg("ai scope revoke failed")
		return err
	}
	s.logger.Info().Str("recipient", recipient).Str("action", action).Msg("ai scope revoked")
	return nil
}

// SubmitIntent stages an AI intent under its consent scope.
func (s *Service) SubmitIntent(action string, params map[string]string, now time.Time) (*shared.Intent, error) {
	if strings.TrimSpace(action) == "" {
		err := fmt.Errorf("action is required")
		s.logger.Warn().Err(err).Msg("ai intent submit failed")
		return nil, err
	}
	in, err := s.gw.SubmitIntent(action, params, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("action", action).Msg("ai intent blocked")
		return nil, err
	}
	s.logger.Info().Str("intent_id", in.ID).Str("action", in.Action).Str("recipient", in.Recipient).Msg("ai intent submitted")
	return in, nil
}

// Confirm redeems the human-confirmation token.
func (s *Service) Confirm(id, token string, now time.Time) error {
	if err := s.gw.Confirm(id, token, now); err != nil {
		s.logger.Warn().Err(err).Str("intent_id", id).Msg("ai intent confirm failed")
		return err
	}
	s.logger.Info().Str("intent_id", id).Msg("ai intent confirmed")
	return nil
}

// Execute runs the stub, returning executed/skipped plus a reason.
func (s *Service) Execute(id string, now time.Time) (string, string, error) {
	outcome, reason, err := s.gw.Execute(id, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("intent_id", id).Msg("ai intent execute failed")
		return "", "", err
	}
	if outcome == "executed" {
		s.logger.Info().Str("intent_id", id).Str("outcome", outcome).Msg("ai intent executed")
	} else {
		s.logger.Warn().Str("intent_id", id).Str("outcome", outcome).Str("reason", reason).Msg("ai intent skipped")
	}
	return outcome, reason, nil
}

// Get returns an intent.
func (s *Service) Get(id string) (*shared.Intent, error) {
	in, err := s.gw.Get(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("intent_id", id).Msg("ai intent get failed")
		return nil, err
	}
	return in, nil
}
