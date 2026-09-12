// Adaptive load shedding extends the passive shedding middleware with a
// tier controller (NORMAL → DEGRADED → CRITICAL) driven by dependency
// latency/error samples and RPS, plus per-class policy:
//
//	critical     = keep (never shed)
//	deferrable   = delay with queue cap (overflow drops)
//	analytics    = drop when degraded
//	notifications = queue with cap (overflow drops)
package shedding

import (
	"fmt"
	"sync"
)

// Tier is the controller's load tier.
type Tier string

const (
	TierNormal   Tier = "NORMAL"
	TierDegraded Tier = "DEGRADED"
	TierCritical Tier = "CRITICAL"
)

// Class is the request class under policy.
type Class string

const (
	ClassCritical      Class = "critical"
	ClassDeferrable    Class = "deferrable"
	ClassAnalytics     Class = "analytics"
	ClassNotifications Class = "notifications"
)

// Action is the shedding decision.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDelay Action = "delay"
	ActionDrop  Action = "drop"
	ActionQueue Action = "queue"
)

// Sample is one dependency/RPS observation.
type Sample struct {
	LatencyP99Ms float64 `json:"latency_p99_ms"`
	ErrorRatePct float64 `json:"error_rate_pct"`
	RPS          float64 `json:"rps"`
}

// AdaptiveConfig holds escalation thresholds and queue policy.
type AdaptiveConfig struct {
	DegradedP99Ms    float64 `json:"degraded_p99_ms"`
	CriticalP99Ms    float64 `json:"critical_p99_ms"`
	DegradedErrorPct float64 `json:"degraded_error_pct"`
	CriticalErrorPct float64 `json:"critical_error_pct"`
	DegradedRPS      float64 `json:"degraded_rps"`
	CriticalRPS      float64 `json:"critical_rps"`
	// RecoverySamples is the consecutive healthy samples required to step
	// down one tier (hysteresis against flapping).
	RecoverySamples int `json:"recovery_samples"`
	// QueueCap bounds deferrable/notification queues; overflow drops.
	QueueCap int `json:"queue_cap"`
}

// DefaultAdaptiveConfig returns banking-sensible thresholds.
func DefaultAdaptiveConfig() AdaptiveConfig {
	return AdaptiveConfig{
		DegradedP99Ms:    500,
		CriticalP99Ms:    1000,
		DegradedErrorPct: 1,
		CriticalErrorPct: 5,
		DegradedRPS:      1000,
		CriticalRPS:      2000,
		RecoverySamples:  3,
		QueueCap:         100,
	}
}

// Decision is the per-class shedding answer.
type Decision struct {
	Action Action `json:"action"`
	Tier   Tier   `json:"tier"`
	Class  Class  `json:"class"`
	Reason string `json:"reason"`
	Queued int    `json:"queued"`
}

// Controller tracks the tier and per-class queues.
type Controller struct {
	mu            sync.Mutex
	cfg           AdaptiveConfig
	tier          Tier
	healthyStreak int
	queued        map[Class]int
}

// NewAdaptiveController builds a controller in NORMAL.
func NewAdaptiveController(cfg AdaptiveConfig) *Controller {
	def := DefaultAdaptiveConfig()
	if cfg.DegradedP99Ms <= 0 {
		cfg.DegradedP99Ms = def.DegradedP99Ms
	}
	if cfg.CriticalP99Ms <= 0 {
		cfg.CriticalP99Ms = def.CriticalP99Ms
	}
	if cfg.DegradedErrorPct <= 0 {
		cfg.DegradedErrorPct = def.DegradedErrorPct
	}
	if cfg.CriticalErrorPct <= 0 {
		cfg.CriticalErrorPct = def.CriticalErrorPct
	}
	if cfg.DegradedRPS <= 0 {
		cfg.DegradedRPS = def.DegradedRPS
	}
	if cfg.CriticalRPS <= 0 {
		cfg.CriticalRPS = def.CriticalRPS
	}
	if cfg.RecoverySamples <= 0 {
		cfg.RecoverySamples = def.RecoverySamples
	}
	if cfg.QueueCap <= 0 {
		cfg.QueueCap = def.QueueCap
	}
	return &Controller{cfg: cfg, tier: TierNormal, queued: map[Class]int{}}
}

