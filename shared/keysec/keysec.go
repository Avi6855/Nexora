// Package keysec implements Nexora's key and secret security platform:
//
//  25. Key lifecycle: keys (encryption, signing, api_credential) move
//     CREATE -> ACTIVATE -> ROTATE -> REVOKE -> DESTROY, are versioned
//     per service, and ActiveVersion resolves through an explicit
//     active pointer (never "latest"), so restarts cannot re-point it.
//
//  26. Usage anomaly: a per-key rolling requests/day baseline alerts
//     when today exceeds N x baseline, with key/service/region/
//     operation context.
//
//  27. Secret scanner: a staged-secret detector over commit text
//     (api_key / private_key / customer_credential patterns) with a
//     contextual allowlist (example/test fixtures) returning
//     BLOCK/ALLOW plus findings.
//
//  28. Security-policy sim: a proposed policy evaluated against
//     synthetic traffic (new_device, new_region, new_login) reports
//     allowed/blocked/challenged counts before production.
package keysec

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned for unknown keys.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned for malformed requests.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict is returned for duplicate or illegal lifecycle transitions.
	ErrConflict = errors.New("conflict")
)

// ── 25. Key lifecycle ───────────────────────────────────────────────────

// Key types.
const (
	KeyTypeEncryption    = "encryption"
	KeyTypeSigning       = "signing"
	KeyTypeAPICredential = "api_credential"
)

// Key statuses.
const (
	StatusCreated   = "CREATED"
	StatusActive    = "ACTIVE"
	StatusRotated   = "ROTATED"
	StatusRevoked   = "REVOKED"
	StatusDestroyed = "DESTROYED"
)

// Verdicts shared by scanner and policy sim.
const (
	VerdictAllow     = "ALLOW"
	VerdictBlock     = "BLOCK"
	VerdictChallenge = "CHALLENGE"
)

func validKeyType(t string) bool {
	switch t {
	case KeyTypeEncryption, KeyTypeSigning, KeyTypeAPICredential:
		return true
	}
	return false
}

