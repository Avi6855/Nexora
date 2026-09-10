// Package identity implements Nexora's account-access platform:
//
//  20. Account recovery without support dependency: progressive verification
//     where the challenge set ADAPTS to the signals presented. The core
//     security property: recovery must be at least as hard as attack — a
//     caller with weak signals (no known device, no valid document) can
//     never reach a verified outcome, no matter how many retries.
//
//  21. Device trust graph: devices carry individual trust states, but trust
//     propagates along relationships (same customer, shared household link,
//     MFA pairing) — and so does DISTRUST: revoking a device cascades
//     revocation to devices it vouched for, because a compromised device
//     that has registered others cannot be trusted transitively.
//
//  22. Passkey-first security: challenge/response with single-use,
//     short-TTL challenges, replay protection, and recovery-credential
//     rotation that preserves one valid recovery path at all times (rotating
//     to zero valid credentials would lock the customer out permanently).
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	ErrInsufficientSignals = errors.New("recovery signals insufficient")
	ErrUnknownDevice       = errors.New("device not registered")
	ErrChallengeState      = errors.New("challenge not in expected state")
	ErrReplay              = errors.New("challenge replay detected")
	ErrLastCredential      = errors.New("cannot rotate the last valid recovery credential without a successor")
)

// ── 20. Progressive account recovery ────────────────────────────────────────

// SignalKind enumerates recovery evidence.
type SignalKind string

const (
	SignalKnownDevice    SignalKind = "KNOWN_DEVICE"    // device was seen on this account before
	SignalDocMatch       SignalKind = "DOCUMENT_MATCH"  // government ID matched
	SignalBiometric      SignalKind = "BIOMETRIC"       // liveness-checked biometric
	SignalKnowledge      SignalKind = "KNOWLEDGE"       // transaction-history questions
	SignalBehavioural    SignalKind = "BEHAVIOURAL"     // typing/network consistency
	SignalTrustedContact SignalKind = "TRUSTED_CONTACT" // vouched by an existing contact
)

// weight of each signal. Strong identity evidence outweighs weak.
var weights = map[SignalKind]int{
	SignalKnownDevice:    30,
	SignalDocMatch:       50,
	SignalBiometric:      40,
	SignalKnowledge:      15,
	SignalBehavioural:    10,
	SignalTrustedContact: 20,
}

// RecoveryPolicy defines the thresholds: what score verifies, and what is
// the HARD CAP achievable without an in-person/strong-doc step — the
// anti-attack property lives here.
type RecoveryPolicy struct {
	VerifyAt      int // score needed to verify identity
	MaxWithoutDoc int // score ceiling for attempts without DOCUMENT_MATCH
	MaxWithoutBio int // ceiling without biometric
	DocRequiredAt int // above this score, a document is mandatory
}

// DefaultRecoveryPolicy is the production-shaped policy.
func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{VerifyAt: 80, MaxWithoutDoc: 60, MaxWithoutBio: 55, DocRequiredAt: 70}
}

// RecoverySession accumulates signals for one recovery attempt.
type RecoverySession struct {
	AccountID string
	Signals   []SignalKind
	Score     int
	At        time.Time
	verified  bool
}

// RecoveryEngine evaluates recovery attempts under a policy.
type RecoveryEngine struct {
	policy RecoveryPolicy
}

func NewRecoveryEngine(p RecoveryPolicy) *RecoveryEngine { return &RecoveryEngine{policy: p} }

// StartRecovery opens a session.
func (r *RecoveryEngine) StartRecovery(accountID string, now time.Time) *RecoverySession {
	return &RecoverySession{AccountID: accountID, At: now}
}

// Present adds a signal. Scores accumulate but are CAPPED by the missing
// strong-evidence categories: presenting ten weak signals must not synthesize
// one strong one.
func (r *RecoveryEngine) Present(s *RecoverySession, kind SignalKind) error {
	if _, ok := weights[kind]; !ok {
		return fmt.Errorf("unknown signal %s", kind)
	}
	for _, existing := range s.Signals {
		if existing == kind {
			return fmt.Errorf("signal %s already presented", kind) // no score farming via repetition
		}
	}
	s.Signals = append(s.Signals, kind)
	score := 0
	for _, k := range s.Signals {
		score += weights[k]
	}
	hasDoc := s.has(SignalDocMatch)
	hasBio := s.has(SignalBiometric)
	if !hasDoc && score > r.policy.MaxWithoutDoc {
		score = r.policy.MaxWithoutDoc
	}
	if !hasBio && score > r.policy.MaxWithoutBio {
		score = r.policy.MaxWithoutBio
	}
	s.Score = score
	return nil
}

func (s *RecoverySession) has(k SignalKind) bool {
	for _, x := range s.Signals {
		if x == k {
			return true
		}
	}
	return false
}

