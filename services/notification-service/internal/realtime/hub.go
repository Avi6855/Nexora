package realtime

import (
	"sync"

	"github.com/google/uuid"
)

// Hub fans out live feed/notification events to connected clients per user.
// It is the in-memory delivery layer behind the SSE /v1/stream endpoint: the
// Android app holds one connection and receives every event the notification
// service emits for that user — card authorisations, captures with the new
// balance, security alerts. No polling, no hardcoded data: events originate
// from Kafka consumers / API calls and are persisted before fan-out.
type Hub struct {
	mu    sync.RWMutex
	conns map[uuid.UUID]map[chan []byte]struct{}
}

func NewHub() *Hub {
	return &Hub{conns: make(map[uuid.UUID]map[chan []byte]struct{})}
}

// Subscribe registers a connection for userID and returns the channel plus a
// close function that deregisters and drains it.
func (h *Hub) Subscribe(userID uuid.UUID) (<-chan []byte, func()) {
	ch := make(chan []byte, 64)
	h.mu.Lock()
	if h.conns[userID] == nil {
		h.conns[userID] = make(map[chan []byte]struct{})
	}
	h.conns[userID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if conns := h.conns[userID]; conns != nil {
				delete(conns, ch)
				if len(conns) == 0 {
					delete(h.conns, userID)
				}
			}
			close(ch)
		})
	}
	return ch, closeFn
}

// Publish delivers an event to every connected client of userID. Slow clients
// are dropped rather than allowed to back-pressure the notification path.
func (h *Hub) Publish(userID uuid.UUID, payload []byte) {
	h.mu.RLock()
	conns := h.conns[userID]
	var targets []chan []byte
	for ch := range conns {
		targets = append(targets, ch)
	}
	h.mu.RUnlock()

	for _, ch := range targets {
		select {
		case ch <- payload:
		default:
			// Client too slow: drop it (reconnect via SSE is cheap).
		}
	}
}

// SubscriberCount is used by the /live endpoint and metrics.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	total := 0
	for _, conns := range h.conns {
		total += len(conns)
	}
	return total
}
