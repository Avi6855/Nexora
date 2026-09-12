package merchants

import (
	"testing"
	"time"
)

func observe(t *testing.T, g *Graph, name string, amount int64, refunded bool) {
	t.Helper()
	if _, err := g.Observe(name, "London", amount, refunded, time.Now().UTC()); err != nil {
		t.Fatalf("observe %q failed: %v", name, err)
	}
}

func TestAMZNVariantsResolveToOneCanonical(t *testing.T) {
	g := NewGraph()
	observe(t, g, "AMZN MKTP UK", 2500, false)
	observe(t, g, "AMAZON.CO.UK", 3000, false)
	observe(t, g, "Amzn Prime", 799, false)
	observe(t, g, "amazon marketplace", 1200, false)
	for _, variant := range []string{"AMZN MKTP UK", "AMAZON.CO.UK", "Amzn Prime", "amazon marketplace", "AMZN"} {
		n, err := g.Resolve(variant)
		if err != nil {
			t.Fatalf("resolve %q failed: %v", variant, err)
		}
		if n.Canonical != "AMAZON" {
			t.Fatalf("variant %q canonical = %s, want AMAZON", variant, n.Canonical)
		}
		if n.Brand != "Amazon" || n.Category != "RETAIL" || n.Parent != "AMAZON GROUP" {
			t.Fatalf("brand chain wrong: %+v", n)
		}
	}
	first, _ := g.Resolve("AMZN MKTP UK")
	second, _ := g.Resolve("AMAZON.CO.UK")
	if first.ID != second.ID {
		t.Fatalf("variants must share one node: %s vs %s", first.ID, second.ID)
	}
	if len(first.Aliases) < 4 {
		t.Fatalf("expected 4 aliases, got %v", first.Aliases)
	}
}

func TestAddAliasAndRiskFlags(t *testing.T) {
	g := NewGraph()
	observe(t, g, "Tesco Stores 1234", 4500, false)
	n, err := g.AddAlias("TESCO", "Tesco Express Camden")
	if err != nil {
		t.Fatalf("add alias failed: %v", err)
	}
	resolved, err := g.Resolve("Tesco Express Camden")
	if err != nil {
		t.Fatalf("resolve alias failed: %v", err)
	}
	if resolved.ID != n.ID || resolved.Canonical != "TESCO" {
		t.Fatalf("alias must resolve to TESCO: %+v", resolved)
	}
	flagged, err := g.FlagRisk("TESCO", "HIGH_REFUND_RATE")
	if err != nil {
		t.Fatalf("flag failed: %v", err)
	}
	if len(flagged.RiskFlags) != 1 || flagged.RiskFlags[0] != "HIGH_REFUND_RATE" {
		t.Fatalf("flags wrong: %+v", flagged.RiskFlags)
	}
	// Idempotent re-flag.
	if _, err := g.FlagRisk("TESCO", "HIGH_REFUND_RATE"); err != nil {
		t.Fatalf("re-flag failed: %v", err)
	}
	got, _ := g.Get("TESCO")
	if len(got.RiskFlags) != 1 {
		t.Fatalf("duplicate flag stored: %+v", got.RiskFlags)
	}
}

func TestRefundStatsMath(t *testing.T) {
	g := NewGraph()
	observe(t, g, "Costa Coffee", 350, false)
	observe(t, g, "COSTA", 350, true)
	observe(t, g, "Costa Ltd", 400, true)
	observe(t, g, "Costa", 300, false)
	stats, err := g.RefundStats("COSTA")
	if err != nil {
		t.Fatalf("refund stats failed: %v", err)
	}
	if stats.Total != 4 || stats.Refunded != 2 {
		t.Fatalf("stats wrong: %+v", stats)
	}
	if stats.RefundRate != 0.5 {
		t.Fatalf("rate = %v, want 0.5", stats.RefundRate)
	}
	if stats.RefundedMinor != 750 {
		t.Fatalf("refunded minor = %d, want 750", stats.RefundedMinor)
	}
}

func TestSubscriptionsDetection(t *testing.T) {
	g := NewGraph()
	observe(t, g, "Netflix", 1599, false)
	observe(t, g, "NETFLIX", 1599, false)
	if subs := g.Subscriptions(); len(subs) != 0 {
		t.Fatalf("2 charges must not be a subscription: %+v", subs)
	}
	observe(t, g, "Netflix UK", 1599, false)
	subs := g.Subscriptions()
	if len(subs) != 1 || subs[0].Canonical != "NETFLIX" || subs[0].Charges != 3 {
		t.Fatalf("subscription wrong: %+v", subs)
	}
}
