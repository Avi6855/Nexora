// Package consentcentre is the Consent Centre extension: per-provider,
// per-datatype grants with purpose, expiry and a per-access audit trail.
//
// Each connected provider (HSBC, Amex, investment provider, ...) carries
// per-datatype toggles {balance, transactions, holdings,
// payment_initiation, sell} plus a purpose and an expiry. Every access
// check — ALLOW or DENY — appends an audit entry {who, what, when,
// purpose, ticket}. Enforcement is clock-aware and the expiry sweep marks
// providers whose window has closed. State is held in memory behind one
// mutex, mirroring the openbanking/aigateway extensions.
package consentcentre

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Datatypes grantable per provider.
const (
	DataBalance           = "balance"
	DataTransactions      = "transactions"
	DataHoldings          = "holdings"
	DataPaymentInitiation = "payment_initiation"
	DataSell              = "sell"
)

// AllDatatypes lists every toggleable datatype.
var AllDatatypes = []string{
	DataBalance,
	DataTransactions,
	DataHoldings,
	DataPaymentInitiation,
	DataSell,
}

// ValidDatatype reports whether a datatype string is toggleable.
func ValidDatatype(d string) bool {
	for _, v := range AllDatatypes {
		if v == d {
			return true
		}
	}
	return false
}

func normaliseDatatype(d string) string {
	return strings.ToLower(strings.TrimSpace(d))
}

var (
	ErrProviderNotFound = errors.New("provider not found")
	ErrProviderExists   = errors.New("provider already connected")
	ErrProviderRevoked  = errors.New("provider consent is revoked")
	ErrProviderExpired  = errors.New("provider consent is expired")
	ErrAlreadyRevoked   = errors.New("provider consent is already revoked")
	ErrInvalidDatatype  = errors.New("unknown datatype (balance, transactions, holdings, payment_initiation, sell)")
	ErrNameRequired     = errors.New("provider name is required")
	ErrPurposeRequired  = errors.New("purpose is required")
	ErrExpiryRequired   = errors.New("expiry must be a future time")
)

// AccessEntry captures one exercised (or refused) data access.
type AccessEntry struct {
	Who     string    `json:"who"`
	What    string    `json:"what"`
	When    time.Time `json:"when"`
	Purpose string    `json:"purpose"`
	Ticket  string    `json:"ticket"`
	Allowed bool      `json:"allowed"`
	Reason  string    `json:"reason"`
}

// ProviderConnection is one connected provider with its datatype toggles.
type ProviderConnection struct {
	Name        string          `json:"name"`
	Toggles     map[string]bool `json:"toggles"`
	Purpose     string          `json:"purpose"`
	ConnectedAt time.Time       `json:"connected_at"`
	ExpiresAt   time.Time       `json:"expires_at"`
	Revoked     bool            `json:"revoked"`
	RevokedAt   *time.Time      `json:"revoked_at,omitempty"`
	Expired     bool            `json:"expired"`
	AccessLog   []AccessEntry   `json:"-"`
}

// Status resolves the lifecycle against the clock.
func (p *ProviderConnection) Status(now time.Time) string {
	if p.Revoked {
		return "REVOKED"
	}
	if p.Expired || now.After(p.ExpiresAt) {
		return "EXPIRED"
	}
	return "ACTIVE"
}

// Service holds the connected providers behind one mutex.
type Service struct {
	mu        sync.Mutex
	providers map[string]*ProviderConnection
	logger    zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{providers: map[string]*ProviderConnection{}, logger: logger}
}

