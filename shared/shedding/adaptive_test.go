package shedding

import (
	"testing"
)

func healthySample() Sample { return Sample{LatencyP99Ms: 50, ErrorRatePct: 0.1, RPS: 10} }

func TestAdaptiveEscalationOnP99Breach(t *testing.T) {
	c := NewAdaptiveController(DefaultAdaptiveConfig())
	if got := c.Tier(); got != TierNormal {
		t.Fatalf("expected NORMAL, got %s", got)
	}
	c.Observe(Sample{LatencyP99Ms: 600, ErrorRatePct: 0.1, RPS: 10})
	if got := c.Tier(); got != TierDegraded {
		t.Fatalf("expected DEGRADED after p99 breach, got %s", got)
	}
	c.Observe(Sample{LatencyP99Ms: 1500, ErrorRatePct: 0.1, RPS: 10})
	if got := c.Tier(); got != TierCritical {
		t.Fatalf("expected CRITICAL after p99 critical breach, got %s", got)
	}
}

func TestAdaptiveRecoveryHysteresis(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.RecoverySamples = 3
	c := NewAdaptiveController(cfg)
	c.Observe(Sample{LatencyP99Ms: 1500})
	if got := c.Tier(); got != TierCritical {
		t.Fatalf("expected CRITICAL, got %s", got)
	}
	// One healthy sample must NOT recover (hysteresis).
	c.Observe(healthySample())
	if got := c.Tier(); got != TierCritical {
		t.Fatalf("expected hysteresis to hold CRITICAL, got %s", got)
	}
	c.Observe(healthySample())
	c.Observe(healthySample())
	if got := c.Tier(); got != TierDegraded {
		t.Fatalf("expected step-down to DEGRADED, got %s", got)
	}
	// Needs another full window to reach NORMAL.
	c.Observe(healthySample())
	if got := c.Tier(); got != TierDegraded {
		t.Fatalf("expected to remain DEGRADED, got %s", got)
	}
	c.Observe(healthySample())
	c.Observe(healthySample())
	if got := c.Tier(); got != TierNormal {
		t.Fatalf("expected NORMAL, got %s", got)
	}
}

func TestAdaptiveClassPolicies(t *testing.T) {
	c := NewAdaptiveController(DefaultAdaptiveConfig())
	bad := Sample{LatencyP99Ms: 1500}
	if d := c.Decide(ClassCritical, bad); d.Action != ActionAllow {
		t.Fatalf("critical must be kept, got %s", d.Action)
	}
	c2 := NewAdaptiveController(DefaultAdaptiveConfig())
	if d := c2.Decide(ClassAnalytics, bad); d.Action != ActionDrop {
		t.Fatalf("analytics must drop when degraded, got %s", d.Action)
	}
	c3 := NewAdaptiveController(DefaultAdaptiveConfig())
	if d := c3.Decide(ClassDeferrable, bad); d.Action != ActionDelay {
		t.Fatalf("deferrable must delay, got %s", d.Action)
	}
	c4 := NewAdaptiveController(DefaultAdaptiveConfig())
	if d := c4.Decide(ClassNotifications, bad); d.Action != ActionQueue {
		t.Fatalf("notifications must queue, got %s", d.Action)
	}
}

func TestAdaptiveQueueCapOverflowDrops(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.QueueCap = 2
	c := NewAdaptiveController(cfg)
	bad := Sample{LatencyP99Ms: 1500}
	for i := 0; i < 2; i++ {
		if d := c.Decide(ClassDeferrable, bad); d.Action != ActionDelay {
			t.Fatalf("expected delay %d, got %s", i, d.Action)
		}
	}
	if d := c.Decide(ClassDeferrable, bad); d.Action != ActionDrop {
		t.Fatalf("expected drop on queue overflow, got %s", d.Action)
	}
}
