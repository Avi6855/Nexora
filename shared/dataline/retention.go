package dataline

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── 15. Retention engine ────────────────────────────────────────────────────

// Retention categories.
const (
	CategoryMetadata    = "metadata"
	CategoryTelemetry   = "telemetry"
	CategoryEvidence    = "evidence"
	CategoryAttachments = "attachments"
)

// Lifecycle states.
const (
	StateCreated    = "CREATED"
	StateArchived   = "ARCHIVED"
	StateAnonymised = "ANONYMISED"
	StateDeleted    = "DELETED"
)

func validCategory(c string) bool {
	switch strings.ToLower(strings.TrimSpace(c)) {
	case CategoryMetadata, CategoryTelemetry, CategoryEvidence, CategoryAttachments:
		return true
	default:
		return false
	}
}

// Policy holds one retention horizon per category. Units follow the
// platform contract: metadata/evidence in years, telemetry/attachments
// in days.
type Policy struct {
	MetadataYears   int `json:"metadata_years"`
	TelemetryDays   int `json:"telemetry_days"`
	EvidenceYears   int `json:"evidence_years"`
	AttachmentsDays int `json:"attachments_days"`
}

// Item is one classified record moving through the lifecycle.
type Item struct {
	ID        string    `json:"id"`
	Category  string    `json:"category"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Hold is a legal hold blocking transitions on one item.
type Hold struct {
	ID     string    `json:"id"`
	ItemID string    `json:"item_id"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// DueTransition is one item whose next lifecycle step is due.
type DueTransition struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// Engine runs classification, due-date computation and guarded
// transitions with legal holds.
type Engine struct {
	mu         sync.Mutex
	policy     Policy
	hasPolicy  bool
	items      map[string]*Item
	holds      map[string]*Hold
	holdByItem map[string]string
	seq        int
}

// NewEngine builds an empty retention engine.
func NewEngine() *Engine {
	return &Engine{
		items:      map[string]*Item{},
		holds:      map[string]*Hold{},
		holdByItem: map[string]string{},
	}
}

// PutPolicy replaces the retention horizons. All horizons must be
// positive.
func (e *Engine) PutPolicy(p Policy) error {
	if p.MetadataYears <= 0 || p.TelemetryDays <= 0 || p.EvidenceYears <= 0 || p.AttachmentsDays <= 0 {
		return fmt.Errorf("%w: all policy horizons must be positive", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policy = p
	e.hasPolicy = true
	return nil
}

// Classify enrols one item in CREATED.
func (e *Engine) Classify(id, category string, createdAt time.Time) (*Item, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: id is required", ErrInvalid)
	}
	if !validCategory(category) {
		return nil, fmt.Errorf("%w: category must be one of metadata, telemetry, evidence, attachments", ErrInvalid)
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	category = strings.ToLower(strings.TrimSpace(category))
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.items[id]; exists {
		return nil, fmt.Errorf("item %q: %w", id, ErrConflict)
	}
	it := &Item{ID: id, Category: category, State: StateCreated, CreatedAt: createdAt, UpdatedAt: createdAt}
	e.items[id] = it
	out := *it
	return &out, nil
}

// GetItem returns one item.
func (e *Engine) GetItem(id string) (*Item, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[id]
	if !ok {
		return nil, fmt.Errorf("item %q: %w", id, ErrNotFound)
	}
	out := *it
	return &out, nil
}

// retentionFor resolves the horizon for a category.
func (e *Engine) retentionFor(category string) time.Duration {
	switch strings.ToLower(category) {
	case CategoryMetadata:
		return time.Duration(e.policy.MetadataYears) * 365 * 24 * time.Hour
	case CategoryTelemetry:
		return time.Duration(e.policy.TelemetryDays) * 24 * time.Hour
	case CategoryEvidence:
		return time.Duration(e.policy.EvidenceYears) * 365 * 24 * time.Hour
	case CategoryAttachments:
		return time.Duration(e.policy.AttachmentsDays) * 24 * time.Hour
	default:
		return 0
	}
}

// nextState advances one lifecycle step.
func nextState(s string) (string, bool) {
	switch s {
	case StateCreated:
		return StateArchived, true
	case StateArchived:
		return StateAnonymised, true
	case StateAnonymised:
		return StateDeleted, true
	default:
		return "", false
	}
}

// DueTransitions lists items whose next step is due at `now`. Items under
// legal hold are excluded — holds park the clock until released.
func (e *Engine) DueTransitions(now time.Time) ([]DueTransition, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.hasPolicy {
		return nil, fmt.Errorf("%w: no retention policy configured", ErrInvalid)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var out []DueTransition
	for _, it := range e.items {
		if it.State == StateDeleted {
			continue
		}
		if _, held := e.holdByItem[it.ID]; held {
			continue
		}
		next, ok := nextState(it.State)
		if !ok {
			continue
		}
		if !now.Before(it.UpdatedAt.Add(e.retentionFor(it.Category))) {
			out = append(out, DueTransition{ID: it.ID, Category: it.Category, From: it.State, To: next})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ApplyTransition moves one item to its next lifecycle state. Blocked by
// legal holds and by the due date: early transitions are rejected so the
// published horizons are honoured.
func (e *Engine) ApplyTransition(id string, now time.Time) (*Item, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.hasPolicy {
		return nil, fmt.Errorf("%w: no retention policy configured", ErrInvalid)
	}
	it, ok := e.items[id]
	if !ok {
		return nil, fmt.Errorf("item %q: %w", id, ErrNotFound)
	}
	if _, held := e.holdByItem[id]; held {
		return nil, fmt.Errorf("item %q under legal hold: %w", id, ErrConflict)
	}
	next, ok := nextState(it.State)
	if !ok {
		return nil, fmt.Errorf("%w: item %q already DELETED", ErrInvalid, id)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if now.Before(it.UpdatedAt.Add(e.retentionFor(it.Category))) {
		return nil, fmt.Errorf("%w: item %q transition to %s not yet due", ErrInvalid, id, next)
	}
	it.State = next
	it.UpdatedAt = now
	out := *it
	return &out, nil
}

// PlaceHold parks one item under a legal hold. One active hold per item.
func (e *Engine) PlaceHold(itemID, reason string) (*Hold, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("%w: reason is required", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.items[itemID]; !ok {
		return nil, fmt.Errorf("item %q: %w", itemID, ErrNotFound)
	}
	if _, held := e.holdByItem[itemID]; held {
		return nil, fmt.Errorf("item %q already held: %w", itemID, ErrConflict)
	}
	e.seq++
	h := &Hold{ID: fmt.Sprintf("hold-%d", e.seq), ItemID: itemID, Reason: reason, At: time.Now().UTC()}
	e.holds[h.ID] = h
	e.holdByItem[itemID] = h.ID
	out := *h
	return &out, nil
}

// ReleaseHold lifts one legal hold.
func (e *Engine) ReleaseHold(holdID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	h, ok := e.holds[holdID]
	if !ok {
		return fmt.Errorf("hold %q: %w", holdID, ErrNotFound)
	}
	delete(e.holdByItem, h.ItemID)
	delete(e.holds, holdID)
	return nil
}
