package economics

import (
	"math"
	"testing"
)

func costData() []ServiceCost {
	return []ServiceCost{
		{Service: "fraud", MonthlyCostGBP: 20_000, MonthlyRequests: 10_000_000},
		{Service: "ledger", MonthlyCostGBP: 50_000, MonthlyRequests: 100_000_000},
		{Service: "cards", MonthlyCostGBP: 30_000, MonthlyRequests: 50_000_000},
	}
}

func TestAttributeJourney(t *testing.T) {
	j := Journey{Name: "card_payment", MonthlyVolume: 20_000_000, Steps: []JourneyStep{
		{Service: "cards", RequestsPerJourney: 2},
		{Service: "fraud", RequestsPerJourney: 1},
		{Service: "ledger", RequestsPerJourney: 3},
	}}
	bd, err := AttributeJourney(j, costData())
	if err != nil {
		t.Fatalf("attribute: %v", err)
	}
	// cards: £0.0006/req ×2 = 0.0012; fraud: £0.002 ×1; ledger: £0.0005 ×3.
	want := 0.0012 + 0.002 + 0.0015
	if math.Abs(bd.CostPerJourneyGBP-want) > 1e-9 {
		t.Fatalf("per-journey %.6f, want %.6f", bd.CostPerJourneyGBP, want)
	}
	if bd.TopCostDriver != "fraud" {
		t.Fatalf("fraud (47%% of cost) must be the driver: %s %+v", bd.TopCostDriver, bd.PerService)
	}
	if math.Abs(bd.MonthlyCostGBP-want*20_000_000) > 1 {
		t.Fatalf("monthly %.0f", bd.MonthlyCostGBP)
	}
}

func TestAttributeJourneyValidates(t *testing.T) {
	if _, err := AttributeJourney(Journey{Name: "j", Steps: []JourneyStep{{Service: "ghost", RequestsPerJourney: 1}}}, costData()); err == nil {
		t.Fatal("journey using uncosted service must error")
	}
	bad := []ServiceCost{{Service: "x", MonthlyCostGBP: 100, MonthlyRequests: 0}}
	if _, err := AttributeJourney(Journey{Name: "j", Steps: []JourneyStep{{Service: "x", RequestsPerJourney: 1}}}, bad); err == nil {
		t.Fatal("zero-volume service must error (infinite unit cost)")
	}
}

func TestRankJourneys(t *testing.T) {
	ranked := RankJourneys([]CostBreakdown{
		{Journey: "small", MonthlyCostGBP: 100},
		{Journey: "big", MonthlyCostGBP: 9_000},
		{Journey: "mid", MonthlyCostGBP: 1_000},
	})
	if ranked[0].Journey != "big" || ranked[2].Journey != "small" {
		t.Fatalf("ranking wrong: %+v", ranked)
	}
}

func tiers() []AvailabilityTier {
	return []AvailabilityTier{
		{Nines: 99.9, InfraCostGBPMo: 2_000},
		{Nines: 99.95, InfraCostGBPMo: 4_000},
		{Nines: 99.99, InfraCostGBPMo: 12_000},
		{Nines: 99.999, InfraCostGBPMo: 40_000},
	}
}

func TestOptimiseReliabilityCriticalService(t *testing.T) {
	// Critical payments service: downtime is expensive → higher nines win.
	m := ImpactModel{CustomersPerMinute: 50_000, SupportCostPerCustomer: 0.10,
		ChurnPerMinute: 0.001, LTVGBP: 800, ReputationalGBP: 20_000}
	dec, err := OptimiseReliability(tiers(), m)
	if err != nil {
		t.Fatalf("optimise: %v", err)
	}
	// 99.9% = 43.2 min downtime → impact ≈ 43.2×50000×(0.10+0.8) + 20000 ≈ £1.96M.
	// Even 40k infra for 5 nines beats that.
	if dec.Recommended.Nines != 99.999 {
		t.Fatalf("critical service should buy max nines: %+v", dec.Reasoning)
	}
}

func TestOptimiseReliabilityInternalTool(t *testing.T) {
	// Internal admin tool: 50 users, no churn → cheap tier wins.
	m := ImpactModel{CustomersPerMinute: 50, SupportCostPerCustomer: 0.0,
		ChurnPerMinute: 0.0, LTVGBP: 0, ReputationalGBP: 0}
	dec, err := OptimiseReliability(tiers(), m)
	if err != nil {
		t.Fatalf("optimise: %v", err)
	}
	if dec.Recommended.Nines != 99.9 {
		t.Fatalf("internal tool should stay cheap: %s", dec.Reasoning)
	}
}

func TestOptimiseReliabilityZeroDowntimeTier(t *testing.T) {
	// A tier declared with explicit zero downtime (active/active).
	tt := []AvailabilityTier{
		{Nines: 99.9, InfraCostGBPMo: 2_000},
		{Nines: 100, InfraCostGBPMo: 100_000, DowntimeMinutes: 0},
	}
	m := ImpactModel{CustomersPerMinute: 1_000, SupportCostPerCustomer: 0.01,
		ChurnPerMinute: 0.0001, LTVGBP: 100, ReputationalGBP: 5_000}
	dec, err := OptimiseReliability(tt, m)
	if err != nil {
		t.Fatalf("optimise: %v", err)
	}
	// 99.9 impact ≈ 43.2×1000×(0.01+0.01)+5000 ≈ £5.9k < £100k → cheap tier.
	if dec.Recommended.Nines != 99.9 {
		t.Fatalf("zero-downtime tier at 100k/mo must lose here: %s", dec.Reasoning)
	}
}

func TestOptimiseReliabilityEmpty(t *testing.T) {
	if _, err := OptimiseReliability(nil, ImpactModel{}); err == nil {
		t.Fatal("no tiers must error")
	}
}
