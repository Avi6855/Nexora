// Package cardtokens implements PAN-token issuance with a strict lifecycle
// and a narrowable scope engine.
//
// Lifecycle: ACTIVE → SUSPENDED → ROTATING → REVOKED / EXPIRED.
// Rotation creates a successor ACTIVE token and remaps dependents
// atomically under one lock. Scopes can only narrow, never widen.
package cardtokens

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// IssuerStatus is the PAN-token lifecycle state. Names are deliberately
// distinct from shared/cards TokenState.
type IssuerStatus string

const (
	IssuerStatusActive    IssuerStatus = "ACTIVE"
	IssuerStatusSuspended IssuerStatus = "SUSPENDED"
	IssuerStatusRotating  IssuerStatus = "ROTATING"
	IssuerStatusRevoked   IssuerStatus = "REVOKED"
	IssuerStatusExpired   IssuerStatus = "EXPIRED"
)

// Decision is the scope-engine verdict.
type Decision string

const (
	DecisionAllow Decision = "ALLOW"
	DecisionDeny  Decision = "DENY"
)

var (
	ErrIssuerTokenNotFound = errors.New("card token not found")
	ErrIllegalIssuerMove   = errors.New("illegal card token transition")
	ErrIssuerScopeWiden    = errors.New("scope widening requires reissue")
	ErrIssuerInvalidInput  = errors.New("invalid card token input")
)

// legalIssuerMoves enumerates allowed lifecycle edges.
var legalIssuerMoves = map[IssuerStatus][]IssuerStatus{
	IssuerStatusActive:    {IssuerStatusSuspended, IssuerStatusRotating, IssuerStatusRevoked, IssuerStatusExpired},
	IssuerStatusSuspended: {IssuerStatusActive, IssuerStatusRotating, IssuerStatusRevoked, IssuerStatusExpired},
	IssuerStatusRotating:  {IssuerStatusRevoked, IssuerStatusExpired},
	IssuerStatusRevoked:   {},
	IssuerStatusExpired:   {},
}

// Scope constrains where a token may be used. Empty slices mean
// unconstrained; MaxAmountMinor <= 0 means no amount cap; zero
// WindowStart/End means no time restriction.
type Scope struct {
	Merchants      []string  `json:"merchants,omitempty"`
	Countries      []string  `json:"countries,omitempty"`
	Channels       []string  `json:"channels,omitempty"`
	Devices        []string  `json:"devices,omitempty"`
	MaxAmountMinor int64     `json:"max_amount_minor,omitempty"`
	WindowStart    time.Time `json:"window_start,omitempty"`
	WindowEnd      time.Time `json:"window_end,omitempty"`
}

// AuthContext is one authorisation attempt against a token.
type AuthContext struct {
	Merchant    string    `json:"merchant"`
	Country     string    `json:"country"`
	Channel     string    `json:"channel"`
	Device      string    `json:"device"`
	AmountMinor int64     `json:"amount_minor"`
	At          time.Time `json:"at"`
}

