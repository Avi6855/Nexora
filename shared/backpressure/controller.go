package backpressure

import (
	"sync"
	"time"
)

type WorkloadClass string

const (
	WorkloadCritical    WorkloadClass = "CRITICAL"
	WorkloadImportant   WorkloadClass = "IMPORTANT"
	WorkloadDeferable   WorkloadClass = "DEFERABLE"
)

type Request struct {
	ID        string       `json:"id"`
	Class     WorkloadClass `json:"class"`
	Service   string       `json:"service"`
	Priority  int          `json:"priority"`
	Timestamp time.Time    `json:"timestamp"`
}

type QueueEntry struct {
	Request   *Request     `json:"request"`
	EnqueuedAt time.Time   `json:"enqueued_at"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
}

type RateLimitConfig struct {
	CriticalRPS  float64 `json:"critical_rps"`
	ImportantRPS float64 `json:"important_rps"`
	DeferableRPS float64 `json:"deferable_rps"`
}

type BackpressureController struct {
	queue       []*QueueEntry
	mu          sync.RWMutex
	config      *RateLimitConfig
	counters    map[WorkloadClass]*RateCounter
	overloaded  bool
	maxQueueSize int
}

type RateCounter struct {
	Class       WorkloadClass
	Count       int64
	WindowStart time.Time
	RPS         float64
}

func NewBackpressureController(config *RateLimitConfig) *BackpressureController {
	if config == nil {
		config = DefaultRateLimitConfig()
	}

	return &BackpressureController{
		queue:        make([]*QueueEntry, 0),
		config:       config,
		counters:     make(map[WorkloadClass]*RateCounter),
		maxQueueSize: 10000,
	}
}

func DefaultRateLimitConfig() *RateLimitConfig {
	return &RateLimitConfig{
		CriticalRPS:  1000,
		ImportantRPS: 500,
		DeferableRPS: 100,
	}
}

func (c *BackpressureController) ClassifyWorkload(service, operation string) WorkloadClass {
	criticalOperations := map[string]bool{
		"payment.create":      true,
		"payment.authorize":   true,
		"payment.process":     true,
		"transfer.create":     true,
		"ledger.create_entry": true,
		"fraud.check":         true,
	}

	importantOperations := map[string]bool{
		"payment.get":           true,
		"payment.list":          true,
		"account.get":           true,
		"account.balance":       true,
		"user.get":              true,
		"notification.send":     true,
		"policy.evaluate":       true,
	}

	key := service + "." + operation

	if criticalOperations[key] {
		return WorkloadCritical
	}

	if importantOperations[key] {
		return WorkloadImportant
	}

	return WorkloadDeferable
}

func (c *BackpressureController) ShouldProcess(req *Request) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.overloaded && req.Class == WorkloadDeferable {
		return false
	}

	if !c.checkRateLimit(req.Class) {
		if req.Class == WorkloadDeferable {
			return false
		}
		if req.Class == WorkloadImportant && c.overloaded {
			return false
		}
	}

	return true
}

func (c *BackpressureController) checkRateLimit(class WorkloadClass) bool {
	counter, ok := c.counters[class]
	if !ok {
		counter = &RateCounter{
			Class:       class,
			WindowStart: time.Now().UTC(),
		}
		c.counters[class] = counter
	}

	now := time.Now().UTC()
	if now.Sub(counter.WindowStart) >= time.Second {
		counter.RPS = float64(counter.Count)
		counter.Count = 0
		counter.WindowStart = now
	}

	var limit float64
	switch class {
	case WorkloadCritical:
		limit = c.config.CriticalRPS
	case WorkloadImportant:
		limit = c.config.ImportantRPS
	case WorkloadDeferable:
		limit = c.config.DeferableRPS
	default:
		return false
	}

	return counter.RPS < limit
}

func (c *BackpressureController) Enqueue(req *Request) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.queue) >= c.maxQueueSize {
		if req.Class == WorkloadDeferable {
			return false
		}
		c.dropDeferableRequests()
	}

	entry := &QueueEntry{
		Request:    req,
		EnqueuedAt: time.Now().UTC(),
	}

	c.queue = append(c.queue, entry)

	c.countRequest(req.Class)

	return true
}

func (c *BackpressureController) Dequeue() *Request {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.queue) == 0 {
		return nil
	}

	c.sortByPriority()

	entry := c.queue[0]
	c.queue = c.queue[1:]

	if entry.ExpiresAt != nil && time.Now().UTC().After(*entry.ExpiresAt) {
		return c.Dequeue()
	}

	return entry.Request
}

func (c *BackpressureController) sortByPriority() {
	priorityOrder := map[WorkloadClass]int{
		WorkloadCritical:  0,
		WorkloadImportant: 1,
		WorkloadDeferable: 2,
	}

	for i := 0; i < len(c.queue); i++ {
		for j := i + 1; j < len(c.queue); j++ {
			if priorityOrder[c.queue[i].Request.Class] > priorityOrder[c.queue[j].Request.Class] {
				c.queue[i], c.queue[j] = c.queue[j], c.queue[i]
			}
		}
	}
}

func (c *BackpressureController) countRequest(class WorkloadClass) {
	counter, ok := c.counters[class]
	if !ok {
		counter = &RateCounter{
			Class:       class,
			WindowStart: time.Now().UTC(),
		}
		c.counters[class] = counter
	}
	counter.Count++
}

func (c *BackpressureController) dropDeferableRequests() {
	var retained []*QueueEntry
	for _, entry := range c.queue {
		if entry.Request.Class != WorkloadDeferable {
			retained = append(retained, entry)
		}
	}
	c.queue = retained
}

func (c *BackpressureController) SetOverloaded(overloaded bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.overloaded = overloaded
}

func (c *BackpressureController) IsOverloaded() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.overloaded
}

func (c *BackpressureController) QueueSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.queue)
}

func (c *BackpressureController) GetCounters() map[WorkloadClass]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make(map[WorkloadClass]int64)
	for class, counter := range c.counters {
		result[class] = counter.Count
	}
	return result
}

func (c *BackpressureController) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = make([]*QueueEntry, 0)
	c.counters = make(map[WorkloadClass]*RateCounter)
	c.overloaded = false
}

func (c *BackpressureController) ProcessRequest(req *Request) bool {
	if !c.ShouldProcess(req) {
		if req.Class == WorkloadDeferable {
			c.Enqueue(req)
			return false
		}
		return false
	}

	c.countRequest(req.Class)
	return true
}

func (c *BackpressureController) GetQueueByClass(class WorkloadClass) []*QueueEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var result []*QueueEntry
	for _, entry := range c.queue {
		if entry.Request.Class == class {
			result = append(result, entry)
		}
	}
	return result
}

func (c *BackpressureController) PurgeExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().UTC()
	purged := 0
	var retained []*QueueEntry

	for _, entry := range c.queue {
		if entry.ExpiresAt != nil && now.After(*entry.ExpiresAt) {
			purged++
		} else {
			retained = append(retained, entry)
		}
	}

	c.queue = retained
	return purged
}
