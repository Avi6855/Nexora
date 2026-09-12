// Package eventsourcing implements Nexora's event-sourced customer state:
//
//   - append-only per-aggregate event store with optimistic concurrency
//     (expected_seq)
//   - projections registry (named projectors over event types) with
//     read-model rebuild to any seq plus historical snapshots
//   - customer projectors (balance, cards, pots) over
//     CustomerCreated/CardIssued/PaymentMade/PaymentReversed/PotCreated/
//     MoneyTransferred/RefundReceived/AccountBlocked/AccountUnblocked
//     with blocked-account gating
//   - deterministic pinned replay (DebugReplay): replays a stored stream
//     up to a timestamp with pinned rule versions, returning state +
//     event cursor + rules hash, with version-mismatch detection.
package eventsourcing

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Customer event types.
const (
	EventCustomerCreated  = "CustomerCreated"
	EventCardIssued       = "CardIssued"
	EventPaymentMade      = "PaymentMade"
	EventPaymentReversed  = "PaymentReversed"
	EventPotCreated       = "PotCreated"
	EventMoneyTransferred = "MoneyTransferred"
	EventRefundReceived   = "RefundReceived"
	EventAccountBlocked   = "AccountBlocked"
	EventAccountUnblocked = "AccountUnblocked"
)

var (
	// ErrNotFound is returned for unknown aggregates or projectors.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned for empty ids/types or malformed payloads.
	ErrInvalid = errors.New("invalid request")
	// ErrConflict is returned on optimistic-concurrency mismatch or
	// duplicate projector registration.
	ErrConflict = errors.New("conflict")
	// ErrBlocked is returned when a payment moves while blocked.
	ErrBlocked = errors.New("account is blocked")
	// ErrUnknownRule is returned when a pinned rule version is not known.
	ErrUnknownRule = errors.New("unknown rule version")
)

// KnownRuleVersions is the pinned-rule catalogue for deterministic replay.
// ReplayAt rejects any rule name/version outside this set.
var KnownRuleVersions = map[string][]string{
	"pricing": {"v1", "v2"},
	"fraud":   {"v1", "v2"},
	"ledger":  {"v1"},
}

