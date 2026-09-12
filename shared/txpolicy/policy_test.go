package txpolicy

import (
	"errors"
	"testing"
)

func int64p(v int64) *int64 { return &v }
func intp(v int) *int       { return &v }
func boolp(v bool) *bool    { return &v }

func TestPutRuleVersions(t *testing.T) {
	e := NewEngine()
	r1, err := e.PutRule(Rule{ID: "sanctions", Priority: 1, Predicate: Predicate{Country: "KP"}, Effect: EffectBlock})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != 1 {
		t.Fatalf("first version %d, want 1", r1.Version)
	}
	r2, err := e.PutRule(Rule{ID: "sanctions", Priority: 1, Predicate: Predicate{Country: "KP"}, Effect: EffectRequireStepUp})
	if err != nil {
		t.Fatal(err)
	}
	if r2.Version != 2 {
		t.Fatalf("second version %d, want 2", r2.Version)
	}
	got, err := e.Get("sanctions")
	if err != nil || got.Effect != EffectRequireStepUp || got.Version != 2 {
		t.Fatalf("current must be v2 step-up: %+v %v", got, err)
	}
	if _, err = e.PutRule(Rule{ID: "", Effect: EffectBlock}); err == nil {
		t.Fatal("empty id must fail")
	}
	if _, err = e.PutRule(Rule{ID: "x", Effect: "nuke"}); err == nil {
		t.Fatal("unknown effect must fail")
	}
	if _, err = e.Get("missing"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("unknown rule must 404, got %v", err)
	}
}

func TestEvaluateOrderAndPredicates(t *testing.T) {
	e := NewEngine()
	if _, err := e.PutRule(Rule{ID: "catch-risky", Priority: 10, Predicate: Predicate{MinDestinationRisk: intp(70)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PutRule(Rule{ID: "big-new", Priority: 5, Predicate: Predicate{MinAmountMinor: int64p(100000), BeneficiaryNew: boolp(true)}, Effect: EffectRequireStepUp}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PutRule(Rule{ID: "gaming", Priority: 5, Predicate: Predicate{Category: "gaming", Country: "GB"}, Effect: EffectRequireStepUp}); err != nil {
		t.Fatal(err)
	}

	// Priority wins over insertion: big-new (5) beats catch-risky (10).
	res := e.Evaluate(Context{AmountMinor: 200000, BeneficiaryNew: true, DestinationRisk: 90})
	if !res.Matched || res.RuleID != "big-new" || res.Effect != EffectRequireStepUp {
		t.Fatalf("priority order broken: %+v", res)
	}
	// Same priority ties break by id: big-new < gaming.
	res = e.Evaluate(Context{AmountMinor: 200000, BeneficiaryNew: true, DestinationRisk: 10, Category: "Gaming", Country: "gb"})
	if res.RuleID != "big-new" {
		t.Fatalf("tie-break must pick lowest id, got %+v", res)
	}
	// Category/country match case-insensitively.
	res = e.Evaluate(Context{AmountMinor: 50, Category: "GAMING", Country: "GB"})
	if !res.Matched || res.RuleID != "gaming" {
		t.Fatalf("category/country match broken: %+v", res)
	}
	// No match defaults to approve.
	res = e.Evaluate(Context{AmountMinor: 50, DestinationRisk: 5, Category: "groceries", Country: "GB"})
	if res.Matched || res.Effect != EffectApprove {
		t.Fatalf("default must be unmatched approve: %+v", res)
	}
	// Audit log records every evaluation in order.
	audit := e.Audit()
	if len(audit) != 4 {
		t.Fatalf("audit length %d, want 4", len(audit))
	}
	if audit[0].RuleID != "big-new" || audit[3].Matched {
		t.Fatalf("audit entries out of order or wrong: %+v", audit)
	}
}

func TestRollbackRestoresBehavior(t *testing.T) {
	e := NewEngine()
	if _, err := e.PutRule(Rule{ID: "cap", Priority: 1, Predicate: Predicate{MinAmountMinor: int64p(50000)}, Effect: EffectBlock}); err != nil {
		t.Fatal(err)
	}
	ctx := Context{AmountMinor: 60000, DestinationRisk: 10}
	if res := e.Evaluate(ctx); res.Effect != EffectBlock {
		t.Fatalf("v1 must block: %+v", res)
	}
	if _, err := e.PutRule(Rule{ID: "cap", Priority: 1, Predicate: Predicate{MinAmountMinor: int64p(50000)}, Effect: EffectApprove}); err != nil {
		t.Fatal(err)
	}
	if res := e.Evaluate(ctx); res.Effect != EffectApprove {
		t.Fatalf("v2 must approve: %+v", res)
	}
	restored, err := e.Rollback("cap")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != 1 || restored.Effect != EffectBlock {
		t.Fatalf("rollback must restore v1 block: %+v", restored)
	}
	if res := e.Evaluate(ctx); res.Effect != EffectBlock || res.Version != 1 {
		t.Fatalf("behavior after rollback must be v1 block: %+v", res)
	}
	// Rolling back a single-version rule fails; unknown rules 404.
	if _, err = e.Rollback("cap"); !errors.Is(err, ErrNoPriorVersion) {
		t.Fatalf("second rollback must fail with no-prior-version, got %v", err)
	}
	if _, err = e.Rollback("missing"); !errors.Is(err, ErrRuleNotFound) {
		t.Fatalf("rollback unknown must 404, got %v", err)
	}
}
