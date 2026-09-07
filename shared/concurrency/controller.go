// Package concurrency implements Nexora's Adaptive Concurrency Controller.
//
// Unlike load shedding (which drops work), this package continuously tunes
// HOW MUCH work a service may run at once: max concurrency shrinks on
// timeouts/latency growth and recovers additively when the system is healthy
// (AIMD — the same control law TCP uses). The goal is the knee of the latency
// curve, not the cliff beyond it.
package concurrency

import (
	"math"
	"sync"
	"time"
)

// Signals summarises what the controller observes.
type Signals struct {
	InFlight      int
	Timeouts      int // timeouts observed in the last window
	Completions   int // successful completions in the last window
	P50           time.Duration
	P99           time.Duration
	QueueDepth    int
	DependencyErr float64 // 0..1 error rate of the worst downstream dependency
}

// Config tunes the controller.
type Config struct {
	InitialLimit     int           // starting max concurrency
	MinLimit         int           // never shrink below this
	MaxLimit         int           // never grow beyond this
	ShrinkFactor     float64       // multiplicative decrease (0.5 = halve)
	GrowthPerProbe   int           // additive increase per healthy window
	Window           time.Duration // observation window
	LatencyThreshold time.Duration // p99 beyond which we shrink
	QueueRatio       float64       // queueDepth/limit beyond which we shrink
	DepErrThreshold  float64       // dependency error rate beyond which we shrink
}

// DefaultConfig is a sane starting point for a request-serving service.
func DefaultConfig() Config {
	return Config{
		InitialLimit:     500,
		MinLimit:         20,
		MaxLimit:         2000,
		ShrinkFactor:     0.5,
		GrowthPerProbe:   10,
		Window:           5 * time.Second,
		LatencyThreshold: 800 * time.Millisecond,
		QueueRatio:       2.0,
		DepErrThreshold:  0.05,
	}
}

// Controller adapts max concurrency from observed signals.
type Controller struct {
	mu      sync.Mutex
	cfg     Config
	limit   float64
	lastAdj time.Time

	// rolling window counters
	timeouts    int
	completions int
	windowStart time.Time

	// observed health
	p50 time.Duration
	p99 time.Duration
}

// New creates a controller with the given config.
func New(cfg Config) *Controller {
	if cfg.ShrinkFactor <= 0 || cfg.ShrinkFactor >= 1 {
		cfg.ShrinkFactor = 0.5
	}
	if cfg.MinLimit <= 0 {
		cfg.MinLimit = 1
	}
	if cfg.MaxLimit < cfg.MinLimit {
		cfg.MaxLimit = cfg.MinLimit
	}
	if cfg.InitialLimit < cfg.MinLimit {
		cfg.InitialLimit = cfg.MinLimit
	}
	return &Controller{cfg: cfg, limit: float64(cfg.InitialLimit), windowStart: time.Now()}
}

// Limit returns the current max concurrency (rounded up, clamped).
func (c *Controller) Limit() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := int(math.Ceil(c.limit))
	if l < c.cfg.MinLimit {
		l = c.cfg.MinLimit
	}
	if l > c.cfg.MaxLimit {
		l = c.cfg.MaxLimit
	}
	return l
}

// Observe records request outcomes; call once per completed (or timed-out) request.
func (c *Controller) Observe(s Signals) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeouts += s.Timeouts
	c.completions += s.Completions
	if s.P50 > 0 {
		c.p50 = s.P50
	}
	if s.P99 > 0 {
		c.p99 = s.P99
	}
	if time.Since(c.windowStart) < c.cfg.Window {
		return
	}
	c.windowStart = time.Now()

	// Decide once per window.
	shrink := false
	limit := c.limit
	if limit < 1 {
		limit = 1
	}
	if s.DependencyErr > c.cfg.DepErrThreshold {
		shrink = true
	}
	if c.p99 > c.cfg.LatencyThreshold && c.p99 > 2*c.p50 {
		shrink = true
	}
	if float64(s.QueueDepth)/limit > c.cfg.QueueRatio {
		shrink = true
	}
	if c.timeouts > 0 && c.completions > 0 &&
		float64(c.timeouts)/float64(c.timeouts+c.completions) > 0.02 {
		shrink = true
	}

	switch {
	case shrink:
		limit = limit * c.cfg.ShrinkFactor
		if limit < float64(c.cfg.MinLimit) {
			limit = float64(c.cfg.MinLimit)
		}
		c.limit = limit
		c.lastAdj = time.Now()
	case c.timeouts == 0 && s.DependencyErr == 0 && c.p99 <= c.cfg.LatencyThreshold:
		// Healthy window → additive increase, but not immediately after a
		// shrink (give the system one calm window to breathe).
		if time.Since(c.lastAdj) > c.cfg.Window {
			limit += float64(c.cfg.GrowthPerProbe)
			if limit > float64(c.cfg.MaxLimit) {
				limit = float64(c.cfg.MaxLimit)
			}
			c.limit = limit
			c.lastAdj = time.Now()
		}
	}
	c.timeouts, c.completions = 0, 0
}

// Admit decides whether one more unit of work may start now.
func (c *Controller) Admit(s Signals) bool {
	return s.InFlight < c.Limit()
}

// Snapshot exposes current state for observability.
type Snapshot struct {
	Limit      int
	P50        time.Duration
	P99        time.Duration
	LastShrink string
	LastAdjust time.Time
}

func (c *Controller) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{Limit: c.Limit(), P50: c.p50, P99: c.p99, LastAdjust: c.lastAdj}
}
