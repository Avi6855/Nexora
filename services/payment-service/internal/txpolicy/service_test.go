package txpolicy

import (
	"testing"

	sharedtx "github.com/nexora/nexora/shared/txpolicy"
)

func int64p(v int64) *int64 { return &v }
func intp(v int) *int       { return &v }

func TestServicePutEvaluateAuditRollback(t *testing.T) {
	s := NewService()

	r1, err := s.PutRule(sharedtx.Rule{
		ID:        "cap",
		Priority:  1,
		Predicate: sharedtx.Predicate{MinAmountMinor: int64p(50000)},
		Effect:    sharedtx.EffectBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if r1.Version != 1 {
		t.Fatalf("version %d, want 1", r1.Version)
	}

	res := s.Evaluate(sharedtx.Context{AmountMinor: 60000, DestinationRisk: 10})
	if !res.Matched || res.Effect != sharedtx.EffectBlock || res.RuleID != "cap" {
		t.Fatalf("must block via cap: %+v", res)
	}
	res = s.Evaluate(sharedtx.Context{AmountMinor: 100, DestinationRisk: 5})
	if res.Matched || res.Effect != sharedtx.EffectApprove {
		t.Fatalf("small payment must approve: %+v", res)
	}
	if audit := s.Audit(); len(audit) != 2 {
		t.Fatalf("audit length %d, want 2", len(audit))
	}

	// Loosen to approve, then roll back to the blocking version.
	r2, err := s.PutRule(sharedtx.Rule{
		ID:        "cap",
		Priority:  1,
		Predicate: sharedtx.Predicate{MinAmountMinor: int64p(50000)},
		Effect:    sharedtx.EffectApprove,
	})
	if err != nil || r2.Version != 2 {
		t.Fatalf("v2 wrong: %+v %v", r2, err)
	}
	if res := s.Evaluate(sharedtx.Context{AmountMinor: 60000}); res.Effect != sharedtx.EffectApprove {
		t.Fatalf("v2 must approve: %+v", res)
	}
	restored, err := s.Rollback("cap")
	if err != nil || restored.Version != 1 {
		t.Fatalf("rollback must restore v1: %+v %v", restored, err)
	}
	if res := s.Evaluate(sharedtx.Context{AmountMinor: 60000}); res.Effect != sharedtx.EffectBlock {
		t.Fatalf("post-rollback must block: %+v", res)
	}

	if _, err = s.Rollback("missing"); err == nil {
		t.Fatal("rollback unknown must fail")
	}
	if _, err = s.Rollback("cap"); err == nil {
		t.Fatal("rollback single-version must fail")
	}
	if _, err = s.PutRule(sharedtx.Rule{ID: "", Effect: sharedtx.EffectBlock}); err == nil {
		t.Fatal("empty id must fail")
	}
}

func TestServiceSimulate(t *testing.T) {
	s := NewService()
	if _, err := s.PutRule(sharedtx.Rule{
		ID:        "sanctions",
		Priority:  1,
		Predicate: sharedtx.Predicate{MinDestinationRisk: intp(80)},
		Effect:    sharedtx.EffectBlock,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := s.Simulate([]sharedtx.Rule{
		{
			ID:        "sanctions",
			Priority:  1,
			Predicate: sharedtx.Predicate{MinDestinationRisk: intp(80)},
			Effect:    sharedtx.EffectBlock,
		},
		{
			ID:        "big-block",
			Priority:  5,
			Predicate: sharedtx.Predicate{MinAmountMinor: int64p(100000)},
			Effect:    sharedtx.EffectBlock,
		},
	}, []sharedtx.Context{
		{AmountMinor: 5000, DestinationRisk: 90},
		{AmountMinor: 200000, DestinationRisk: 10},
		{AmountMinor: 1000, DestinationRisk: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CurrentBlocks != 1 || res.NewBlocks != 2 || res.Delta != 1 {
		t.Fatalf("delta math wrong: %+v", res)
	}
	if res.FalsePositiveDelta != 1 {
		t.Fatalf("false positive delta %d, want 1", res.FalsePositiveDelta)
	}
	if res.PerRuleDiff["big-block"] != 1 {
		t.Fatalf("big-block diff %d, want 1", res.PerRuleDiff["big-block"])
	}

	if _, err = s.Simulate([]sharedtx.Rule{{ID: "", Effect: sharedtx.EffectBlock}}, nil); err == nil {
		t.Fatal("candidate without id must fail")
	}
}