// PANToken is one issued PAN token. PANFingerprint is a one-way
// fingerprint of the PAN — the raw PAN is never stored.
type PANToken struct {
	ID             string       `json:"id"`
	PANFingerprint string       `json:"pan_fingerprint"`
	Status         IssuerStatus `json:"status"`
	Scope          Scope        `json:"scope"`
	ExpiresAt      time.Time    `json:"expires_at,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
	PrevID         string       `json:"prev_id,omitempty"`
	NextID         string       `json:"next_id,omitempty"`
	Dependents     []string     `json:"dependents,omitempty"`
}

// IssuerVault is the mutex-guarded PAN-token store.
type IssuerVault struct {
	mu     sync.RWMutex
	tokens map[string]*PANToken
	logger zerolog.Logger
}

// NewIssuerVault returns an empty vault.
func NewIssuerVault(logger zerolog.Logger) *IssuerVault {
	return &IssuerVault{tokens: make(map[string]*PANToken), logger: logger}
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyScope(s Scope) Scope {
	return Scope{
		Merchants:      copyStrings(s.Merchants),
		Countries:      copyStrings(s.Countries),
		Channels:       copyStrings(s.Channels),
		Devices:        copyStrings(s.Devices),
		MaxAmountMinor: s.MaxAmountMinor,
		WindowStart:    s.WindowStart,
		WindowEnd:      s.WindowEnd,
	}
}

func copyPANToken(t *PANToken) *PANToken {
	cp := *t
	cp.Scope = copyScope(t.Scope)
	cp.Dependents = copyStrings(t.Dependents)
	return &cp
}

func allowedMove(from, to IssuerStatus) bool {
	for _, n := range legalIssuerMoves[from] {
		if n == to {
			return true
		}
	}
	return false
}

// Issue mints a new ACTIVE token for a PAN fingerprint.
func (v *IssuerVault) Issue(panFingerprint string, scope Scope, ttl time.Duration, now time.Time) (*PANToken, error) {
	if strings.TrimSpace(panFingerprint) == "" {
		return nil, fmt.Errorf("%w: pan fingerprint is required", ErrIssuerInvalidInput)
	}
	if scope.MaxAmountMinor < 0 {
		return nil, fmt.Errorf("%w: max_amount must be >= 0", ErrIssuerInvalidInput)
	}
	if !scope.WindowStart.IsZero() && !scope.WindowEnd.IsZero() && scope.WindowStart.After(scope.WindowEnd) {
		return nil, fmt.Errorf("%w: time window start after end", ErrIssuerInvalidInput)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	t := &PANToken{
		ID:             "ctok-" + uuid.NewString(),
		PANFingerprint: panFingerprint,
		Status:         IssuerStatusActive,
		Scope:          copyScope(scope),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if ttl > 0 {
		t.ExpiresAt = now.Add(ttl)
	}
	v.mu.Lock()
	v.tokens[t.ID] = t
	v.mu.Unlock()
	v.logger.Info().Str("token_id", t.ID).Str("status", string(t.Status)).Msg("card token issued")
	return copyPANToken(t), nil
}

// Get returns a copy of one token.
func (v *IssuerVault) Get(id string) (*PANToken, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	t, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	return copyPANToken(t), nil
}

// List returns copies of all tokens.
func (v *IssuerVault) List() []*PANToken {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]*PANToken, 0, len(v.tokens))
	for _, t := range v.tokens {
		out = append(out, copyPANToken(t))
	}
	return out
}

func (v *IssuerVault) transitionLocked(t *PANToken, to IssuerStatus, now time.Time) error {
	if !allowedMove(t.Status, to) {
		return fmt.Errorf("%w: %s -> %s", ErrIllegalIssuerMove, t.Status, to)
	}
	t.Status = to
	t.UpdatedAt = now
	return nil
}

// Suspend moves ACTIVE -> SUSPENDED.
func (v *IssuerVault) Suspend(id string, now time.Time) (*PANToken, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	if err := v.transitionLocked(t, IssuerStatusSuspended, now); err != nil {
		return nil, err
	}
	v.logger.Info().Str("token_id", id).Msg("card token suspended")
	return copyPANToken(t), nil
}

// Resume moves SUSPENDED -> ACTIVE.
func (v *IssuerVault) Resume(id string, now time.Time) (*PANToken, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	if err := v.transitionLocked(t, IssuerStatusActive, now); err != nil {
		return nil, err
	}
	v.logger.Info().Str("token_id", id).Msg("card token resumed")
	return copyPANToken(t), nil
}

// Revoke moves ACTIVE/SUSPENDED/ROTATING -> REVOKED.
func (v *IssuerVault) Revoke(id string, now time.Time) (*PANToken, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	if err := v.transitionLocked(t, IssuerStatusRevoked, now); err != nil {
		return nil, err
	}
	v.logger.Info().Str("token_id", id).Msg("card token revoked")
	return copyPANToken(t), nil
}

// AddDependent attaches a dependent (e.g. merchant credential-on-file
// reference) to a live token. Dependents are what rotation remaps.
func (v *IssuerVault) AddDependent(id, dependent string, now time.Time) (*PANToken, error) {
	if strings.TrimSpace(dependent) == "" {
		return nil, fmt.Errorf("%w: dependent is required", ErrIssuerInvalidInput)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	if t.Status == IssuerStatusRevoked || t.Status == IssuerStatusExpired {
		return nil, fmt.Errorf("%w: cannot attach dependents to %s token", ErrIllegalIssuerMove, t.Status)
	}
	for _, d := range t.Dependents {
		if d == dependent {
			return copyPANToken(t), nil
		}
	}
	t.Dependents = append(t.Dependents, dependent)
	t.UpdatedAt = now
	v.logger.Info().Str("token_id", id).Str("dependent", dependent).Msg("card token dependent attached")
	return copyPANToken(t), nil
}

// Rotate moves old ACTIVE/SUSPENDED -> ROTATING and mints a successor
// ACTIVE token carrying the same PAN fingerprint and scope. Dependents
// are remapped atomically under the same lock: on success the old token
// holds none and the new token holds all.
func (v *IssuerVault) Rotate(id string, now time.Time) (*PANToken, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	old, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	if err := v.transitionLocked(old, IssuerStatusRotating, now); err != nil {
		return nil, err
	}
	next := &PANToken{
		ID:             "ctok-" + uuid.NewString(),
		PANFingerprint: old.PANFingerprint,
		Status:         IssuerStatusActive,
		Scope:          copyScope(old.Scope),
		ExpiresAt:      old.ExpiresAt,
		CreatedAt:      now,
		UpdatedAt:      now,
		PrevID:         old.ID,
		Dependents:     copyStrings(old.Dependents),
	}
	old.NextID = next.ID
	old.Dependents = nil
	old.UpdatedAt = now
	v.tokens[next.ID] = next
	v.logger.Info().Str("old_token", old.ID).Str("new_token", next.ID).Int("dependents", len(next.Dependents)).Msg("card token rotated")
	return copyPANToken(next), nil
}

// SweepExpired marks every past-expiry ACTIVE/SUSPENDED/ROTATING token
// EXPIRED and returns how many were swept.
func (v *IssuerVault) SweepExpired(now time.Time) int {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for _, t := range v.tokens {
		if t.ExpiresAt.IsZero() || !now.After(t.ExpiresAt) {
			continue
		}
		if t.Status == IssuerStatusRevoked || t.Status == IssuerStatusExpired {
			continue
		}
		if !allowedMove(t.Status, IssuerStatusExpired) {
			continue
		}
		t.Status = IssuerStatusExpired
		t.UpdatedAt = now
		n++
		v.logger.Info().Str("token_id", t.ID).Msg("card token expired by sweep")
	}
	return n
}

// Authorize evaluates a token against an attempt context.
func (v *IssuerVault) Authorize(id string, ctx AuthContext) (Decision, string, error) {
	v.mu.RLock()
	t, ok := v.tokens[id]
	var cp *PANToken
	if ok {
		cp = copyPANToken(t)
	}
	v.mu.RUnlock()
	if !ok {
		return DecisionDeny, "", ErrIssuerTokenNotFound
	}
	if cp.Status != IssuerStatusActive {
		return DecisionDeny, fmt.Sprintf("token not active: %s", cp.Status), nil
	}
	if !cp.ExpiresAt.IsZero() {
		at := ctx.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		if at.After(cp.ExpiresAt) {
			return DecisionDeny, "token expired", nil
		}
	}
	if len(cp.Scope.Merchants) > 0 && !containsFold(cp.Scope.Merchants, ctx.Merchant) {
		return DecisionDeny, fmt.Sprintf("merchant %q not in token scope", ctx.Merchant), nil
	}
	if len(cp.Scope.Countries) > 0 && !containsFold(cp.Scope.Countries, ctx.Country) {
		return DecisionDeny, fmt.Sprintf("country %q not in token scope", ctx.Country), nil
	}
	if len(cp.Scope.Channels) > 0 && !containsFold(cp.Scope.Channels, ctx.Channel) {
		return DecisionDeny, fmt.Sprintf("channel %q not in token scope", ctx.Channel), nil
	}
	if len(cp.Scope.Devices) > 0 && !containsFold(cp.Scope.Devices, ctx.Device) {
		return DecisionDeny, fmt.Sprintf("device %q not in token scope", ctx.Device), nil
	}
	if cp.Scope.MaxAmountMinor > 0 && ctx.AmountMinor > cp.Scope.MaxAmountMinor {
		return DecisionDeny, fmt.Sprintf("amount %d exceeds token max %d", ctx.AmountMinor, cp.Scope.MaxAmountMinor), nil
	}
	if !cp.Scope.WindowStart.IsZero() || !cp.Scope.WindowEnd.IsZero() {
		at := ctx.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		if !cp.Scope.WindowStart.IsZero() && at.Before(cp.Scope.WindowStart) {
			return DecisionDeny, "token not yet valid for this time window", nil
		}
		if !cp.Scope.WindowEnd.IsZero() && at.After(cp.Scope.WindowEnd) {
			return DecisionDeny, "token time window expired", nil
		}
	}
	return DecisionAllow, "within token scope", nil
}

// NarrowScope replaces a token scope with a narrower one. Any widening
// (extra merchant/country/channel/device, higher max amount, wider time
// window, or dropping a constraint) is rejected and requires reissue.
func (v *IssuerVault) NarrowScope(id string, next Scope, now time.Time) (*PANToken, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if next.MaxAmountMinor < 0 {
		return nil, fmt.Errorf("%w: max_amount must be >= 0", ErrIssuerInvalidInput)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.tokens[id]
	if !ok {
		return nil, ErrIssuerTokenNotFound
	}
	if t.Status == IssuerStatusRevoked || t.Status == IssuerStatusExpired {
		return nil, fmt.Errorf("%w: cannot rescope %s token", ErrIllegalIssuerMove, t.Status)
	}
	if err := checkNarrow(t.Scope, next); err != nil {
		return nil, err
	}
	t.Scope = copyScope(next)
	t.UpdatedAt = now
	v.logger.Info().Str("token_id", id).Msg("card token scope narrowed")
	return copyPANToken(t), nil
}

func checkNarrow(cur, next Scope) error {
	if err := checkSubset("merchants", cur.Merchants, next.Merchants); err != nil {
		return err
	}
	if err := checkSubset("countries", cur.Countries, next.Countries); err != nil {
		return err
	}
	if err := checkSubset("channels", cur.Channels, next.Channels); err != nil {
		return err
	}
	if err := checkSubset("devices", cur.Devices, next.Devices); err != nil {
		return err
	}
	// Max amount: 0 means unlimited. Moving to 0 from a cap widens.
	if cur.MaxAmountMinor > 0 {
		if next.MaxAmountMinor == 0 {
			return fmt.Errorf("%w: max_amount 0 widens capped %d", ErrIssuerScopeWiden, cur.MaxAmountMinor)
		}
		if next.MaxAmountMinor > cur.MaxAmountMinor {
			return fmt.Errorf("%w: max_amount %d exceeds %d", ErrIssuerScopeWiden, next.MaxAmountMinor, cur.MaxAmountMinor)
		}
	}
	// Time window: dropping a bound widens; shrinking is narrowing.
	curHasStart := !cur.WindowStart.IsZero()
	curHasEnd := !cur.WindowEnd.IsZero()
	nextHasStart := !next.WindowStart.IsZero()
	nextHasEnd := !next.WindowEnd.IsZero()
	if curHasStart && !nextHasStart {
		return fmt.Errorf("%w: removing window start widens scope", ErrIssuerScopeWiden)
	}
	if curHasEnd && !nextHasEnd {
		return fmt.Errorf("%w: removing window end widens scope", ErrIssuerScopeWiden)
	}
	if curHasStart && nextHasStart && next.WindowStart.Before(cur.WindowStart) {
		return fmt.Errorf("%w: window start moves earlier", ErrIssuerScopeWiden)
	}
	if curHasEnd && nextHasEnd && next.WindowEnd.After(cur.WindowEnd) {
		return fmt.Errorf("%w: window end moves later", ErrIssuerScopeWiden)
	}
	return nil
}

func checkSubset(field string, cur, next []string) error {
	if len(cur) == 0 {
		return nil // unconstrained narrows to anything
	}
	if len(next) == 0 {
		return fmt.Errorf("%w: %s removes constraint", ErrIssuerScopeWiden, field)
	}
	for _, n := range next {
		if !containsFold(cur, n) {
			return fmt.Errorf("%w: %s %q not in current scope", ErrIssuerScopeWiden, field, n)
		}
	}
	return nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(s)) {
			return true
		}
	}
	return false
}