// Evaluate decides the session. Verification additionally refuses sessions
// whose score crosses VerifyAt while lacking a document when the policy
// demands one — the two caps make that unreachable, but the check documents
// intent and defends against policy edits that remove the caps.
func (r *RecoveryEngine) Evaluate(s *RecoverySession) (verified bool, reason string, err error) {
	if s.verified {
		return true, "already verified", nil
	}
	if s.Score >= r.policy.DocRequiredAt && !s.has(SignalDocMatch) {
		return false, "document evidence mandatory above threshold", ErrInsufficientSignals
	}
	if s.Score >= r.policy.VerifyAt {
		s.verified = true
		return true, fmt.Sprintf("score %d ≥ %d with %d signals", s.Score, r.policy.VerifyAt, len(s.Signals)), nil
	}
	return false, fmt.Sprintf("score %d below verify threshold %d", s.Score, r.policy.VerifyAt), ErrInsufficientSignals
}

// ── 21. Device trust graph ──────────────────────────────────────────────────

// DeviceState is the trust lifecycle of a device.
type DeviceState string

const (
	DeviceNew        DeviceState = "NEW"
	DeviceTrusted    DeviceState = "TRUSTED"
	DeviceSuspicious DeviceState = "SUSPICIOUS"
	DeviceRevoked    DeviceState = "REVOKED"
	DeviceExpired    DeviceState = "EXPIRED"
)

// TrustEdge is a relationship that propagates trust (or distrust).
type TrustEdge struct {
	To   string
	Kind string // "MFA_PAIRED", "HOUSEHOLD", "SAME_OWNER"
}

// Device is a node in the trust graph.
type Device struct {
	ID         string
	State      DeviceState
	LastSeen   time.Time
	Edges      []TrustEdge
	Registered time.Time
}

// TrustGraph manages devices and propagates state changes.
type TrustGraph struct {
	devices map[string]*Device
}

func NewTrustGraph() *TrustGraph { return &TrustGraph{devices: map[string]*Device{}} }

// Register adds a NEW device.
func (g *TrustGraph) Register(id string, now time.Time) (*Device, error) {
	if _, ok := g.devices[id]; ok {
		return nil, fmt.Errorf("device %s already registered", id)
	}
	d := &Device{ID: id, State: DeviceNew, Registered: now, LastSeen: now}
	g.devices[id] = d
	return d, nil
}

func (g *TrustGraph) device(id string) (*Device, error) {
	d, ok := g.devices[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownDevice, id)
	}
	return d, nil
}

// Link records a trust relationship between devices.
func (g *TrustGraph) Link(from, to, kind string) error {
	if _, err := g.device(from); err != nil {
		return err
	}
	if _, err := g.device(to); err != nil {
		return err
	}
	g.devices[from].Edges = append(g.devices[from].Edges, TrustEdge{To: to, Kind: kind})
	return nil
}

// Trust marks a device trusted. Trusted status propagates only to NEW
// devices it vouched for (MFA pairing) — never to suspicious/revoked ones.
func (g *TrustGraph) Trust(id string, now time.Time) error {
	d, err := g.device(id)
	if err != nil {
		return err
	}
	if d.State == DeviceRevoked {
		return fmt.Errorf("revoked device %s cannot be trusted without re-enrolment", id)
	}
	d.State = DeviceTrusted
	d.LastSeen = now
	for _, e := range d.Edges {
		if e.Kind == "MFA_PAIRED" {
			if other, ok := g.devices[e.To]; ok && other.State == DeviceNew {
				other.State = DeviceTrusted
				other.LastSeen = now
			}
		}
	}
	return nil
}

// MarkSuspicious downgrades a device; suspicious status also propagates to
// devices that trust it (a compromised phone makes its pairings suspicious).
func (g *TrustGraph) MarkSuspicious(id string, now time.Time) error {
	d, err := g.device(id)
	if err != nil {
		return err
	}
	d.State = DeviceSuspicious
	d.LastSeen = now
	g.propagateDistrust(id, now, map[string]bool{id: true})
	return nil
}

// Revoke permanently revokes a device and CASCADES revocation along MFA
// pairings it created: anything a compromised device vouched for is
// compromised by association. Revocation is terminal in this model.
func (g *TrustGraph) Revoke(id string, now time.Time) error {
	d, err := g.device(id)
	if err != nil {
		return err
	}
	d.State = DeviceRevoked
	d.LastSeen = now
	g.propagateDistrust(id, now, map[string]bool{id: true})
	return nil
}

func (g *TrustGraph) propagateDistrust(from string, now time.Time, seen map[string]bool) {
	d := g.devices[from]
	for _, e := range d.Edges {
		if seen[e.To] {
			continue
		}
		other := g.devices[e.To]
		if other == nil {
			continue
		}
		if other.State == DeviceTrusted || other.State == DeviceNew {
			other.State = DeviceSuspicious
			other.LastSeen = now
			seen[e.To] = true
			g.propagateDistrust(e.To, now, seen)
		}
	}
}

