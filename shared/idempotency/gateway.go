package idempotency

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// RequestState is the idempotent gateway lifecycle:
//
//	REQUESTED → PROCESSING → SUCCEEDED | FAILED
//	any state → EXPIRED (via TTL sweep) → re-execute
var (
	ErrConflictRetry   = errors.New("request is already processing; retry later")
	ErrRequestNotFound = errors.New("idempotency request not found")
	ErrInvalidState    = errors.New("invalid request state transition")
)

// Gateway states (distinct from the legacy Engine PENDING/COMPLETED/FAILED
// statuses, which are left untouched).
type RequestState string

const (
	StateRequested  RequestState = "REQUESTED"
	StateProcessing RequestState = "PROCESSING"
	StateSucceeded  RequestState = "SUCCEEDED"
	StateFailed     RequestState = "FAILED"
	StateExpired    RequestState = "EXPIRED"
)

// GatewayRequest is one idempotent request slot.
type GatewayRequest struct {
	Key         string       `json:"key"`
	Fingerprint string       `json:"fingerprint"`
	Method      string       `json:"method"`
	Path        string       `json:"path"`
	BodyHash    string       `json:"body_hash"`
	State       RequestState `json:"state"`
	Response    []byte       `json:"response,omitempty"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
	ExpiresAt   time.Time    `json:"expires_at"`
}

// RequestFingerprint binds method+path+body hash to the idempotency key so
// the same key with a different payload is a conflict, not a replay.
func RequestFingerprint(method, path string, body []byte, key string) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write(body)
	h.Write([]byte{0})
	h.Write([]byte(key))
	return hex.EncodeToString(h.Sum(nil))
}

// Gateway dedupes by key with TTL expiry. It is safe for concurrent use.
type Gateway struct {
	mu   sync.Mutex
	ttl  time.Duration
	reqs map[string]*GatewayRequest
	// Now is swappable in tests to simulate TTL expiry.
	Now func() time.Time
}

// NewGateway builds a gateway with the given entry TTL.
func NewGateway(ttl time.Duration) *Gateway {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Gateway{ttl: ttl, reqs: map[string]*GatewayRequest{}, Now: time.Now}
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

func copyRequest(r *GatewayRequest) *GatewayRequest {
	if r == nil {
		return nil
	}
	cp := *r
	if r.Response != nil {
		cp.Response = append([]byte(nil), r.Response...)
	}
	return &cp
}

// Request opens a REQUESTED slot. Re-requesting the same key+fingerprint
// returns the existing slot; a different fingerprint conflicts.
func (g *Gateway) Request(key, method, path string, body []byte) (*GatewayRequest, error) {
	if key == "" {
		return nil, ErrKeyRequired
	}
	fp := RequestFingerprint(method, path, body, key)
	bodyHash := HashRequest(body)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if existing, ok := g.reqs[key]; ok {
		if now.After(existing.ExpiresAt) {
			existing.State = StateExpired
			existing.UpdatedAt = now
		} else if existing.Fingerprint != fp {
			return copyRequest(existing), ErrConflict
		} else {
			return copyRequest(existing), nil
		}
		// Expired slot with a new fingerprint falls through and is replaced.
		if existing.Fingerprint != fp {
			// Allow reuse after expiry with a new fingerprint.
		} else {
			return copyRequest(existing), nil
		}
	}
	rec := &GatewayRequest{
		Key: key, Fingerprint: fp, Method: method, Path: path, BodyHash: bodyHash,
		State: StateRequested, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(g.ttl),
	}
	g.reqs[key] = rec
	return copyRequest(rec), nil
}

// StartProcessing moves REQUESTED → PROCESSING.
func (g *Gateway) StartProcessing(key string) (*GatewayRequest, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.reqs[key]
	if !ok {
		return nil, ErrRequestNotFound
	}
	if g.now().After(rec.ExpiresAt) {
		rec.State = StateExpired
		rec.UpdatedAt = g.now()
		return copyRequest(rec), ErrInvalidState
	}
	if rec.State != StateRequested {
		return copyRequest(rec), ErrInvalidState
	}
	rec.State = StateProcessing
	rec.UpdatedAt = g.now()
	return copyRequest(rec), nil
}

// Complete moves PROCESSING → SUCCEEDED with the stored response.
func (g *Gateway) Complete(key string, response []byte) (*GatewayRequest, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.reqs[key]
	if !ok {
		return nil, ErrRequestNotFound
	}
	if rec.State != StateProcessing {
		return copyRequest(rec), ErrInvalidState
	}
	rec.State = StateSucceeded
	rec.Response = append([]byte(nil), response...)
	rec.UpdatedAt = g.now()
	return copyRequest(rec), nil
}

// Fail moves PROCESSING → FAILED.
func (g *Gateway) Fail(key string) (*GatewayRequest, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.reqs[key]
	if !ok {
		return nil, ErrRequestNotFound
	}
	if rec.State != StateProcessing {
		return copyRequest(rec), ErrInvalidState
	}
	rec.State = StateFailed
	rec.UpdatedAt = g.now()
	return copyRequest(rec), nil
}

// Get returns a copy of the slot.
func (g *Gateway) Get(key string) (*GatewayRequest, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.reqs[key]
	if !ok {
		return nil, ErrRecordNotFound
	}
	return copyRequest(rec), nil
}

// Sweep marks TTL-expired slots EXPIRED and returns how many transitioned.
func (g *Gateway) Sweep() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	n := 0
	for _, rec := range g.reqs {
		if rec.State != StateExpired && now.After(rec.ExpiresAt) {
			rec.State = StateExpired
			rec.UpdatedAt = now
			n++
		}
	}
	return n
}

// Execute dedupes by key:
//
//	PROCESSING/REQUESTED → ErrConflictRetry (409 conflict-retry)
//	SUCCEEDED (same fingerprint) → replay stored response (replayed=true)
//	FAILED/EXPIRED → re-execute fn
//	mismatched fingerprint (non-expired) → ErrConflict
//
// On success the slot ends SUCCEEDED; on fn error it ends FAILED.
func (g *Gateway) Execute(key, method, path string, body []byte, fn func() ([]byte, error)) ([]byte, bool, error) {
	if key == "" {
		return nil, false, ErrKeyRequired
	}
	fp := RequestFingerprint(method, path, body, key)
	bodyHash := HashRequest(body)

	g.mu.Lock()
	now := g.now()
	if existing, ok := g.reqs[key]; ok {
		if now.After(existing.ExpiresAt) && existing.State != StateExpired {
			existing.State = StateExpired
			existing.UpdatedAt = now
		}
		switch existing.State {
		case StateSucceeded:
			if existing.Fingerprint != fp {
				cp := copyRequest(existing)
				_ = cp
				g.mu.Unlock()
				return nil, false, ErrConflict
			}
			resp := append([]byte(nil), existing.Response...)
			g.mu.Unlock()
			return resp, true, nil
		case StateRequested, StateProcessing:
			if existing.Fingerprint != fp {
				g.mu.Unlock()
				return nil, false, ErrConflict
			}
			g.mu.Unlock()
			return nil, false, ErrConflictRetry
		case StateFailed, StateExpired:
			// Re-execute below (refresh fingerprint + PROCESSING).
			existing.Fingerprint = fp
			existing.Method = method
			existing.Path = path
			existing.BodyHash = bodyHash
			existing.State = StateProcessing
			existing.Response = nil
			existing.UpdatedAt = now
			existing.ExpiresAt = now.Add(g.ttl)
			g.mu.Unlock()
			resp, err := fn()
			g.mu.Lock()
			rec := g.reqs[key]
			if err != nil {
				rec.State = StateFailed
				rec.UpdatedAt = g.now()
				g.mu.Unlock()
				return nil, false, err
			}
			rec.State = StateSucceeded
			rec.Response = append([]byte(nil), resp...)
			rec.UpdatedAt = g.now()
			g.mu.Unlock()
			return append([]byte(nil), resp...), false, nil
		}
	}
	// Fresh slot: REQUESTED → PROCESSING, then run fn without holding the lock.
	rec := &GatewayRequest{
		Key: key, Fingerprint: fp, Method: method, Path: path, BodyHash: bodyHash,
		State: StateProcessing, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(g.ttl),
	}
	g.reqs[key] = rec
	g.mu.Unlock()

	resp, err := fn()
	g.mu.Lock()
	defer g.mu.Unlock()
	stored := g.reqs[key]
	if err != nil {
		stored.State = StateFailed
		stored.UpdatedAt = g.now()
		return nil, false, err
	}
	stored.State = StateSucceeded
	stored.Response = append([]byte(nil), resp...)
	stored.UpdatedAt = g.now()
	return append([]byte(nil), resp...), false, nil
}
