// Package offline implements Nexora's offline-first intents + conflict
// resolution as a tested shared library.
//
//  1. IntentStore: a device registers a per-device secret, then queues signed
//     intents {request_id, account, amount, payee, seq, prev_hash,
//     signature}. The payload is stored as an opaque encrypted blob (bytes
//     here — callers encrypt before queueing). On reconnect Sync validates
//     each intent in seq order: HMAC signature, monotonic seq per device
//     (old seq rejected), idempotent commit by request_id (double submit
//     returns DUPLICATE with the original result), and hash-chain continuity
//     against newer server state (mismatch returns HELD).
//
//  2. Resolver: versioned ops {entity, base_version, op, actor_device,
//     lamport_ts} resolve to MERGE (field-level union of metadata maps),
//     REJECT (stale delete vs newer write), RETRY (concurrent increments) or
//     MANUAL_REVIEW (delete vs update).
package offline

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrDeviceExists marks duplicate device registration with a different secret.
	ErrDeviceExists = errors.New("device already registered")
	// ErrUnknownDevice marks operations for an unregistered device.
	ErrUnknownDevice = errors.New("unknown device")
)

// IntentStatus is the per-intent Sync outcome.
type IntentStatus string

const (
	StatusCommitted IntentStatus = "COMMITTED"
	StatusRejected  IntentStatus = "REJECTED"
	StatusHeld      IntentStatus = "HELD"
	StatusDuplicate IntentStatus = "DUPLICATE"
)

// SignedIntent is the client-created offline intent. Blob carries the
// encrypted payload opaquely; the server never inspects it.
type SignedIntent struct {
	RequestID string `json:"request_id"`
	Account   string `json:"account"`
	Amount    int64  `json:"amount"`
	Payee     string `json:"payee"`
	Seq       uint64 `json:"seq"`
	PrevHash  string `json:"prev_hash"`
	Signature string `json:"signature"`
	Blob      []byte `json:"blob,omitempty"`
}

// SyncResult is the per-intent outcome of Sync.
type SyncResult struct {
	RequestID string       `json:"request_id"`
	Seq       uint64       `json:"seq"`
	Status    IntentStatus `json:"status"`
	Reason    string       `json:"reason,omitempty"`
}

// device holds per-device sync state.
type device struct {
	ID      string
	Secret  []byte
	LastSeq uint64
	Head    string
}

// queuedIntent is one stored intent in arrival order.
type queuedIntent struct {
	DeviceID string
	Intent   SignedIntent
}

// IntentStore queues offline intents and commits them idempotently on sync.
type IntentStore struct {
	mu        sync.Mutex
	devices   map[string]*device
	queue     []queuedIntent
	committed map[string]*SyncResult
}

// NewIntentStore returns an empty store.
func NewIntentStore() *IntentStore {
	return &IntentStore{
		devices:   make(map[string]*device),
		committed: make(map[string]*SyncResult),
	}
}

// RegisterDevice enrols a device with its per-device HMAC secret. Re-registering
// the same device with the same secret is idempotent; a different secret
// returns ErrDeviceExists.
func (s *IntentStore) RegisterDevice(deviceID, secret string) error {
	if deviceID == "" || secret == "" {
		return fmt.Errorf("device_id and secret are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.devices[deviceID]; ok {
		if string(d.Secret) == secret {
			return nil
		}
		return ErrDeviceExists
	}
	s.devices[deviceID] = &device{ID: deviceID, Secret: []byte(secret)}
	return nil
}

// QueueIntent stores an intent blob for later sync. Queueing is intentionally
// permissive: signatures, seq order and conflicts are all evaluated at Sync
// time so offline clients can always make progress.
func (s *IntentStore) QueueIntent(deviceID string, intent SignedIntent) error {
	if deviceID == "" {
		return fmt.Errorf("device_id is required")
	}
	if intent.RequestID == "" {
		return fmt.Errorf("request_id is required")
	}
	if intent.Seq == 0 {
		return fmt.Errorf("seq must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return ErrUnknownDevice
	}
	s.queue = append(s.queue, queuedIntent{DeviceID: deviceID, Intent: intent})
	return nil
}

// SignIntent computes the HMAC-SHA256 signature for an intent. Clients sign
// with their per-device secret; the server recomputes the same value.
func SignIntent(secret string, intent SignedIntent) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s|%s|%d|%s|%d|%s",
		intent.RequestID, intent.Account, intent.Amount, intent.Payee, intent.Seq, intent.PrevHash)
	return hex.EncodeToString(mac.Sum(nil))
}