// Key is one versioned service key.
type Key struct {
	ID        string    `json:"id"`
	Service   string    `json:"service"`
	Type      string    `json:"type"`
	Version   int       `json:"version"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ── 26. Usage anomaly ───────────────────────────────────────────────────

// UsageContext carries the alert dimensions.
type UsageContext struct {
	Service   string `json:"service"`
	Region    string `json:"region"`
	Operation string `json:"operation"`
}

// Anomaly is one baseline-breach alert.
type Anomaly struct {
	KeyID      string    `json:"key_id"`
	Service    string    `json:"service"`
	Region     string    `json:"region"`
	Operation  string    `json:"operation"`
	Baseline   float64   `json:"baseline"`
	Today      int       `json:"today"`
	Multiplier float64   `json:"multiplier"`
	At         time.Time `json:"at"`
}

type keyUsage struct {
	days map[string]int // YYYY-MM-DD -> count
	ctx  UsageContext
}

// ── 27. Secret scanner ──────────────────────────────────────────────────

// Finding is one scanner hit.
type Finding struct {
	Kind    string `json:"kind"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

var (
	apiKeyRe = regexp.MustCompile(`(?i)\bapi[_-]?key\b\s*[:=]\s*['"]?([A-Za-z0-9_\-\.\/\+]{16,})['"]?`)
	// sk-live / AKIA style tokens also count as api keys.
	apiTokenRe     = regexp.MustCompile(`\b(sk-live-[A-Za-z0-9]{8,}|AKIA[0-9A-Z]{16}|xox[bap]-[A-Za-z0-9\-]{8,})`)
	privateKeyRe   = regexp.MustCompile(`-----BEGIN (?:RSA )?PRIVATE KEY-----`)
	customerCredRe = regexp.MustCompile(`(?i)\bcustomer[_-]?(?:credential|secret|token|password)\b\s*[:=]\s*['"]?([A-Za-z0-9_\-\.\/\+]{8,})['"]?`)
	genericAssign  = regexp.MustCompile(`(?i)\b(secret|passwd|password)\b\s*[:=]\s*['"]?([A-Za-z0-9_\-\.\/\+]{12,})['"]?`)
)

func isAllowlistedLine(line string) bool {
	l := strings.ToLower(line)
	for _, m := range []string{"example", "sample", "dummy", "placeholder", "fixture", "mock", "testdata", "documentation", "tutorial"} {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

func isPlaceholderValue(v string) bool {
	l := strings.ToLower(strings.TrimSpace(v))
	if l == "" {
		return true
	}
	// NOTE: "test" alone is not a placeholder marker: real vendor-style
	// test keys contain it. Fixture lines are allowlisted
	// by isAllowlistedLine; values only count when they are obviously
	// synthetic.
	for _, m := range []string{"example", "sample", "dummy", "placeholder", "fake", "xxx", "***", "changeme", "your-", "todo", "redacted"} {
		if strings.Contains(l, m) {
			return true
		}
	}
	// All-same-character redaction (xxx, ***).
	if len(l) >= 3 {
		same := true
		for i := 1; i < len(l); i++ {
			if l[i] != l[0] {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

func snippet(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 120 {
		return line[:120]
	}
	return line
}

// ScanCommit scans commit text (diff or message) for staged secrets.
// BLOCK with findings when a real secret pattern appears outside the
// contextual allowlist; otherwise ALLOW with no findings.
func ScanCommit(text string) (string, []Finding) {
	var findings []Finding
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if isAllowlistedLine(line) {
			continue
		}
		ln := i + 1
		if privateKeyRe.MatchString(line) {
			findings = append(findings, Finding{Kind: "private_key", Line: ln, Snippet: snippet(line)})
			continue
		}
		if m := apiKeyRe.FindStringSubmatch(line); m != nil {
			if !isPlaceholderValue(m[1]) {
				findings = append(findings, Finding{Kind: "api_key", Line: ln, Snippet: snippet(line)})
				continue
			}
		}
		if apiTokenRe.MatchString(line) {
			findings = append(findings, Finding{Kind: "api_key", Line: ln, Snippet: snippet(line)})
			continue
		}
		if m := customerCredRe.FindStringSubmatch(line); m != nil {
			if !isPlaceholderValue(m[1]) {
				findings = append(findings, Finding{Kind: "customer_credential", Line: ln, Snippet: snippet(line)})
				continue
			}
		}
		// Generic password assignments are only flagged when they look
		// like a real credential (long high-entropy value).
		if m := genericAssign.FindStringSubmatch(line); m != nil {
			if !isPlaceholderValue(m[2]) && len(m[2]) >= 16 {
				findings = append(findings, Finding{Kind: "customer_credential", Line: ln, Snippet: snippet(line)})
			}
		}
	}
	if len(findings) > 0 {
		return VerdictBlock, findings
	}
	return VerdictAllow, []Finding{}
}

// ── 28. Security-policy sim ─────────────────────────────────────────────

// Policy is a proposed authentication policy evaluated before production.
type Policy struct {
	Name                string `json:"name"`
	BlockNewRegion      bool   `json:"block_new_region"`
	ChallengeNewDevice  bool   `json:"challenge_new_device"`
	ChallengeNewLogin   bool   `json:"challenge_new_login"`
	BlockHighRiskRegion bool   `json:"block_high_risk_region"`
}

// TrafficEvent is one synthetic login.
type TrafficEvent struct {
	NewDevice bool `json:"new_device"`
	NewRegion bool `json:"new_region"`
	NewLogin  bool `json:"new_login"`
	HighRisk  bool `json:"high_risk_region"`
}

// SimulationResult counts policy outcomes over synthetic traffic.
type SimulationResult struct {
	Total      int `json:"total"`
	Allowed    int `json:"allowed"`
	Blocked    int `json:"blocked"`
	Challenged int `json:"challenged"`
}

// EvaluateOne applies a policy to one event: region blocks win,
// then device/login challenges, else ALLOW.
func EvaluateOne(p Policy, e TrafficEvent) string {
	if p.BlockNewRegion && e.NewRegion {
		return VerdictBlock
	}
	if p.BlockHighRiskRegion && e.HighRisk {
		return VerdictBlock
	}
	if p.ChallengeNewDevice && e.NewDevice {
		return VerdictChallenge
	}
	if p.ChallengeNewLogin && e.NewLogin {
		return VerdictChallenge
	}
	return VerdictAllow
}

// Simulate runs a proposed policy over synthetic traffic.
func Simulate(p Policy, traffic []TrafficEvent) SimulationResult {
	res := SimulationResult{Total: len(traffic)}
	for _, e := range traffic {
		switch EvaluateOne(p, e) {
		case VerdictBlock:
			res.Blocked++
		case VerdictChallenge:
			res.Challenged++
		default:
			res.Allowed++
		}
	}
	return res
}

// ── Store ───────────────────────────────────────────────────────────────

// Store is the mutex-guarded in-memory key lifecycle + usage engine.
// Scanner and policy sim are pure functions; the store owns keys,
// explicit active pointers and rolling usage baselines.
type Store struct {
	mu         sync.RWMutex
	seq        int
	keys       map[string]*Key
	serviceSeq map[string]int
	active     map[string]string // service -> active key id (explicit, never "latest")
	usage      map[string]*keyUsage
	threshold  float64
}

// NewStore builds an empty store with a 3x anomaly threshold.
func NewStore() *Store {
	return &Store{
		keys:       map[string]*Key{},
		serviceSeq: map[string]int{},
		active:     map[string]string{},
		usage:      map[string]*keyUsage{},
		threshold:  3,
	}
}

// SetThreshold sets the anomaly multiplier (must be > 1).
func (s *Store) SetThreshold(m float64) error {
	if m <= 1 {
		return fmt.Errorf("%w: threshold multiplier must exceed 1", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threshold = m
	return nil
}

// Threshold returns the current multiplier.
func (s *Store) Threshold() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.threshold
}

// CreateKey creates a versioned CREATED key for a service.
func (s *Store) CreateKey(service, keyType string) (*Key, error) {
	service = strings.TrimSpace(service)
	keyType = strings.ToLower(strings.TrimSpace(keyType))
	if service == "" {
		return nil, fmt.Errorf("%w: service is required", ErrInvalid)
	}
	if !validKeyType(keyType) {
		return nil, fmt.Errorf("%w: type must be one of encryption, signing, api_credential", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	s.serviceSeq[service]++
	now := time.Now().UTC()
	k := &Key{
		ID:        fmt.Sprintf("key-%d", s.seq),
		Service:   service,
		Type:      keyType,
		Version:   s.serviceSeq[service],
		Status:    StatusCreated,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.keys[k.ID] = k
	out := *k
	return &out, nil
}

// GetKey returns a copy of a key.
func (s *Store) GetKey(id string) (*Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[id]
	if !ok {
		return nil, fmt.Errorf("key %s: %w", id, ErrNotFound)
	}
	out := *k
	return &out, nil
}

// ActivateKey moves CREATED -> ACTIVE and points the service's explicit
// active pointer at it. The previous ACTIVE key becomes ROTATED.
func (s *Store) ActivateKey(id string) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return nil, fmt.Errorf("key %s: %w", id, ErrNotFound)
	}
	if k.Status == StatusDestroyed {
		return nil, fmt.Errorf("key %s is destroyed: %w", id, ErrConflict)
	}
	if k.Status == StatusRevoked {
		return nil, fmt.Errorf("revoked key %s cannot be activated: %w", id, ErrConflict)
	}
	if k.Status == StatusActive {
		out := *k
		return &out, nil
	}
	if k.Status != StatusCreated && k.Status != StatusRotated {
		return nil, fmt.Errorf("key %s in status %s cannot be activated: %w", id, k.Status, ErrConflict)
	}
	if prevID, ok := s.active[k.Service]; ok && prevID != k.ID {
		if prev, ok := s.keys[prevID]; ok && prev.Status == StatusActive {
			prev.Status = StatusRotated
			prev.UpdatedAt = time.Now().UTC()
		}
	}
	k.Status = StatusActive
	k.UpdatedAt = time.Now().UTC()
	s.active[k.Service] = k.ID
	out := *k
	return &out, nil
}

// RotateKey rotates the ACTIVE key: the old key becomes ROTATED and a
// successor version is created ACTIVE with the explicit pointer moved.
// Rotating a non-ACTIVE key conflicts.
func (s *Store) RotateKey(id string) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return nil, fmt.Errorf("key %s: %w", id, ErrNotFound)
	}
	if k.Status != StatusActive {
		return nil, fmt.Errorf("only an ACTIVE key can be rotated (key %s is %s): %w", id, k.Status, ErrConflict)
	}
	k.Status = StatusRotated
	k.UpdatedAt = time.Now().UTC()
	s.seq++
	s.serviceSeq[k.Service]++
	now := time.Now().UTC()
	next := &Key{
		ID:        fmt.Sprintf("key-%d", s.seq),
		Service:   k.Service,
		Type:      k.Type,
		Version:   s.serviceSeq[k.Service],
		Status:    StatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.keys[next.ID] = next
	s.active[k.Service] = next.ID
	out := *next
	return &out, nil
}

// RevokeKey moves ACTIVE/ROTATED/CREATED -> REVOKED, clearing the active
// pointer when the active key is revoked.
func (s *Store) RevokeKey(id string) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return nil, fmt.Errorf("key %s: %w", id, ErrNotFound)
	}
	switch k.Status {
	case StatusRevoked:
		out := *k
		return &out, nil
	case StatusDestroyed:
		return nil, fmt.Errorf("destroyed key %s cannot be revoked: %w", id, ErrConflict)
	case StatusActive, StatusRotated, StatusCreated:
		k.Status = StatusRevoked
		k.UpdatedAt = time.Now().UTC()
		if s.active[k.Service] == k.ID {
			delete(s.active, k.Service)
		}
		out := *k
		return &out, nil
	default:
		return nil, fmt.Errorf("key %s in status %s cannot be revoked: %w", id, k.Status, ErrConflict)
	}
}

// DestroyKey moves REVOKED -> DESTROYED. Only revoked keys may be destroyed.
func (s *Store) DestroyKey(id string) (*Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return nil, fmt.Errorf("key %s: %w", id, ErrNotFound)
	}
	if k.Status != StatusRevoked {
		return nil, fmt.Errorf("only a REVOKED key can be destroyed (key %s is %s): %w", id, k.Status, ErrConflict)
	}
	k.Status = StatusDestroyed
	k.UpdatedAt = time.Now().UTC()
	out := *k
	return &out, nil
}

// ActiveKey resolves the explicit active pointer for a service.
// It never falls back to "latest": without an activation there is no active key.
func (s *Store) ActiveKey(service string) (*Key, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.active[service]
	if !ok {
		return nil, fmt.Errorf("no active key for service %s: %w", service, ErrNotFound)
	}
	k, ok := s.keys[id]
	if !ok || k.Status != StatusActive {
		return nil, fmt.Errorf("no active key for service %s: %w", service, ErrNotFound)
	}
	out := *k
	return &out, nil
}

// ActiveVersion returns the version number of the active key.
func (s *Store) ActiveVersion(service string) (int, error) {
	k, err := s.ActiveKey(service)
	if err != nil {
		return 0, err
	}
	return k.Version, nil
}

// Snapshot exports keys plus the explicit active pointers so a restart
// can rebuild without "latest" ambiguity.
func (s *Store) Snapshot() ([]Key, map[string]string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]Key, 0, len(s.keys))
	for _, k := range s.keys {
		keys = append(keys, *k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].ID < keys[j].ID })
	active := map[string]string{}
	for svc, id := range s.active {
		active[svc] = id
	}
	return keys, active
}

// Restore rebuilds a store from a snapshot, preserving the explicit
// active pointers and per-service versions.
func Restore(keys []Key, active map[string]string) *Store {
	s := NewStore()
	for _, k := range keys {
		cp := k
		s.keys[k.ID] = &cp
		if cp.Version > s.serviceSeq[cp.Service] {
			s.serviceSeq[cp.Service] = cp.Version
		}
		// Keep global seq ahead of any numeric key-N id.
		var n int
		if _, err := fmt.Sscanf(k.ID, "key-%d", &n); err == nil && n > s.seq {
			s.seq = n
		}
	}
	for svc, id := range active {
		if k, ok := s.keys[id]; ok && k.Status == StatusActive && k.Service == svc {
			s.active[svc] = id
		}
	}
	return s
}

func dayKey(t time.Time) string { return t.UTC().Format("2006-01-02") }

// ObserveUsage records one day's request count with its context.
func (s *Store) ObserveUsage(keyID string, day time.Time, count int, service, region, operation string) error {
	if strings.TrimSpace(keyID) == "" {
		return fmt.Errorf("%w: key id is required", ErrInvalid)
	}
	if count < 0 {
		return fmt.Errorf("%w: count must be non-negative", ErrInvalid)
	}
	if day.IsZero() {
		day = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[keyID]; !ok {
		return fmt.Errorf("key %s: %w", keyID, ErrNotFound)
	}
	u, ok := s.usage[keyID]
	if !ok {
		u = &keyUsage{days: map[string]int{}}
		s.usage[keyID] = u
	}
	u.days[dayKey(day)] = count
	if service != "" || region != "" || operation != "" {
		u.ctx = UsageContext{Service: service, Region: region, Operation: operation}
	}
	return nil
}

// baselineOf averages up to the 7 days before today (exclusive).
func baselineOf(days map[string]int, today string) (float64, int) {
	var keys []string
	for d := range days {
		if d < today {
			keys = append(keys, d)
		}
	}
	sort.Strings(keys)
	if len(keys) > 7 {
		keys = keys[len(keys)-7:]
	}
	if len(keys) < 3 {
		return 0, len(keys)
	}
	sum := 0
	for _, k := range keys {
		sum += days[k]
	}
	return float64(sum) / float64(len(keys)), len(keys)
}

// Anomalies returns one alert per key whose latest day exceeds
// threshold x rolling baseline.
func (s *Store) Anomalies() []Anomaly {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []Anomaly
	for keyID, u := range s.usage {
		if len(u.days) == 0 {
			continue
		}
		today := ""
		for d := range u.days {
			if d > today {
				today = d
			}
		}
		base, n := baselineOf(u.days, today)
		if n < 3 || base <= 0 {
			continue
		}
		cur := u.days[today]
		if float64(cur) > s.threshold*base {
			at, _ := time.Parse("2006-01-02", today)
			out = append(out, Anomaly{
				KeyID: keyID, Service: u.ctx.Service, Region: u.ctx.Region, Operation: u.ctx.Operation,
				Baseline: base, Today: cur, Multiplier: s.threshold, At: at.UTC(),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].KeyID < out[j].KeyID })
	if out == nil {
		out = []Anomaly{}
	}
	return out
}
