package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Scope is one capability a delegate may exercise. Money movement is NEVER
// delegable in this model (the emergency lockdown + risk gates stay with the
// owner); grants are read/report scoped.
type Scope string

const (
	ScopeViewBalance     Scope = "VIEW_BALANCE"
	ScopeViewTransactions Scope = "VIEW_TRANSACTIONS"
	ScopeDownloadStatements Scope = "DOWNLOAD_STATEMENTS"
)

var AllScopes = []Scope{ScopeViewBalance, ScopeViewTransactions, ScopeDownloadStatements}

// ValidScope reports whether a scope string is grantable.
func ValidScope(s string) bool {
	for _, sc := range AllScopes {
		if string(sc) == s {
			return true
		}
	}
	return false
}

// GrantStatus is the lifecycle of a delegation grant.
type GrantStatus string

const (
	GrantStatusActive  GrantStatus = "ACTIVE"
	GrantStatusExpired GrantStatus = "EXPIRED"
	GrantStatusRevoked GrantStatus = "REVOKED"
)

// DelegationGrant lets another person act on the owner's account for a
// limited time with explicit capabilities ("give Avi view access for 7 days").
type DelegationGrant struct {
	GrantID       uuid.UUID  `json:"grant_id"`
	OwnerUserID   uuid.UUID  `json:"owner_user_id"`
	DelegateEmail string     `json:"delegate_email"`
	DelegateUserID uuid.UUID `json:"delegate_user_id,omitempty"`
	Label         string     `json:"label"` // who it's for: "Accountant", "Partner"...
	Scopes        []Scope    `json:"scopes"`
	Status        GrantStatus `json:"status"`
	StartsAt      time.Time  `json:"starts_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	CreatedAt     time.Time  `json:"created_at"`
	RevokedAt     *time.Time `json:"revoked_at,omitempty"`
}

// AuditRecord captures one exercised (or refused) scoped access.
type AuditRecord struct {
	GrantID        uuid.UUID `json:"grant_id"`
	UsedAt         time.Time `json:"used_at"`
	AuditID        uuid.UUID `json:"audit_id"`
	DelegateUserID uuid.UUID `json:"delegate_user_id"`
	Action         string    `json:"action"`   // e.g. "ledger.entries.read"
	Resource       string    `json:"resource"` // account/entry id
	Allowed        bool      `json:"allowed"`
	IPAddress      string    `json:"ip_address,omitempty"`
}

var (
	ErrGrantNotFound  = errors.New("delegation grant not found")
	ErrGrantNotActive = errors.New("delegation grant is not active")
	ErrNotOwner       = errors.New("grant does not belong to caller")
	ErrInvalidScopes  = errors.New("at least one valid scope is required (VIEW_BALANCE, VIEW_TRANSACTIONS, DOWNLOAD_STATEMENTS)")
	ErrInvalidWindow  = errors.New("grant window invalid: expires_at must be after starts_at and within 90 days")
	ErrSelfGrant      = errors.New("you cannot delegate access to yourself")
	ErrEmailRequired  = errors.New("delegate_email is required")
)

// EffectiveStatus resolves the stored status against the clock: an ACTIVE
// grant past its expiry is EXPIRED (checked on every enforcement read).
func (g *DelegationGrant) EffectiveStatus(now time.Time) GrantStatus {
	if g.Status == GrantStatusActive && now.After(g.ExpiresAt) {
		return GrantStatusExpired
	}
	return g.Status
}

// HasScope reports whether an active grant carries a capability.
func (g *DelegationGrant) HasScope(now time.Time, wanted Scope) bool {
	if g.EffectiveStatus(now) != GrantStatusActive {
		return false
	}
	for _, s := range g.Scopes {
		if s == wanted {
			return true
		}
	}
	return false
}

// ScopesToString serialises scopes for the Cassandra TEXT column.
func ScopesToString(scopes []Scope) string {
	parts := make([]string, 0, len(scopes))
	for _, s := range scopes {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ",")
}

// StringToScopes parses the stored scope list, dropping unknown values.
func StringToScopes(s string) []Scope {
	parts := strings.Split(s, ",")
	out := make([]Scope, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if ValidScope(p) {
			out = append(out, Scope(p))
		}
	}
	return out
}

// CreateGrantRequest is the app's "share access" payload.
type CreateGrantRequest struct {
	DelegateEmail string   `json:"delegate_email"`
	Label         string   `json:"label"`
	Scopes        []string `json:"scopes"`
	DurationDays  int      `json:"duration_days"`
}
