package invest

import (
	"testing"
	"time"

	sharedinvest "github.com/nexora/nexora/shared/investments"
)

func testTime() time.Time {
	return time.Date(2026, time.May, 1, 12, 0, 0, 0, time.UTC)
}

func TestContributeISA(t *testing.T) {
	svc := NewService()
	at := testTime()
	year := sharedinvest.TaxYear(at)
	if err := svc.ContributeISA(sharedinvest.Contribution{
		ID: "c1", OwnerID: "alice", Wrapper: sharedinvest.WrapperCash, AmountMinor: 500000, At: at,
	}); err != nil {
		t.Fatalf("admit: %v", err)
	}
	// Duplicate delivery is idempotent.
	if err := svc.ContributeISA(sharedinvest.Contribution{
		ID: "c1", OwnerID: "alice", Wrapper: sharedinvest.WrapperCash, AmountMinor: 500000, At: at,
	}); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	rem, _ := svc.RemainingAllowance("alice", year)
	if rem != AnnualAllowanceMinor-500000 {
		t.Fatalf("remaining = %d, want %d", rem, AnnualAllowanceMinor-500000)
	}
	// Correction releases the original amount.
	if err := svc.ContributeISA(sharedinvest.Contribution{
		ID: "c2", OwnerID: "alice", Wrapper: sharedinvest.WrapperCash, AmountMinor: 50000, At: at, CorrectedFrom: "c1",
	}); err != nil {
		t.Fatalf("correction: %v", err)
	}
	rem, _ = svc.RemainingAllowance("alice", year)
	if rem != AnnualAllowanceMinor-50000 {
		t.Fatalf("after correction remaining = %d, want %d", rem, AnnualAllowanceMinor-50000)
	}
	if err := svc.ContributeISA(sharedinvest.Contribution{
		ID: "c3", OwnerID: "alice", Wrapper: sharedinvest.WrapperSNS, AmountMinor: AnnualAllowanceMinor, At: at,
	}); err == nil {
		t.Fatalf("expected allowance exceeded")
	}
}

func TestJointGoal(t *testing.T) {
	svc := NewService()
	g, err := svc.CreateJointGoal("g1", "deposit", 1000000, []string{"alice", "bob"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.FundedMinor != 0 {
		t.Fatalf("funded = %d, want 0", g.FundedMinor)
	}
	if _, err := svc.ContributeJointGoal("g1", "alice", 400000); err != nil {
		t.Fatalf("contribute: %v", err)
	}
	if _, err := svc.ContributeJointGoal("g1", "mallory", 1000); err == nil {
		t.Fatalf("expected non-owner error")
	}
	if _, err := svc.ContributeJointGoal("g1", "bob", 700000); err == nil {
		t.Fatalf("expected overfund error")
	}
	if _, err := svc.ContributeJointGoal("missing", "alice", 1000); err == nil {
		t.Fatalf("expected goal not found")
	}
	if _, err := svc.CreateJointGoal("bad", "x", 100, []string{"solo"}); err == nil {
		t.Fatalf("expected two-owner validation error")
	}
}

func TestTaxLots(t *testing.T) {
	svc := NewService()
	at := testTime()
	lots := []sharedinvest.Lot{
		{ID: "l1", Symbol: "VUSA", UnitsMilli: 1000, CostBasisMinor: 10000, BoughtAt: at},
		{ID: "l2", Symbol: "VUSA", UnitsMilli: 1000, CostBasisMinor: 20000, BoughtAt: at.Add(time.Hour)},
	}
	if err := svc.OpenTaxLots("VUSA", sharedinvest.LotFIFO, lots); err != nil {
		t.Fatalf("open: %v", err)
	}
	res, err := svc.SellLots("VUSA", 1500, 15000)
	if err != nil {
		t.Fatalf("sell: %v", err)
	}
	if len(res.Disposals) != 2 {
		t.Fatalf("disposals = %d, want 2", len(res.Disposals))
	}
	if res.Disposals[0].LotID != "l1" {
		t.Fatalf("FIFO first lot = %s, want l1", res.Disposals[0].LotID)
	}
	if _, err := svc.SellLots("VUSA", 5000, 15000); err == nil {
		t.Fatalf("expected insufficient units")
	}
	if _, err := svc.SellLots("MISSING", 100, 1000); err == nil {
		t.Fatalf("expected engine not found")
	}
}

func TestCorporateAction(t *testing.T) {
	svc := NewService()
	at := testTime()
	out, res, err := svc.ApplyAction(
		sharedinvest.Holding{Symbol: "AAA", UnitsMilli: 1000, CostBasisMinor: 50000},
		sharedinvest.CorporateAction{Type: sharedinvest.ActionSplit, Symbol: "AAA", RatioNum: 2, RatioDen: 1, At: at},
	)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if out.UnitsMilli != 2000 || out.CostBasisMinor != 50000 {
		t.Fatalf("split out = %+v", out)
	}
	if res.UnitsBefore != 1000 || res.UnitsAfter != 2000 {
		t.Fatalf("split audit = %+v", res)
	}
	divOut, divRes, err := svc.ApplyAction(
		sharedinvest.Holding{Symbol: "AAA", UnitsMilli: 2000, CostBasisMinor: 50000},
		sharedinvest.CorporateAction{Type: sharedinvest.ActionDividend, Symbol: "AAA", PerUnitMinor: 100, At: at},
	)
	if err != nil {
		t.Fatalf("dividend: %v", err)
	}
	if divRes.CashMinor != 2000*100/1000 {
		t.Fatalf("dividend cash = %d", divRes.CashMinor)
	}
	if divOut.UnitsMilli != 2000 {
		t.Fatalf("dividend must not change units")
	}
	if _, _, err := svc.ApplyAction(
		sharedinvest.Holding{Symbol: "AAA", UnitsMilli: 1000, CostBasisMinor: 1000},
		sharedinvest.CorporateAction{Type: "BOGUS", Symbol: "AAA", At: at},
	); err == nil {
		t.Fatalf("expected unknown action error")
	}
	if _, _, err := svc.ApplyAction(
		sharedinvest.Holding{Symbol: "ZZZ", UnitsMilli: 0, CostBasisMinor: 0},
		sharedinvest.CorporateAction{Type: sharedinvest.ActionSplit, Symbol: "ZZZ", RatioNum: 2, RatioDen: 1, At: at},
	); err == nil {
		t.Fatalf("expected unknown holding error")
	}
}
