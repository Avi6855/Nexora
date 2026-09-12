package cardtokens

import (
	"time"

	"github.com/rs/zerolog"

	sharedtokens "github.com/nexora/nexora/shared/cardtokens"
)

// Service is the card-service's in-memory PAN-token store backed by
// shared/cardtokens.
type Service struct {
	vault  *sharedtokens.IssuerVault
	logger zerolog.Logger
}

// NewService creates a token service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{vault: sharedtokens.NewIssuerVault(logger), logger: logger}
}

// Vault exposes the underlying vault (tests, ops tooling).
func (s *Service) Vault() *sharedtokens.IssuerVault { return s.vault }

// Issue mints a token and attaches any initial dependents.
func (s *Service) Issue(panFingerprint string, scope sharedtokens.Scope, ttl time.Duration, dependents []string) (*sharedtokens.PANToken, error) {
	now := time.Now().UTC()
	tok, err := s.vault.Issue(panFingerprint, scope, ttl, now)
	if err != nil {
		return nil, err
	}
	for _, d := range dependents {
		if _, err := s.vault.AddDependent(tok.ID, d, now); err != nil {
			return nil, err
		}
	}
	if len(dependents) > 0 {
		tok, err = s.vault.Get(tok.ID)
		if err != nil {
			return nil, err
		}
	}
	s.logger.Info().Str("token_id", tok.ID).Msg("card token issued")
	return tok, nil
}

// Get returns one token.
func (s *Service) Get(id string) (*sharedtokens.PANToken, error) {
	return s.vault.Get(id)
}

// Suspend moves ACTIVE -> SUSPENDED.
func (s *Service) Suspend(id string) (*sharedtokens.PANToken, error) {
	tok, err := s.vault.Suspend(id, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("token_id", id).Msg("card token suspended")
	return tok, nil
}

// Resume moves SUSPENDED -> ACTIVE.
func (s *Service) Resume(id string) (*sharedtokens.PANToken, error) {
	tok, err := s.vault.Resume(id, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("token_id", id).Msg("card token resumed")
	return tok, nil
}

// Rotate mints the successor and remaps dependents atomically.
func (s *Service) Rotate(id string) (*sharedtokens.PANToken, error) {
	tok, err := s.vault.Rotate(id, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("old_token", id).Str("new_token", tok.ID).Msg("card token rotated")
	return tok, nil
}

// Revoke terminates a token.
func (s *Service) Revoke(id string) (*sharedtokens.PANToken, error) {
	tok, err := s.vault.Revoke(id, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("token_id", id).Msg("card token revoked")
	return tok, nil
}

// SweepExpired expires past-due tokens.
func (s *Service) SweepExpired() int {
	n := s.vault.SweepExpired(time.Now().UTC())
	s.logger.Info().Int("swept", n).Msg("card token expiry sweep")
	return n
}

// AddDependent attaches a dependent credential reference.
func (s *Service) AddDependent(id, dependent string) (*sharedtokens.PANToken, error) {
	tok, err := s.vault.AddDependent(id, dependent, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("token_id", id).Str("dependent", dependent).Msg("card token dependent attached")
	return tok, nil
}

// Authorize evaluates a token against an attempt context.
func (s *Service) Authorize(id string, ctx sharedtokens.AuthContext) (sharedtokens.Decision, string, error) {
	return s.vault.Authorize(id, ctx)
}

// NarrowScope replaces the scope with a narrower one.
func (s *Service) NarrowScope(id string, next sharedtokens.Scope) (*sharedtokens.PANToken, error) {
	tok, err := s.vault.NarrowScope(id, next, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.logger.Info().Str("token_id", id).Msg("card token scope narrowed")
	return tok, nil
}
