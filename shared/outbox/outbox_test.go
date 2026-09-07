package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── In-memory store ─────────────────────────────────────────────────────────

type memStore struct {
	mu     sync.Mutex
	events map[uuid.UUID]*Event
	order  []uuid.UUID
}

func newMemStore() *memStore {
	return &memStore{events: make(map[uuid.UUID]*Event)}
}

func (m *memStore) Enqueue(ctx context.Context, events ...*Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range events {
		e.Status = StatusPending
		m.events[e.EventID] = e
		m.order = append(m.order, e.EventID)
	}
	return nil
}

func (m *memStore) ClaimPending(ctx context.Context, limit int) ([]*Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Event
	for _, id := range m.order {
		if len(out) >= limit {
			break
		}
		if e, ok := m.events[id]; ok && e.Status == StatusPending {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) MarkPublished(ctx context.Context, eventID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.events[eventID]; ok {
		now := time.Now().UTC()
		e.Status = StatusPublished
		e.PublishedAt = &now
	}
	return nil
}

func (m *memStore) MarkFailed(ctx context.Context, eventID uuid.UUID, err error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.events[eventID]; ok {
		e.Attempts++
		e.LastError = err.Error()
	}
	return nil
}

func (m *memStore) MarkTerminal(ctx context.Context, eventID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.events[eventID]; ok {
		now := time.Now().UTC()
		e.Status = StatusFailed
		e.PublishedAt = &now
	}
	return nil
}

func (m *memStore) CountPending(ctx context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, e := range m.events {
		if e.Status == StatusPending {
			n++
		}
	}
	return n, nil
}

func (m *memStore) get(id uuid.UUID) (*Event, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[id]
	return e, ok
}

// ── Fake publisher ──────────────────────────────────────────────────────────

type fakePublisher struct {
	mu         sync.Mutex
	publishErr error            // returned by every Publish call
	dlqErr     error            // returned by every PublishDLQ call
	published  []*Event
	dlq        []*Event
}

func newFakePublisher() *fakePublisher {
	return &fakePublisher{}
}

func (f *fakePublisher) Publish(ctx context.Context, event *Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.publishErr != nil {
		return f.publishErr
	}
	f.published = append(f.published, event)
	return nil
}

func (f *fakePublisher) PublishDLQ(ctx context.Context, event *Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dlqErr != nil {
		return f.dlqErr
	}
	f.dlq = append(f.dlq, event)
	return nil
}

func (f *fakePublisher) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func (f *fakePublisher) dlqCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.dlq)
}

// ── Fixtures ────────────────────────────────────────────────────────────────

func newTestEvent(aggregate string) *Event {
	return &Event{
		EventID:       uuid.New(),
		AggregateID:   aggregate,
		EventType:     "payment.created",
		Topic:         "nexora.payment.created",
		CorrelationID: uuid.New().String(),
		Producer:      "payment-service",
		Payload:       []byte(`{"payment_id":"` + aggregate + `"}`),
		Status:        StatusPending,
		CreatedAt:     time.Now().UTC(),
	}
}

func newRelayForTest(store Store, pub Publisher, maxAttempts int) *Relay {
	cfg := RelayConfig{
		PollInterval: 10 * time.Millisecond,
		BatchSize:    100,
		MaxAttempts:  maxAttempts,
	}
	return NewRelay(store, pub, zerolog.Nop(), cfg)
}

// ── Tests ───────────────────────────────────────────────────────────────────

func TestEnqueueAndClaim(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()

	e1 := newTestEvent("agg-1")
	e2 := newTestEvent("agg-2")
	e3 := newTestEvent("agg-3")

	require.NoError(t, store.Enqueue(ctx, e1, e2, e3))

	claimed, err := store.ClaimPending(ctx, 10)
	require.NoError(t, err)
	require.Len(t, claimed, 3)
	assert.Equal(t, e1.EventID, claimed[0].EventID)
	assert.Equal(t, e2.EventID, claimed[1].EventID)
	assert.Equal(t, e3.EventID, claimed[2].EventID)

	count, err := store.CountPending(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestRelayPublishesPendingEvents(t *testing.T) {
	store := newMemStore()
	pub := newFakePublisher()
	relay := newRelayForTest(store, pub, 5)
	ctx := context.Background()

	ev := newTestEvent("agg-1")
	require.NoError(t, store.Enqueue(ctx, ev))

	relay.drainOnce()

	assert.Equal(t, 1, pub.publishedCount())
	stored, ok := store.get(ev.EventID)
	require.True(t, ok)
	assert.Equal(t, StatusPublished, stored.Status)
	assert.NotNil(t, stored.PublishedAt)

	// A second drain must not re-publish (dedupe / restart safety).
	relay.drainOnce()
	assert.Equal(t, 1, pub.publishedCount())
}

func TestRelayRetriesFailedPublishes(t *testing.T) {
	store := newMemStore()
	pub := newFakePublisher()
	relay := newRelayForTest(store, pub, 5)
	ctx := context.Background()

	ev := newTestEvent("agg-1")
	require.NoError(t, store.Enqueue(ctx, ev))

	// Fail the first two attempts, then succeed.
	pub.publishErr = errors.New("broker unavailable")
	relay.drainOnce()
	relay.drainOnce()

	stored, _ := store.get(ev.EventID)
	assert.Equal(t, StatusPending, stored.Status)
	assert.Equal(t, 2, stored.Attempts)
	assert.Equal(t, 0, pub.publishedCount())

	pub.publishErr = nil
	relay.drainOnce()

	assert.Equal(t, 1, pub.publishedCount())
	stored, _ = store.get(ev.EventID)
	assert.Equal(t, StatusPublished, stored.Status)
}

func TestRelayMovesPoisonEventToDLQ(t *testing.T) {
	store := newMemStore()
	pub := newFakePublisher()
	// MaxAttempts = 3: publish attempts at attempts=0,1,2 all fail, then DLQ.
	relay := newRelayForTest(store, pub, 3)
	ctx := context.Background()

	ev := newTestEvent("poison")
	require.NoError(t, store.Enqueue(ctx, ev))

	pub.publishErr = errors.New("poison message")
	// attempts 0,1,2 fail; the claim at attempts=3 triggers the DLQ path.
	for i := 0; i < 4; i++ {
		relay.drainOnce()
	}

	assert.Equal(t, 0, pub.publishedCount())
	assert.Equal(t, 1, pub.dlqCount())

	stored, _ := store.get(ev.EventID)
	assert.Equal(t, StatusFailed, stored.Status)
	assert.Equal(t, 3, stored.Attempts)

	// Subsequent drains do nothing (event is terminal).
	relay.drainOnce()
	assert.Equal(t, 1, pub.dlqCount())
}

func TestRelayPublishesImmediatelyOnStart(t *testing.T) {
	// Events enqueued before the relay starts (e.g. by a previous process
	// that crashed) must be drained on Start, not on the first tick.
	store := newMemStore()
	pub := newFakePublisher()
	cfg := RelayConfig{PollInterval: time.Hour, BatchSize: 10, MaxAttempts: 5}
	relay := NewRelay(store, pub, zerolog.Nop(), cfg)
	ctx := context.Background()

	ev := newTestEvent("agg-1")
	require.NoError(t, store.Enqueue(ctx, ev))

	relay.Start(context.Background())
	defer relay.Stop()
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, 1, pub.publishedCount())
}

func TestClaimRespectsLimit(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		require.NoError(t, store.Enqueue(ctx, newTestEvent("agg")))
	}

	claimed, err := store.ClaimPending(ctx, 4)
	require.NoError(t, err)
	assert.Len(t, claimed, 4)
}