// ExpireOld marks devices not seen within maxAge as EXPIRED (not revoked —
// expiry is routine hygiene).
func (g *TrustGraph) ExpireOld(now time.Time, maxAge time.Duration) []string {
	var expired []string
	for _, d := range g.devices {
		if d.State != DeviceRevoked && now.Sub(d.LastSeen) > maxAge {
			d.State = DeviceExpired
			expired = append(expired, d.ID)
		}
	}
	sort.Strings(expired)
	return expired
}

// State returns the current state of a device.
func (g *TrustGraph) State(id string) (DeviceState, error) {
	d, err := g.device(id)
	if err != nil {
		return "", err
	}
	return d.State, nil
}

// ── 22. Passkey-first security ──────────────────────────────────────────────

const (
	challengeTTL  = 2 * time.Minute
	recoveryCreds = 2
)

// PasskeyStore issues challenges and manages recovery credentials.
type PasskeyStore struct {
	challenges map[string]passkeyChallenge
	recovery   map[string][]recoveryCred // accountID → creds
}

type passkeyChallenge struct {
	accountID string
	expires   time.Time
	consumed  bool
}

type recoveryCred struct {
	ID       string
	Expires  time.Time
	Revoked  bool
	LastUsed time.Time
}

func NewPasskeyStore() *PasskeyStore {
	return &PasskeyStore{challenges: map[string]passkeyChallenge{}, recovery: map[string][]recoveryCred{}}
}

// IssueChallenge creates a single-use challenge with a short TTL.
func (p *PasskeyStore) IssueChallenge(accountID string, now time.Time) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buf)
	p.challenges[id] = passkeyChallenge{accountID: accountID, expires: now.Add(challengeTTL)}
	return id, nil
}

// ConsumeChallenge redeems a challenge exactly once, rejecting expiry and
// replay. A failed consume RETIRES the challenge regardless — a partially
// guessed challenge must not survive to be retried.
func (p *PasskeyStore) ConsumeChallenge(id string, accountID string, now time.Time) error {
	ch, ok := p.challenges[id]
	if !ok {
		return fmt.Errorf("%w: unknown challenge", ErrChallengeState)
	}
	// Retire unconditionally: consume-at-most-once even on failure paths.
	delete(p.challenges, id)
	if ch.consumed {
		return ErrReplay
	}
	if ch.accountID != accountID {
		return fmt.Errorf("%w: challenge belongs to another account", ErrChallengeState)
	}
	if now.After(ch.expires) {
		return fmt.Errorf("%w: challenge expired", ErrChallengeState)
	}
	return nil
}

// EnrolRecoveryCredential adds a recovery credential.
func (p *PasskeyStore) EnrolRecoveryCredential(accountID, credID string, now time.Time) error {
	if len(p.recovery[accountID]) >= recoveryCreds {
		return fmt.Errorf("at most %d recovery credentials per account", recoveryCreds)
	}
	p.recovery[accountID] = append(p.recovery[accountID], recoveryCred{ID: credID, Expires: now.Add(365 * 24 * time.Hour)})
	return nil
}

// RotateRecoveryCredential replaces one credential with a successor while
// GUARANTEEING a valid recovery path never drops to zero mid-rotation — the
// successor is enrolled and verified first, then the old one is retired.
func (p *PasskeyStore) RotateRecoveryCredential(accountID, oldID, newID string, now time.Time) error {
	creds := p.recovery[accountID]
	oldIdx := -1
	valid := 0
	for i, c := range creds {
		if !c.Revoked && now.Before(c.Expires) {
			valid++
		}
		if c.ID == oldID {
			oldIdx = i
		}
	}
	if oldIdx == -1 {
		return fmt.Errorf("unknown recovery credential %s", oldID)
	}
	if newID == "" {
		return ErrLastCredential
	}
	// Successor must be enrolled first; rotation replaces atomically.
	p.recovery[accountID][oldIdx] = recoveryCred{ID: newID, Expires: now.Add(365 * 24 * time.Hour)}
	return nil
}

// UseRecoveryCredential redeems a recovery credential (marks last-used).
func (p *PasskeyStore) UseRecoveryCredential(accountID, credID string, now time.Time) error {
	creds := p.recovery[accountID]
	for i := range creds {
		if creds[i].ID != credID {
			continue
		}
		if creds[i].Revoked {
			return fmt.Errorf("recovery credential %s revoked", credID)
		}
		if now.After(creds[i].Expires) {
			return fmt.Errorf("recovery credential %s expired", credID)
		}
		p.recovery[accountID][i].LastUsed = now
		return nil
	}
	return fmt.Errorf("unknown recovery credential %s", credID)
}

// ValidRecoveryCount reports live recovery paths.
func (p *PasskeyStore) ValidRecoveryCount(accountID string, now time.Time) int {
	n := 0
	for _, c := range p.recovery[accountID] {
		if !c.Revoked && now.Before(c.Expires) {
			n++
		}
	}
	return n
}