// ChainHash advances the per-device hash chain after a commit.
func ChainHash(prev string, intent SignedIntent) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d", prev, intent.RequestID, intent.Seq)
	return hex.EncodeToString(h.Sum(nil))
}

func verifySignature(secret []byte, intent SignedIntent) bool {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%s|%s|%d|%s|%d|%s",
		intent.RequestID, intent.Account, intent.Amount, intent.Payee, intent.Seq, intent.PrevHash)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(intent.Signature))
}

// Sync processes the queued intents for one device in seq order, returning one
// result per intent: COMMITTED (signature + seq + chain valid), DUPLICATE
// (request_id already committed — original result), REJECTED (bad signature
// or old seq), or HELD (prev_hash mismatch vs newer server state).
func (s *IntentStore) Sync(deviceID string) ([]SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[deviceID]
	if !ok {
		return nil, ErrUnknownDevice
	}
	var pending []queuedIntent
	var rest []queuedIntent
	for _, q := range s.queue {
		if q.DeviceID == deviceID {
			pending = append(pending, q)
		} else {
			rest = append(rest, q)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Intent.Seq < pending[j].Intent.Seq })

	results := make([]SyncResult, 0, len(pending))
	for _, q := range pending {
		in := q.Intent
		// Idempotent commit: double submit returns the original outcome.
		if prev, done := s.committed[in.RequestID]; done {
			results = append(results, SyncResult{
				RequestID: in.RequestID, Seq: in.Seq,
				Status: StatusDuplicate, Reason: fmt.Sprintf("request %s already %s", in.RequestID, prev.Status),
			})
			continue
		}
		if !verifySignature(d.Secret, in) {
			r := &SyncResult{RequestID: in.RequestID, Seq: in.Seq, Status: StatusRejected, Reason: "invalid signature"}
			s.committed[in.RequestID] = &SyncResult{RequestID: in.RequestID, Seq: in.Seq, Status: StatusRejected}
			results = append(results, *r)
			continue
		}
		if in.Seq <= d.LastSeq {
			r := &SyncResult{RequestID: in.RequestID, Seq: in.Seq, Status: StatusRejected, Reason: fmt.Sprintf("stale seq %d <= committed %d", in.Seq, d.LastSeq)}
			s.committed[in.RequestID] = &SyncResult{RequestID: in.RequestID, Seq: in.Seq, Status: StatusRejected}
			results = append(results, *r)
			continue
		}
		if in.PrevHash != d.Head {
			results = append(results, SyncResult{
				RequestID: in.RequestID, Seq: in.Seq,
				Status: StatusHeld, Reason: "conflict with newer server state",
			})
			continue
		}
		d.LastSeq = in.Seq
		d.Head = ChainHash(d.Head, in)
		s.committed[in.RequestID] = &SyncResult{RequestID: in.RequestID, Seq: in.Seq, Status: StatusCommitted}
		results = append(results, SyncResult{RequestID: in.RequestID, Seq: in.Seq, Status: StatusCommitted})
	}
	s.queue = rest
	// HELD intents stay out of the committed map so a client refresh + retry
	// can commit them later; they are drained from this device's queue.
	return results, nil
}

// Pending returns the number of queued intents for a device.
func (s *IntentStore) Pending(deviceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	for _, q := range s.queue {
		if q.DeviceID == deviceID {
			n++
		}
	}
	return n
}

// ── Conflict resolution ─────────────────────────────────────────────────────

// Op types for versioned entities.
const (
	OpUpdate    = "UPDATE"
	OpWrite     = "WRITE"
	OpDelete    = "DELETE"
	OpIncrement = "INCREMENT"
)