// Config returns a copy of the active config.
func (c *Controller) Config() AdaptiveConfig {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// UpdateConfig replaces thresholds/queue caps (queues are preserved).
func (c *Controller) UpdateConfig(cfg AdaptiveConfig) {
	def := DefaultAdaptiveConfig()
	c.mu.Lock()
	defer c.mu.Unlock()
	if cfg.DegradedP99Ms > 0 {
		c.cfg.DegradedP99Ms = cfg.DegradedP99Ms
	}
	if cfg.CriticalP99Ms > 0 {
		c.cfg.CriticalP99Ms = cfg.CriticalP99Ms
	}
	if cfg.DegradedErrorPct > 0 {
		c.cfg.DegradedErrorPct = cfg.DegradedErrorPct
	}
	if cfg.CriticalErrorPct > 0 {
		c.cfg.CriticalErrorPct = cfg.CriticalErrorPct
	}
	if cfg.DegradedRPS > 0 {
		c.cfg.DegradedRPS = cfg.DegradedRPS
	}
	if cfg.CriticalRPS > 0 {
		c.cfg.CriticalRPS = cfg.CriticalRPS
	}
	if cfg.RecoverySamples > 0 {
		c.cfg.RecoverySamples = cfg.RecoverySamples
	}
	if cfg.QueueCap > 0 {
		c.cfg.QueueCap = cfg.QueueCap
	}
	_ = def
}

// Tier returns the current tier.
func (c *Controller) Tier() Tier {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tier
}

// QueuedDepth returns the queued count for a class.
func (c *Controller) QueuedDepth(class Class) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queued[class]
}

// Observe ingests one sample. Escalation is immediate; recovery steps down
// one tier per RecoverySamples consecutive healthy samples.
func (c *Controller) Observe(s Sample) Tier {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observeLocked(s)
}

func (c *Controller) observeLocked(s Sample) Tier {
	critical := s.LatencyP99Ms >= c.cfg.CriticalP99Ms ||
		s.ErrorRatePct >= c.cfg.CriticalErrorPct ||
		s.RPS >= c.cfg.CriticalRPS
	if critical {
		c.tier = TierCritical
		c.healthyStreak = 0
		return c.tier
	}
	degraded := s.LatencyP99Ms >= c.cfg.DegradedP99Ms ||
		s.ErrorRatePct >= c.cfg.DegradedErrorPct ||
		s.RPS >= c.cfg.DegradedRPS
	if degraded {
		if c.tier == TierNormal {
			c.tier = TierDegraded
		}
		c.healthyStreak = 0
		return c.tier
	}
	c.healthyStreak++
	if c.healthyStreak >= c.cfg.RecoverySamples {
		switch c.tier {
		case TierCritical:
			c.tier = TierDegraded
		case TierDegraded:
			c.tier = TierNormal
		}
		c.healthyStreak = 0
	}
	return c.tier
}

// Decide records the sample, then applies the per-class policy.
func (c *Controller) Decide(class Class, s Sample) Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observeLocked(s)
	switch class {
	case ClassCritical:
		return Decision{Action: ActionAllow, Tier: c.tier, Class: class,
			Reason: "critical traffic is never shed", Queued: c.queued[class]}
	case ClassAnalytics:
		if c.tier == TierNormal {
			return Decision{Action: ActionAllow, Tier: c.tier, Class: class,
				Reason: "tier NORMAL: analytics allowed", Queued: c.queued[class]}
		}
		return Decision{Action: ActionDrop, Tier: c.tier, Class: class,
			Reason: fmt.Sprintf("tier %s: analytics dropped to protect critical paths", c.tier),
			Queued: c.queued[class]}
	case ClassDeferrable:
		if c.tier == TierNormal {
			return Decision{Action: ActionAllow, Tier: c.tier, Class: class,
				Reason: "tier NORMAL: deferrable allowed", Queued: c.queued[class]}
		}
		if c.queued[class] >= c.cfg.QueueCap {
			return Decision{Action: ActionDrop, Tier: c.tier, Class: class,
				Reason: fmt.Sprintf("tier %s: deferrable queue cap %d reached; dropping", c.tier, c.cfg.QueueCap),
				Queued: c.queued[class]}
		}
		c.queued[class]++
		return Decision{Action: ActionDelay, Tier: c.tier, Class: class,
			Reason: fmt.Sprintf("tier %s: deferrable delayed (queue %d/%d)", c.tier, c.queued[class], c.cfg.QueueCap),
			Queued: c.queued[class]}
	case ClassNotifications:
		if c.tier == TierNormal {
			return Decision{Action: ActionAllow, Tier: c.tier, Class: class,
				Reason: "tier NORMAL: notifications allowed", Queued: c.queued[class]}
		}
		if c.queued[class] >= c.cfg.QueueCap {
			return Decision{Action: ActionDrop, Tier: c.tier, Class: class,
				Reason: fmt.Sprintf("tier %s: notification queue cap %d reached; dropping", c.tier, c.cfg.QueueCap),
				Queued: c.queued[class]}
		}
		c.queued[class]++
		return Decision{Action: ActionQueue, Tier: c.tier, Class: class,
			Reason: fmt.Sprintf("tier %s: notifications queued (%d/%d)", c.tier, c.queued[class], c.cfg.QueueCap),
			Queued: c.queued[class]}
	default:
		return Decision{Action: ActionDrop, Tier: c.tier, Class: class,
			Reason: fmt.Sprintf("unknown class %q; dropping", string(class)), Queued: 0}
	}
}
