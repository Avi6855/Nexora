package failover

import (
	"errors"
	"testing"
)

func testController() *Controller {
	c := NewController(Config{RPOLagMs: 1000, RTOSeconds: 60})
	_ = c.RegisterRegion("eu-west", RolePrimary)
	_ = c.RegisterRegion("eu-north", RoleStandby)
	return c
}

func TestFailoverOnUnhealthyPrimary(t *testing.T) {
	c := testController()
	if got := c.Route(); got.Route != RoutePrimary {
		t.Fatalf("expected PRIMARY, got %s", got.Route)
	}
	if err := c.ReportHealth("eu-west", false, 10); err != nil {
		t.Fatal(err)
	}
	got := c.Route()
	if got.Route != RouteFailover {
		t.Fatalf("expected FAILOVER, got %s", got.Route)
	}
	if got.Target != "eu-north" {
		t.Fatalf("expected failover target eu-north, got %q", got.Target)
	}
}

func TestReadOnlyWhenLagExceedsRPO(t *testing.T) {
	c := testController()
	if err := c.ReportHealth("eu-west", true, 5000); err != nil {
		t.Fatal(err)
	}
	got := c.Route()
	if got.Route != RouteReadOnlyDegraded {
		t.Fatalf("expected READ_ONLY_DEGRADED, got %s", got.Route)
	}
}

func TestStaleEpochWriteFenced(t *testing.T) {
	c := testController()
	cur := c.CurrentEpoch()
	newEpoch := c.AdvanceEpoch()
	if newEpoch != cur+1 {
		t.Fatalf("expected epoch %d, got %d", cur+1, newEpoch)
	}
	if err := c.CheckWrite(cur); !errors.Is(err, ErrFenced) {
		t.Fatalf("expected ErrFenced for stale epoch, got %v", err)
	}
	if err := c.CheckWrite(newEpoch); err != nil {
		t.Fatalf("expected current epoch to pass, got %v", err)
	}
	if _, err := c.ExecuteOp("eu-west", "op-1", cur); !errors.Is(err, ErrFenced) {
		t.Fatalf("expected fenced ExecuteOp, got %v", err)
	}
}

func TestDuplicateOpDeduped(t *testing.T) {
	c := testController()
	epoch := c.CurrentEpoch()
	first, err := c.ExecuteOp("eu-west", "tx-123", epoch)
	if err != nil || !first {
		t.Fatalf("expected first execute=true, got %v %v", first, err)
	}
	second, err := c.ExecuteOp("eu-north", "tx-123", epoch)
	if err != nil {
		t.Fatalf("dedupe must not error, got %v", err)
	}
	if second {
		t.Fatal("expected duplicate op to be deduped (executed=false)")
	}
}

func TestRecoveryDiff(t *testing.T) {
	c := testController()
	epoch := c.CurrentEpoch()
	_, _ = c.ExecuteOp("eu-west", "op-a", epoch)
	_, _ = c.ExecuteOp("eu-west", "op-b", epoch)
	// Simulate a standby-side op that never replicated: inject via standby region.
	_, _ = c.ExecuteOp("eu-north", "op-c", epoch)
	rep, err := c.RecoveryDiff("eu-west", "eu-north")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DivergedOps) == 0 {
		t.Fatal("expected diverged ops")
	}
	found := map[string]bool{}
	for _, op := range rep.DivergedOps {
		found[op] = true
	}
	for _, want := range []string{"op-a", "op-b", "op-c"} {
		if !found[want] {
			t.Fatalf("expected diverged to contain %s, got %v", want, rep.DivergedOps)
		}
	}
}
