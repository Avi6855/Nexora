package concurrency

import (
	"testing"
	"time"
)

func TestAdmitWithinLimit(t *testing.T) {
	c := New(DefaultConfig())
	if !c.Admit(Signals{InFlight: 499}) {
		t.Fatal("in-flight below limit must be admitted")
	}
	if c.Admit(Signals{InFlight: 501}) {
		t.Fatal("in-flight above limit must be rejected")
	}
}

func TestShrinkOnDependencyErrors(t *testing.T) {
	c := New(DefaultConfig())
	start := c.Limit()
	// Force a window boundary and an unhealthy signal.
	c.mu.Lock()
	c.windowStart = time.Now().Add(-c.cfg.Window)
	c.mu.Unlock()
	c.Observe(Signals{DependencyErr: 0.5, Completions: 10})
	got := c.Limit()
	if got >= start {
		t.Fatalf("limit must shrink on dependency errors: %d → %d", start, got)
	}
	if got != start/2 {
		t.Fatalf("AIMD shrink must halve: got %d, want %d", got, start/2)
	}
}

func TestShrinkOnTailLatency(t *testing.T) {
	c := New(DefaultConfig())
	c.mu.Lock()
	c.windowStart = time.Now().Add(-c.cfg.Window)
	c.mu.Unlock()
	c.Observe(Signals{P50: 50 * time.Millisecond, P99: 3 * time.Second, Completions: 10})
	if c.Limit() >= 500 {
		t.Fatalf("p99 ≫ p50 must shrink, got %d", c.Limit())
	}
}

func TestGrowWhenHealthy(t *testing.T) {
	c := New(DefaultConfig())
	c.mu.Lock()
	c.limit = 100
	c.windowStart = time.Now().Add(-c.cfg.Window)
	c.mu.Unlock()
	c.Observe(Signals{P50: 20 * time.Millisecond, P99: 100 * time.Millisecond, Completions: 1000})
	if got := c.Limit(); got != 110 {
		t.Fatalf("healthy window must grow additively: got %d, want 110", got)
	}
}

func TestGrowWaitsOneCalmWindowAfterShrink(t *testing.T) {
	c := New(DefaultConfig())
	c.mu.Lock()
	c.windowStart = time.Now().Add(-c.cfg.Window)
	c.mu.Unlock()
	c.Observe(Signals{DependencyErr: 0.9, Completions: 5})
	shrunk := c.Limit()
	c.mu.Lock()
	c.windowStart = time.Now().Add(-c.cfg.Window)
	c.mu.Unlock()
	c.Observe(Signals{Completions: 100}) // healthy window right after shrink
	if c.Limit() != shrunk {
		t.Fatal("must not grow in the window immediately after a shrink")
	}
}

func TestLimitNeverBelowFloor(t *testing.T) {
	c := New(DefaultConfig())
	for i := 0; i < 20; i++ {
		c.mu.Lock()
		c.windowStart = time.Now().Add(-c.cfg.Window)
		c.mu.Unlock()
		c.Observe(Signals{DependencyErr: 1.0})
	}
	if c.Limit() < c.cfg.MinLimit {
		t.Fatalf("limit %d below floor %d", c.Limit(), c.cfg.MinLimit)
	}
}

func TestLimitNeverAboveCeiling(t *testing.T) {
	c := New(DefaultConfig())
	c.mu.Lock()
	c.limit = float64(c.cfg.MaxLimit - 1)
	c.windowStart = time.Now().Add(-c.cfg.Window)
	c.mu.Unlock()
	c.Observe(Signals{Completions: 100})
	if c.Limit() > c.cfg.MaxLimit {
		t.Fatalf("limit %d above ceiling %d", c.Limit(), c.cfg.MaxLimit)
	}
}