// Decision is the resolver outcome.
type Decision string

const (
	DecisionMerge        Decision = "MERGE"
	DecisionReject       Decision = "REJECT"
	DecisionRetry        Decision = "RETRY"
	DecisionManualReview Decision = "MANUAL_REVIEW"
)

// VersionedOp is one actor's proposed mutation.
type VersionedOp struct {
	Entity      string            `json:"entity"`
	BaseVersion int               `json:"base_version"`
	Op          string            `json:"op"`
	ActorDevice string            `json:"actor_device"`
	LamportTs   uint64            `json:"lamport_ts"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// Resolution is the outcome of Resolve.
type Resolution struct {
	Decision Decision          `json:"decision"`
	Merged   map[string]string `json:"merged,omitempty"`
	Reason   string            `json:"reason"`
}

// Resolver merges concurrent versioned ops.
type Resolver struct{}

// NewResolver returns a Resolver.
func NewResolver() *Resolver { return &Resolver{} }

// Resolve decides between two concurrent ops on the same entity:
//   - MERGE for concurrent metadata updates (field-level union, higher
//     lamport timestamp wins per-key conflicts);
//   - REJECT for a stale delete against a newer write;
//   - RETRY for concurrent increments on the same base version;
//   - MANUAL_REVIEW for delete-vs-update races (and anything ambiguous).
func (r *Resolver) Resolve(local, remote VersionedOp) (Resolution, error) {
	if local.Entity == "" || remote.Entity == "" {
		return Resolution{}, fmt.Errorf("entity is required")
	}
	if local.Entity != remote.Entity {
		return Resolution{}, fmt.Errorf("cannot resolve ops on different entities")
	}
	ln, rn := normaliseOp(local.Op), normaliseOp(remote.Op)
	if ln == "" || rn == "" {
		return Resolution{}, fmt.Errorf("unknown op")
	}

	// Stale delete vs newer write: the delete loses.
	if ln == OpDelete && isWrite(rn) && local.BaseVersion < remote.BaseVersion {
		return Resolution{Decision: DecisionReject, Reason: "stale delete vs newer write"}, nil
	}
	if rn == OpDelete && isWrite(ln) && remote.BaseVersion < local.BaseVersion {
		return Resolution{Decision: DecisionReject, Reason: "stale delete vs newer write"}, nil
	}
	// Concurrent increments on the same base must retry on the new base.
	if ln == OpIncrement && rn == OpIncrement && local.BaseVersion == remote.BaseVersion {
		return Resolution{Decision: DecisionRetry, Reason: "concurrent increments"}, nil
	}
	// Delete vs update on the same base needs a human.
	if isDelete(ln) != isDelete(rn) && local.BaseVersion == remote.BaseVersion {
		if (isDelete(ln) && isWrite(rn)) || (isDelete(rn) && isWrite(ln)) {
			return Resolution{Decision: DecisionManualReview, Reason: "delete vs update"}, nil
		}
	}
	// Concurrent metadata writes merge field-by-field.
	if isWrite(ln) && isWrite(rn) {
		merged := make(map[string]string, len(local.Metadata)+len(remote.Metadata))
		for k, v := range local.Metadata {
			merged[k] = v
		}
		for k, v := range remote.Metadata {
			if lv, ok := merged[k]; !ok || remote.LamportTs >= local.LamportTs {
				merged[k] = v
			} else {
				merged[k] = lv
			}
		}
		return Resolution{Decision: DecisionMerge, Merged: merged, Reason: "field-level union"}, nil
	}
	return Resolution{Decision: DecisionManualReview, Reason: "ambiguous conflict"}, nil
}

func normaliseOp(op string) string {
	switch op {
	case OpUpdate, OpWrite:
		return op
	case OpDelete:
		return OpDelete
	case OpIncrement:
		return OpIncrement
	default:
		return ""
	}
}

func isWrite(op string) bool  { return op == OpUpdate || op == OpWrite }
func isDelete(op string) bool { return op == OpDelete }
