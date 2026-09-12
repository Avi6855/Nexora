// Package delegated is the Delegated Permissions extension: granular,
// time-bound, capability-scoped grants with an explicit DENY list, scoped
// account sets and per-grant usage audit.
//
// It builds on the existing DelegationGrant concept (read/report-scoped,
// time-boxed delegation) without touching it: capabilities mirror the
// domain scopes {view_balance, view_transactions, download_statements},
// money-movement-style actions {make_payments, add_beneficiaries,
// change_details} are never delegable, the {start,end} window is enforced
// on every use, each grant is scoped to one account set (household-only,
// child-only or business-only views) and every use — ALLOW or DENY — is
// recorded for audit. Grants can be revoked early. State is held in memory
// behind one mutex, mirroring the openbanking/aigateway extensions.
package delegated

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// Grantable capabilities.
const (
	CapViewBalance        = "view_balance"
	CapViewTransactions   = "view_transactions"
	CapDownloadStatements = "download_statements"
)

// AllCapabilities lists every grantable capability.
var AllCapabilities = []string{
	CapViewBalance,
	CapViewTransactions,
	CapDownloadStatements,
}

// Explicit DENY list: never delegable, denied on every use.
const (
	DenyMakePayments     = "make_payments"
	DenyAddBeneficiaries = "add_beneficiaries"
	DenyChangeDetails    = "change_details"
)

// DeniedCapabilities lists the explicit DENY actions.
var DeniedCapabilities = []string{
	DenyMakePayments,
	DenyAddBeneficiaries,
	DenyChangeDetails,
}

// Scoped account sets: one view per grant.
const (
	AccountsHousehold = "household"
	AccountsChild     = "child"
	AccountsBusiness  = "business"
)

// AllAccountSets lists every valid account set.
var AllAccountSets = []string{
	AccountsHousehold,
	AccountsChild,
	AccountsBusiness,
}

// maxGrantWindow mirrors the domain 90-day delegation cap.
const maxGrantWindow = 90 * 24 * time.Hour

