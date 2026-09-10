package investments

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func taxYearDate(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func TestISAControlTower(t *testing.T) {
	tower := NewControlTower(2000000) // £20,000 allowance
	inApr := taxYearDate(2026, time.April, 10)
	if ty := TaxYear(inApr); ty != "2026/27" {
		t.Fatalf("tax year %s, want 2026/27", ty)
	}
	if ty := TaxYear(taxYearDate(2026, time.March, 31)); ty != "2025/26" {
		t.Fatalf("March is the OLD tax year: %s", ty)
	}
	// Contributions split across wrappers share one allowance.
	if err := tower.Admit(Contribution{ID: "c1", OwnerID: "o1", Wrapper: WrapperCash, AmountMinor: 800000, At: inApr}); err != nil {
		t.Fatal(err)
	}
	if err := tower.Admit(Contribution{ID: "c2", OwnerID: "o1", Wrapper: WrapperSNS, AmountMinor: 400000, At: inApr}); err != nil {
		t.Fatal(err)
	}
	if got := tower.Remaining("o1", "2026/27"); got != 800000 {
		t.Fatalf("remaining %d, want £8,000", got)
	}
	// Over-allowance refused.
	err := tower.Admit(Contribution{ID: "c3", OwnerID: "o1", Wrapper: WrapperCash, AmountMinor: 900000, At: inApr})
	if err == nil {
		t.Fatal("over-allowance must fail")
	}
	// Duplicate delivery is absorbed idempotently.
	if err := tower.Admit(Contribution{ID: "c2", OwnerID: "o1", Wrapper: WrapperSNS, AmountMinor: 400000, At: inApr}); err != nil {
		t.Fatalf("duplicate must be a no-op, not an error: %v", err)
	}
	if got := tower.Remaining("o1", "2026/27"); got != 800000 {
		t.Fatalf("duplicate must not consume allowance: %d", got)
	}
	// Correction releases the replaced amount.
	if err := tower.Admit(Contribution{ID: "c2-fix", OwnerID: "o1", Wrapper: WrapperSNS, AmountMinor: 100000, At: inApr, CorrectedFrom: "c2"}); err != nil {
		t.Fatal(err)
	}
	if got := tower.Remaining("o1", "2026/27"); got != 1100000 {
		t.Fatalf("after correction remaining %d, want £11,000", got)
	}
	// A new tax year resets the envelope (2027/28 starts 6 April 2027 —
	// 30 April 2026 would still be 2026/27).
	newYear := taxYearDate(2027, time.April, 10)
	if err := tower.Admit(Contribution{ID: "c4", OwnerID: "o1", Wrapper: WrapperCash, AmountMinor: 2000000, At: newYear}); err != nil {
		t.Fatalf("new tax year must be fresh: %v", err)
	}
	// Owners are isolated.
	if got := tower.Remaining("o2", "2026/27"); got != 2000000 {
		t.Fatalf("owner isolation broken: %d", got)
	}
}

func TestJointGoal(t *testing.T) {
	g, err := NewJointGoal("g1", "House deposit", 1000000, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Contribute("a", 400000); err != nil {
		t.Fatal(err)
	}
	if err := g.Contribute("b", 600000); err != nil {
		t.Fatal(err)
	}
	if g.FundedMinor != 1000000 {
		t.Fatalf("funded %d", g.FundedMinor)
	}
	// Goal overfunding blocked even though allowance may remain.
	if err := g.Contribute("a", 1); err == nil {
		t.Fatal("funded goal must refuse more")
	}
	// Non-owner refused.
	if err := g.Contribute("c", 100); err == nil {
		t.Fatal("non-owner must be refused")
	}
	// Owner validation.
	if _, err := NewJointGoal("g2", "x", 1000, []string{"a", "a"}); err == nil {
		t.Fatal("identical owners rejected")
	}
	if _, err := NewJointGoal("g3", "x", 1000, []string{"a"}); err == nil {
		t.Fatal("single owner rejected")
	}
	v := g.BalanceView()
	if v["a"] != 0.5 || v["b"] != 0.5 {
		t.Fatalf("goal shares are per-owner: %+v", v)
	}
}

func TestTaxLotEngine(t *testing.T) {
	lots := []Lot{
		{ID: "L1", Symbol: "VUSA", UnitsMilli: 1000, CostBasisMinor: 50000, BoughtAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}, // 50/unit
		{ID: "L2", Symbol: "VUSA", UnitsMilli: 1000, CostBasisMinor: 80000, BoughtAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}, // 80/unit
		{ID: "L3", Symbol: "VUSA", UnitsMilli: 1000, CostBasisMinor: 30000, BoughtAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)}, // 30/unit
	}
	price := int64(60000) // sell at 60/unit
	// FIFO: oldest first → L3 (30), L1 (50).
	fifo := NewTaxLotEngine(LotFIFO, lots)
	res, err := fifo.Sell("VUSA", 2000, price)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Disposals) != 2 || res.Disposals[0].LotID != "L3" || res.Disposals[1].LotID != "L1" {
		t.Fatalf("FIFO order wrong: %+v", res.Disposals)
	}
	// Proceeds 2000 × 60/1000 = 120000; cost 30000+50000=80000; gain 40000.
	if res.TotalProceedsMinor != 120000 || res.TotalGainMinor != 40000 {
		t.Fatalf("FIFO numbers: %+v", res)
	}
	fifoRes := res
	// LIFO: newest first → L2 (80), L1 (50).
	lifo := NewTaxLotEngine(LotLIFO, lots)
	res, _ = lifo.Sell("VUSA", 2000, price)
	if res.Disposals[0].LotID != "L2" {
		t.Fatalf("LIFO order wrong: %+v", res.Disposals)
	}
	// HIFO: highest per-unit basis first → L2 (80), L1 (50) = 130k cost,
	// vs FIFO's L3(30)+L1(50) = 80k cost. Same proceeds, so HIFO gains less:
	// −10000 vs +40000 (a loss vs a gain).
	hifo := NewTaxLotEngine(LotHIFO, lots)
	resH, _ := hifo.Sell("VUSA", 2000, price)
	if resH.TotalGainMinor >= fifoRes.TotalGainMinor {
		t.Fatalf("HIFO should minimise gain vs FIFO: HIFO %d vs FIFO %d", resH.TotalGainMinor, fifoRes.TotalGainMinor)
	}
	// Partial-lot consumption is proportional.
	partial := NewTaxLotEngine(LotFIFO, lots)
	resP, _ := partial.Sell("VUSA", 500, price)
	if len(resP.Disposals) != 1 || resP.Disposals[0].LotID != "L3" {
		t.Fatalf("partial lot: %+v", resP.Disposals)
	}
	// 500 units of L3: cost = 500 × 30/1000 = 15000.
	if resP.Disposals[0].CostMinor != 15000 {
		t.Fatalf("proportional basis %d, want 15000", resP.Disposals[0].CostMinor)
	}
	// Overselling fails.
	if _, err := fifo.Sell("VUSA", 99999, price); err == nil {
		t.Fatal("insufficient units must fail")
	}
}