// CSEvent is one append-only fact in an aggregate stream.
type CSEvent struct {
	AggregateID string          `json:"aggregate_id"`
	Seq         int             `json:"seq"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	At          time.Time       `json:"at"`
}

// CustomerCard is one issued card on the read model.
type CustomerCard struct {
	CardID string `json:"card_id"`
	Last4  string `json:"last4"`
	Active bool   `json:"active"`
}

// CustomerState is the projected read model for one customer aggregate.
type CustomerState struct {
	AggregateID   string           `json:"aggregate_id"`
	Balance       int64            `json:"balance"`
	Cards         []CustomerCard   `json:"cards"`
	Pots          map[string]int64 `json:"pots"`
	Blocked       bool             `json:"blocked"`
	CursorSeq     int              `json:"cursor_seq"`
	EventsApplied int              `json:"events_applied"`
}

// PinnedReplay pins a deterministic replay to an instant + rule versions.
type PinnedReplay struct {
	At           time.Time         `json:"at"`
	RuleVersions map[string]string `json:"rule_versions"`
}

// DebugReplayResult is the deterministic replay outcome.
type DebugReplayResult struct {
	State          *CustomerState `json:"state"`
	CursorSeq      int            `json:"cursor_seq"`
	RulesHash      string         `json:"rules_hash"`
	EventsReplayed int            `json:"events_replayed"`
	At             time.Time      `json:"at"`
}

// ProjectorFunc folds one event into the read model.
type ProjectorFunc func(state *CustomerState, ev CSEvent) error

type projectorEntry struct {
	name       string
	eventTypes map[string]bool
	fn         ProjectorFunc
}

// Store is the mutex-guarded append-only event store + projector registry.
type Store struct {
	mu         sync.RWMutex
	streams    map[string][]CSEvent
	projectors map[string]*projectorEntry
	order      []string
}

// NewStore builds an empty store with the customer projectors registered.
func NewStore() *Store {
	s := &Store{
		streams:    map[string][]CSEvent{},
		projectors: map[string]*projectorEntry{},
	}
	// Customer projectors are always present: balance, cards, pots.
	_ = s.RegisterProjector("balance",
		[]string{EventCustomerCreated, EventPaymentMade, EventPaymentReversed, EventRefundReceived, EventMoneyTransferred, EventAccountBlocked, EventAccountUnblocked},
		applyBalance)
	_ = s.RegisterProjector("cards",
		[]string{EventCustomerCreated, EventCardIssued},
		applyCards)
	_ = s.RegisterProjector("pots",
		[]string{EventPotCreated, EventMoneyTransferred},
		applyPots)
	return s
}

// RegisterProjector adds a named projector over event types. Duplicate names
// conflict; empty names or type lists are invalid.
func (s *Store) RegisterProjector(name string, eventTypes []string, fn ProjectorFunc) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: projector name is required", ErrInvalid)
	}
	if len(eventTypes) == 0 {
		return fmt.Errorf("%w: at least one event type is required", ErrInvalid)
	}
	if fn == nil {
		return fmt.Errorf("%w: projector func is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.projectors[name]; exists {
		return fmt.Errorf("projector %q: %w", name, ErrConflict)
	}
	set := map[string]bool{}
	for _, t := range eventTypes {
		t = strings.TrimSpace(t)
		if t == "" {
			return fmt.Errorf("%w: event type must not be blank", ErrInvalid)
		}
		set[t] = true
	}
	s.projectors[name] = &projectorEntry{name: name, eventTypes: set, fn: fn}
	s.order = append(s.order, name)
	return nil
}

// Append adds one event with optimistic concurrency: expectedSeq must equal
// the current stream length (0 for a new aggregate).
func (s *Store) Append(aggregateID string, expectedSeq int, eventType string, payload json.RawMessage, at time.Time) (*CSEvent, error) {
	aggregateID = strings.TrimSpace(aggregateID)
	if aggregateID == "" {
		return nil, fmt.Errorf("%w: aggregate_id is required", ErrInvalid)
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return nil, fmt.Errorf("%w: event type is required", ErrInvalid)
	}
	if expectedSeq < 0 {
		return nil, fmt.Errorf("%w: expected_seq must be >= 0", ErrInvalid)
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	} else {
		var v interface{}
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("%w: payload must be valid JSON", ErrInvalid)
		}
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stream := s.streams[aggregateID]
	if expectedSeq != len(stream) {
		return nil, fmt.Errorf("aggregate %q expected_seq %d conflicts with stream length %d: %w", aggregateID, expectedSeq, len(stream), ErrConflict)
	}
	ev := CSEvent{
		AggregateID: aggregateID,
		Seq:         len(stream) + 1,
		Type:        eventType,
		Payload:     append(json.RawMessage(nil), payload...),
		At:          at.UTC(),
	}
	s.streams[aggregateID] = append(stream, ev)
	out := ev
	return &out, nil
}

// Load returns a copy of one aggregate stream in seq order.
func (s *Store) Load(aggregateID string) ([]CSEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stream, ok := s.streams[aggregateID]
	if !ok || len(stream) == 0 {
		return nil, fmt.Errorf("aggregate %q: %w", aggregateID, ErrNotFound)
	}
	out := make([]CSEvent, len(stream))
	copy(out, stream)
	return out, nil
}

// Project rebuilds the read model to toSeq (0 or negative means latest).
func (s *Store) Project(aggregateID string, toSeq int) (*CustomerState, error) {
	s.mu.RLock()
	stream, ok := s.streams[aggregateID]
	if !ok || len(stream) == 0 {
		s.mu.RUnlock()
		return nil, fmt.Errorf("aggregate %q: %w", aggregateID, ErrNotFound)
	}
	events := make([]CSEvent, len(stream))
	copy(events, stream)
	entries := make([]*projectorEntry, 0, len(s.order))
	for _, name := range s.order {
		entries = append(entries, s.projectors[name])
	}
	s.mu.RUnlock()
	state := &CustomerState{AggregateID: aggregateID, Pots: map[string]int64{}}
	applied := 0
	for _, ev := range events {
		if toSeq > 0 && ev.Seq > toSeq {
			break
		}
		for _, p := range entries {
			if !p.eventTypes[ev.Type] {
				continue
			}
			if err := p.fn(state, ev); err != nil {
				return nil, err
			}
		}
		state.CursorSeq = ev.Seq
		applied++
	}
	state.EventsApplied = applied
	if state.Cards == nil {
		state.Cards = []CustomerCard{}
	}
	return state, nil
}

// Snapshot returns the historical read model at seq (alias for Project,
// kept explicit for support tooling).
func (s *Store) Snapshot(aggregateID string, seq int) (*CustomerState, error) {
	return s.Project(aggregateID, seq)
}

// ReplayAt deterministically replays the stored stream up to instant at
// with pinned rule versions. Events with At after the pin are excluded
// (exact-timestamp cutoff: At.Equal(at) is included). Unknown rule
// names/versions are rejected before any folding.
func (s *Store) ReplayAt(aggregateID string, at time.Time, rules map[string]string) (*DebugReplayResult, error) {
	if at.IsZero() {
		return nil, fmt.Errorf("%w: replay instant at is required", ErrInvalid)
	}
	hash, err := hashRules(rules)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	stream, ok := s.streams[aggregateID]
	if !ok || len(stream) == 0 {
		s.mu.RUnlock()
		return nil, fmt.Errorf("aggregate %q: %w", aggregateID, ErrNotFound)
	}
	events := make([]CSEvent, len(stream))
	copy(events, stream)
	entries := make([]*projectorEntry, 0, len(s.order))
	for _, name := range s.order {
		entries = append(entries, s.projectors[name])
	}
	s.mu.RUnlock()
	state := &CustomerState{AggregateID: aggregateID, Pots: map[string]int64{}}
	cursor, applied := 0, 0
	for _, ev := range events {
		if ev.At.After(at) {
			continue
		}
		for _, p := range entries {
			if !p.eventTypes[ev.Type] {
				continue
			}
			if err := p.fn(state, ev); err != nil {
				return nil, err
			}
		}
		if ev.Seq > cursor {
			cursor = ev.Seq
		}
		applied++
	}
	if state.Cards == nil {
		state.Cards = []CustomerCard{}
	}
	state.CursorSeq = cursor
	state.EventsApplied = applied
	return &DebugReplayResult{
		State:          state,
		CursorSeq:      cursor,
		RulesHash:      hash,
		EventsReplayed: applied,
		At:             at.UTC(),
	}, nil
}

// hashRules validates pinned versions against KnownRuleVersions and returns
// the deterministic sha256 over sorted "name=version" pairs.
func hashRules(rules map[string]string) (string, error) {
	if len(rules) == 0 {
		return "", fmt.Errorf("%w: at least one rule version is required", ErrInvalid)
	}
	names := make([]string, 0, len(rules))
	for n := range rules {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		allowed, ok := KnownRuleVersions[n]
		if !ok {
			return "", fmt.Errorf("rule %q: %w", n, ErrUnknownRule)
		}
		found := false
		for _, v := range allowed {
			if v == rules[n] {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("rule %q version %q: %w", n, rules[n], ErrUnknownRule)
		}
	}
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(n)
		b.WriteString("=")
		b.WriteString(rules[n])
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

// ── customer projector fns ──────────────────────────────────────────────────

func decodePayload(ev CSEvent) map[string]interface{} {
	m := map[string]interface{}{}
	if len(ev.Payload) == 0 {
		return m
	}
	_ = json.Unmarshal(ev.Payload, &m)
	return m
}

func amountOf(m map[string]interface{}) int64 {
	switch v := m["amount"].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func stringOf(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func applyBalance(state *CustomerState, ev CSEvent) error {
	m := decodePayload(ev)
	switch ev.Type {
	case EventCustomerCreated:
		state.Balance = amountOf(m)
		if v, ok := m["initial_balance"].(float64); ok {
			state.Balance = int64(v)
		}
	case EventPaymentMade:
		if state.Blocked {
			return fmt.Errorf("%s on blocked account: %w", EventPaymentMade, ErrBlocked)
		}
		state.Balance -= amountOf(m)
	case EventPaymentReversed, EventRefundReceived:
		state.Balance += amountOf(m)
	case EventMoneyTransferred:
		from, to := stringOf(m, "from_pot"), stringOf(m, "to_pot")
		amount := amountOf(m)
		switch {
		case from == "" && to != "":
			if state.Blocked {
				return fmt.Errorf("%s on blocked account: %w", EventMoneyTransferred, ErrBlocked)
			}
			state.Balance -= amount
		case from != "" && to == "":
			state.Balance += amount
		}
	case EventAccountBlocked:
		state.Blocked = true
	case EventAccountUnblocked:
		state.Blocked = false
	}
	return nil
}

func applyCards(state *CustomerState, ev CSEvent) error {
	m := decodePayload(ev)
	switch ev.Type {
	case EventCustomerCreated:
		if state.Cards == nil {
			state.Cards = []CustomerCard{}
		}
	case EventCardIssued:
		cardID := stringOf(m, "card_id")
		if cardID == "" {
			return fmt.Errorf("%w: card_id is required", ErrInvalid)
		}
		state.Cards = append(state.Cards, CustomerCard{
			CardID: cardID,
			Last4:  stringOf(m, "last4"),
			Active: true,
		})
	}
	return nil
}

func applyPots(state *CustomerState, ev CSEvent) error {
	m := decodePayload(ev)
	switch ev.Type {
	case EventPotCreated:
		potID := stringOf(m, "pot_id")
		if potID == "" {
			return fmt.Errorf("%w: pot_id is required", ErrInvalid)
		}
		if state.Pots == nil {
			state.Pots = map[string]int64{}
		}
		if _, exists := state.Pots[potID]; !exists {
			state.Pots[potID] = 0
		}
	case EventMoneyTransferred:
		from, to := stringOf(m, "from_pot"), stringOf(m, "to_pot")
		amount := amountOf(m)
		if amount <= 0 {
			return fmt.Errorf("%w: transfer amount must be positive", ErrInvalid)
		}
		if state.Pots == nil {
			state.Pots = map[string]int64{}
		}
		switch {
		case from != "" && to != "":
			state.Pots[from] -= amount
			state.Pots[to] += amount
		case from == "" && to != "":
			state.Pots[to] += amount
		case from != "" && to == "":
			state.Pots[from] -= amount
		default:
			return fmt.Errorf("%w: transfer needs from_pot and/or to_pot", ErrInvalid)
		}
	}
	return nil
}