func normalise(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidCapability reports whether a capability is grantable.
func ValidCapability(c string) bool {
	for _, v := range AllCapabilities {
		if v == c {
			return true
		}
	}
	return false
}

// DeniedCapability reports whether a capability sits on the explicit DENY list.
func DeniedCapability(c string) bool {
	for _, v := range DeniedCapabilities {
		if v == c {
			return true
		}
	}
	return false
}

// ValidAccountSet reports whether an account set is known.
func ValidAccountSet(a string) bool {
	for _, v := range AllAccountSets {
		if v == a {
			return true
		}
	}
	return false
}

var (
	ErrGrantNotFound      = errors.New("delegated grant not found")
	ErrAlreadyRevoked     = errors.New("delegated grant is already revoked")
	ErrGranteeRequired    = errors.New("grantee is required")
	ErrCapabilityRequired = errors.New("at least one capability is required (view_balance, view_transactions, download_statements)")
	ErrInvalidCapability  = errors.New("unknown capability")
	ErrDeniedCapability   = errors.New("capability is on the explicit deny list and is never delegable")
	ErrInvalidWindow      = errors.New("grant window invalid: end must be after start and within 90 days")
	ErrInvalidAccountSet  = errors.New("unknown account set (household, child, business)")
	ErrAccountSetRequired = errors.New("account_set is required")
)

// Grant is one capability-scoped, time-bound, account-set-scoped delegation.
type Grant struct {
	ID           string     `json:"id"`
	Grantee      string     `json:"grantee"`
	Capabilities []string   `json:"capabilities"`
	AccountSet   string     `json:"account_set"`
	Start        time.Time  `json:"start"`
	End          time.Time  `json:"end"`
	Revoked      bool       `json:"revoked"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// Status resolves the lifecycle against the clock.
func (g *Grant) Status(now time.Time) string {
	if g.Revoked {
		return "REVOKED"
	}
	if now.After(g.End) {
		return "EXPIRED"
	}
	return "ACTIVE"
}

// UsageEntry captures one exercised (or refused) capability use.
type UsageEntry struct {
	When       time.Time `json:"when"`
	Capability string    `json:"capability"`
	AccountSet string    `json:"account_set"`
	Allowed    bool      `json:"allowed"`
	Reason     string    `json:"reason"`
}

type grantRecord struct {
	grant Grant
	usage []UsageEntry
}

// Service holds the delegated grants behind one mutex.
type Service struct {
	mu     sync.Mutex
	grants map[string]*grantRecord
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{grants: map[string]*grantRecord{}, logger: logger}
}

// Grant issues a new capability-scoped grant. Capabilities on the explicit
// DENY list are rejected outright.
func (s *Service) Grant(grantee string, capabilities []string, start, end time.Time, accountSet string, now time.Time) (*Grant, error) {
	grantee = strings.TrimSpace(grantee)
	if grantee == "" {
		s.logger.Warn().Msg("delegated grant failed: grantee required")
		return nil, ErrGranteeRequired
	}
	if len(capabilities) == 0 {
		s.logger.Warn().Str("grantee", grantee).Msg("delegated grant failed: capabilities required")
		return nil, ErrCapabilityRequired
	}
	seen := map[string]bool{}
	caps := make([]string, 0, len(capabilities))
	for _, raw := range capabilities {
		c := normalise(raw)
		if DeniedCapability(c) {
			err := fmt.Errorf("%w: %q", ErrDeniedCapability, raw)
			s.logger.Warn().Err(err).Str("grantee", grantee).Msg("delegated grant denied")
			return nil, err
		}
		if !ValidCapability(c) {
			err := fmt.Errorf("%w: %q", ErrInvalidCapability, raw)
			s.logger.Warn().Err(err).Str("grantee", grantee).Msg("delegated grant failed")
			return nil, err
		}
		if !seen[c] {
			seen[c] = true
			caps = append(caps, c)
		}
	}
	accountSet = normalise(accountSet)
	if accountSet == "" {
		s.logger.Warn().Str("grantee", grantee).Msg("delegated grant failed: account set required")
		return nil, ErrAccountSetRequired
	}
	if !ValidAccountSet(accountSet) {
		err := fmt.Errorf("%w: %q", ErrInvalidAccountSet, accountSet)
		s.logger.Warn().Err(err).Str("grantee", grantee).Msg("delegated grant failed")
		return nil, err
	}
	if start.IsZero() {
		start = now
	}
	if end.IsZero() || !end.After(start) || end.Sub(start) > maxGrantWindow {
		s.logger.Warn().Str("grantee", grantee).Msg("delegated grant failed: invalid window")
		return nil, ErrInvalidWindow
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	g := Grant{
		ID:           uuid.New().String(),
		Grantee:      grantee,
		Capabilities: caps,
		AccountSet:   accountSet,
		Start:        start,
		End:          end,
		CreatedAt:    now,
	}
	s.grants[g.ID] = &grantRecord{grant: g}
	s.logger.Info().Str("grant_id", g.ID).Str("grantee", g.Grantee).Str("account_set", g.AccountSet).Msg("delegated grant created")
	out := g
	return &out, nil
}

// Use exercises one capability under a grant, enforcing the DENY list, the
// time window, the capability scope and the account set. Every call appends
// a usage audit entry and returns ALLOW/DENY with a reason.
func (s *Service) Use(id, capability, accountSet string, now time.Time) (bool, string, error) {
	capability = normalise(capability)
	if capability == "" {
		s.logger.Warn().Str("grant_id", id).Msg("delegated use failed: capability required")
		return false, "", fmt.Errorf("capability is required")
	}
	accountSet = normalise(accountSet)
	if accountSet == "" {
		s.logger.Warn().Str("grant_id", id).Msg("delegated use failed: account set required")
		return false, "", ErrAccountSetRequired
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.grants[strings.TrimSpace(id)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrGrantNotFound, id)
		s.logger.Warn().Err(err).Str("grant_id", id).Msg("delegated use failed")
		return false, "", err
	}
	g := &rec.grant
	allowed := false
	reason := ""
	switch {
	case g.Revoked:
		reason = "grant is revoked"
	case DeniedCapability(capability):
		reason = fmt.Sprintf("capability %q is explicitly denied and never delegable", capability)
	case now.Before(g.Start):
		reason = "grant window has not started"
	case now.After(g.End):
		reason = "grant is expired"
	case g.AccountSet != accountSet:
		reason = fmt.Sprintf("grant is scoped to account set %q", g.AccountSet)
	case !hasCapability(g.Capabilities, capability):
		reason = fmt.Sprintf("grant does not include capability %q", capability)
	default:
		allowed = true
		reason = "capability granted and window open"
	}
	rec.usage = append(rec.usage, UsageEntry{
		When:       now,
		Capability: capability,
		AccountSet: accountSet,
		Allowed:    allowed,
		Reason:     reason,
	})
	if allowed {
		s.logger.Info().Str("grant_id", g.ID).Str("capability", capability).Str("decision", "ALLOW").Msg("delegated grant used")
	} else {
		s.logger.Warn().Str("grant_id", g.ID).Str("capability", capability).Str("decision", "DENY").Str("reason", reason).Msg("delegated grant denied")
	}
	return allowed, reason, nil
}

// Revoke revokes a grant early. Only a live grant can be revoked.
func (s *Service) Revoke(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.grants[strings.TrimSpace(id)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrGrantNotFound, id)
		s.logger.Warn().Err(err).Str("grant_id", id).Msg("delegated revoke failed")
		return err
	}
	if rec.grant.Revoked {
		err := fmt.Errorf("%w: %s", ErrAlreadyRevoked, id)
		s.logger.Warn().Err(err).Str("grant_id", id).Msg("delegated revoke failed")
		return err
	}
	rec.grant.Revoked = true
	rec.grant.RevokedAt = &now
	s.logger.Info().Str("grant_id", id).Msg("delegated grant revoked")
	return nil
}

// Audit returns the usage entries for a grant, oldest first.
func (s *Service) Audit(id string) ([]UsageEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.grants[strings.TrimSpace(id)]
	if !ok {
		err := fmt.Errorf("%w: %s", ErrGrantNotFound, id)
		s.logger.Warn().Err(err).Str("grant_id", id).Msg("delegated audit failed")
		return nil, err
	}
	out := make([]UsageEntry, len(rec.usage))
	copy(out, rec.usage)
	return out, nil
}

func hasCapability(caps []string, wanted string) bool {
	for _, c := range caps {
		if c == wanted {
			return true
		}
	}
	return false
}