func TestCorporateActions(t *testing.T) {
	h := Holding{Symbol: "ACME", UnitsMilli: 2000, CostBasisMinor: 100000} // 200 units @ £50

	// 2:1 split: units double, total basis unchanged.
	out, ar, err := ApplyCorporateAction(h, CorporateAction{Type: ActionSplit, Symbol: "ACME", RatioNum: 2, RatioDen: 1, At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if out.UnitsMilli != 4000 || out.CostBasisMinor != 100000 {
		t.Fatalf("split must preserve total basis: %+v", out)
	}
	if ar.UnitsBefore != 2000 || ar.UnitsAfter != 4000 {
		t.Fatalf("audit wrong: %+v", ar)
	}

	// Merger: ACME → NEWCO at 1:2 (two old = one new), basis carried.
	out, ar, err = ApplyCorporateAction(h, CorporateAction{Type: ActionMerger, Symbol: "ACME", NewSymbol: "NEWCO", RatioNum: 1, RatioDen: 2, At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if out.Symbol != "NEWCO" || out.UnitsMilli != 1000 || out.CostBasisMinor != 100000 {
		t.Fatalf("merger wrong: %+v", out)
	}
	if ar.Symbol != "NEWCO" {
		t.Fatalf("audit symbol: %+v", ar)
	}

	// Ticker change: pure rename.
	out, ar, _ = ApplyCorporateAction(h, CorporateAction{Type: ActionTicker, Symbol: "ACME", NewSymbol: "ACME2", At: t0})
	if out.Symbol != "ACME2" || out.UnitsMilli != 2000 {
		t.Fatalf("ticker change wrong: %+v", out)
	}
	if ar.Note == "" {
		t.Fatal("audit note required")
	}

	// Dividend: £5.00/unit (500 minor) × 2 units = £10.00 = 1000 minor cash;
	// holding untouched.
	out, ar, err = ApplyCorporateAction(h, CorporateAction{Type: ActionDividend, Symbol: "ACME", PerUnitMinor: 500, At: t0})
	if err != nil {
		t.Fatal(err)
	}
	if out.UnitsMilli != 2000 || ar.CashMinor != 1000 {
		t.Fatalf("dividend wrong: %+v %+v", out, ar)
	}
	// Invalid actions rejected.
	if _, _, err := ApplyCorporateAction(h, CorporateAction{Type: "WEIRD", At: t0}); err == nil {
		t.Fatal("unknown action must fail")
	}
	if _, _, err := ApplyCorporateAction(h, CorporateAction{Type: ActionSplit, RatioNum: 0, RatioDen: 1, At: t0}); err == nil {
		t.Fatal("invalid ratio must fail")
	}
	if ActionAudit(ar) == "" {
		t.Fatal("audit line required")
	}
}
