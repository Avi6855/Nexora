package txpolicy

import "testing"

func seedCurrent(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	if _, err := e.PutRule(Rule{ID: "sanctions", Priority: 1, Predicate: Predicate{MinDestinationRisk: intp(80)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSimulateDeltaMath(t *testing.T) {
	current := seedCurrent(t)
	candidate := NewEngine()
	if _, err := candidate.PutRule(Rule{ID: "sanctions", Priority: 1, Predicate: Predicate{MinDestinationRisk: intp(80)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}
	// Candidate adds a stricter amount rule on top.
	if _, err := candidate.PutRule(Rule{ID: "big-block", Priority: 5, Predicate: Predicate{MinAmountMinor: int64p(100000)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}

	contexts := []Context{
		{AmountMinor: 5000, DestinationRisk: 90},                    // blocked by both
		{AmountMinor: 200000, DestinationRisk: 10},                  // blocked by candidate only (low risk)
		{AmountMinor: 200000, DestinationRisk: 50},                  // blocked by candidate only
		{AmountMinor: 1000, DestinationRisk: 5, Category: "coffee"}, // allowed by both
	}
	res := Simulate(current, candidate, contexts)
	if res.CurrentBlocks != 1 {
		t.Fatalf("current blocks %d, want 1", res.CurrentBlocks)
	}
	if res.NewBlocks != 3 {
		t.Fatalf("new blocks %d, want 3", res.NewBlocks)
	}
	if res.Delta != 2 {
		t.Fatalf("delta %d, want 2", res.Delta)
	}
	// Only the low-risk newly blocked context counts as estimated FP.
	if res.FalsePositiveDelta != 1 {
		t.Fatalf("false positive delta %d, want 1", res.FalsePositiveDelta)
	}
	if res.PerRuleDiff["big-block"] != 2 {
		t.Fatalf("big-block diff %d, want 2", res.PerRuleDiff["big-block"])
	}
	if res.PerRuleDiff["sanctions"] != 0 {
		t.Fatalf("sanctions diff %d, want 0", res.PerRuleDiff["sanctions"])
	}
}

func TestSimulateRemovedRuleAndEmpty(t *testing.T) {
	current := seedCurrent(t)
	if _, err := current.PutRule(Rule{ID: "old-cap", Priority: 5, Predicate: Predicate{MinAmountMinor: int64p(1000)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}
	candidate := NewEngine()
	if _, err := candidate.PutRule(Rule{ID: "sanctions", Priority: 1, Predicate: Predicate{MinDestinationRisk: intp(80)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}
	contexts := []Context{
		{AmountMinor: 5000, DestinationRisk: 10},
		{AmountMinor: 50, DestinationRisk: 10},
	}
	res := Simulate(current, candidate, contexts)
	if res.CurrentBlocks != 1 || res.NewBlocks != 0 || res.Delta != -1 {
		t.Fatalf("removal math wrong: %+v", res)
	}
	if res.PerRuleDiff["old-cap"] != -1 {
		t.Fatalf("removed rule diff %d, want -1", res.PerRuleDiff["old-cap"])
	}
	// Empty batch and nil engines are safe.
	empty := Simulate(current, candidate, nil)
	if empty.CurrentBlocks != 0 || empty.NewBlocks != 0 || empty.Delta != 0 {
		t.Fatalf("empty batch must be all zeros: %+v", empty)
	}
	if empty.PerRuleDiff == nil {
		t.Fatal("per-rule diff map must be non-nil")
	}
	nils := Simulate(nil, nil, contexts)
	if nils.CurrentBlocks != 0 || nils.NewBlocks != 0 {
		t.Fatalf("nil engines must block nothing: %+v", nils)
	}
	// Simulation must not pollute audit logs.
	if len(current.Audit()) != 0 || len(candidate.Audit()) != 0 {
		t.Fatal("simulate must not write audit entries")
	}
}