// Connect connects a provider with per-datatype toggles, a purpose and an
// expiry. A nil/empty toggle map enables every datatype. Reconnecting a
// revoked or expired provider replaces the old connection; connecting an
// already-active provider is a conflict.
func (s *Service) Connect(name, purpose string, toggles map[string]bool, expiresAt, now time.Time) (*ProviderConnection, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		s.logger.Warn().Msg("consent-centre connect failed: name required")
		return nil, ErrNameRequired
	}
	if strings.TrimSpace(purpose) == "" {
		s.logger.Warn().Str("provider", name).Msg("consent-centre connect failed: purpose required")
		return nil, ErrPurposeRequired
	}
	if expiresAt.IsZero() || !expiresAt.After(now) {
		s.logger.Warn().Str("provider", name).Msg("consent-centre connect failed: bad expiry")
		return nil, ErrExpiryRequired
	}
	resolved := map[string]bool{}
	if len(toggles) == 0 {
		for _, d := range AllDatatypes {
			resolved[d] = true
		}
	} else {
		for raw, on := range toggles {
			d := normaliseDatatype(raw)
			if !ValidDatatype(d) {
				err := fmt.Errorf("%w: %q", ErrInvalidDatatype, raw)
				s.logger.Warn().Err(err).Str("provider", name).Msg("consent-centre connect failed")
				return nil, err
			}
			resolved[d] = on
		}
		for _, d := range AllDatatypes {
			if _, ok := resolved[d]; !ok {
				resolved[d] = false
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.providers[name]; ok && existing.Status(now) == "ACTIVE" {
		err := fmt.Errorf("%w: %s", ErrProviderExists, name)
		s.logger.Warn().Err(err).Str("provider", name).Msg("consent-centre connect failed")
		return nil, err
	}
	p := &ProviderConnection{
		Name:        name,
		Toggles:     resolved,
		Purpose:     strings.TrimSpace(purpose),
		ConnectedAt: now,
		ExpiresAt:   expiresAt,
		AccessLog:   []AccessEntry{},
	}
	s.providers[name] = p
	s.logger.Info().Str("provider", p.Name).Str("purpose", p.Purpose).Msg("consent-centre provider connected")
	return p.copy(), nil
}

// SetToggle flips one datatype toggle for a connected provider.
func (s *Service) SetToggle(provider, datatype string, enabled bool, now time.Time) error {
	d := normaliseDatatype(datatype)
	if !ValidDatatype(d) {
		err := fmt.Errorf("%w: %q", ErrInvalidDatatype, datatype)
		s.logger.Warn().Err(err).Str("provider", provider).Msg("consent-centre toggle failed")
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[strings.TrimSpace(provider)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrProviderNotFound, provider)
		s.logger.Warn().Err(err).Str("provider", provider).Msg("consent-centre toggle failed")
		return err
	}
	if p.Revoked {
		err := fmt.Errorf("%w: %s", ErrProviderRevoked, p.Name)
		s.logger.Warn().Err(err).Str("provider", p.Name).Msg("consent-centre toggle failed")
		return err
	}
	if p.Expired || now.After(p.ExpiresAt) {
		p.Expired = true
		err := fmt.Errorf("%w: %s", ErrProviderExpired, p.Name)
		s.logger.Warn().Err(err).Str("provider", p.Name).Msg("consent-centre toggle failed")
		return err
	}
	p.Toggles[d] = enabled
	s.logger.Info().Str("provider", p.Name).Str("datatype", d).Bool("enabled", enabled).Msg("consent-centre toggle updated")
	return nil
}

// Check answers "may this provider share this datatype?" and records a
// per-access audit entry for every decision — allowed or refused.
func (s *Service) Check(provider, datatype, who, purpose, ticket string, now time.Time) (bool, string, error) {
	d := normaliseDatatype(datatype)
	if !ValidDatatype(d) {
		err := fmt.Errorf("%w: %q", ErrInvalidDatatype, datatype)
		s.logger.Warn().Err(err).Str("provider", provider).Msg("consent-centre check failed")
		return false, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[strings.TrimSpace(provider)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrProviderNotFound, provider)
		s.logger.Warn().Err(err).Str("provider", provider).Msg("consent-centre check failed")
		return false, "", err
	}
	allowed := false
	reason := ""
	switch {
	case p.Revoked:
		reason = "provider consent is revoked"
	case p.Expired || now.After(p.ExpiresAt):
		p.Expired = true
		reason = "provider consent is expired"
	case !p.Toggles[d]:
		reason = fmt.Sprintf("datatype %q is disabled for provider %s", d, p.Name)
	default:
		allowed = true
		reason = "datatype enabled and consent window open"
	}
	p.AccessLog = append(p.AccessLog, AccessEntry{
		Who:     who,
		What:    d,
		When:    now,
		Purpose: purpose,
		Ticket:  ticket,
		Allowed: allowed,
		Reason:  reason,
	})
	if allowed {
		s.logger.Info().Str("provider", p.Name).Str("datatype", d).Str("decision", "ALLOW").Msg("consent-centre access checked")
	} else {
		s.logger.Warn().Str("provider", p.Name).Str("datatype", d).Str("decision", "DENY").Str("reason", reason).Msg("consent-centre access denied")
	}
	return allowed, reason, nil
}

// Revoke disconnects a provider immediately, keeping its access log.
func (s *Service) Revoke(provider string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[strings.TrimSpace(provider)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrProviderNotFound, provider)
		s.logger.Warn().Err(err).Str("provider", provider).Msg("consent-centre revoke failed")
		return err
	}
	if p.Revoked {
		err := fmt.Errorf("%w: %s", ErrAlreadyRevoked, p.Name)
		s.logger.Warn().Err(err).Str("provider", p.Name).Msg("consent-centre revoke failed")
		return err
	}
	p.Revoked = true
	p.RevokedAt = &now
	s.logger.Info().Str("provider", p.Name).Msg("consent-centre provider revoked")
	return nil
}

// AccessLog returns the per-access audit entries for a provider, oldest first.
func (s *Service) AccessLog(provider string) ([]AccessEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.providers[strings.TrimSpace(provider)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrProviderNotFound, provider)
		s.logger.Warn().Err(err).Str("provider", provider).Msg("consent-centre access log failed")
		return nil, err
	}
	out := make([]AccessEntry, len(p.AccessLog))
	copy(out, p.AccessLog)
	return out, nil
}

// Sweep marks every provider past its expiry and returns the swept names.
func (s *Service) Sweep(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var swept []string
	for _, p := range s.providers {
		if !p.Revoked && !p.Expired && now.After(p.ExpiresAt) {
			p.Expired = true
			swept = append(swept, p.Name)
		}
	}
	if len(swept) > 0 {
		s.logger.Info().Int("swept", len(swept)).Msg("consent-centre expiry sweep completed")
	}
	return swept
}

func (p *ProviderConnection) copy() *ProviderConnection {
	c := *p
	c.Toggles = map[string]bool{}
	for k, v := range p.Toggles {
		c.Toggles[k] = v
	}
	c.AccessLog = append([]AccessEntry{}, p.AccessLog...)
	return &c
}
