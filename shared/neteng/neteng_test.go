package neteng

import (
	"math"
	"testing"
	"time"
)

func TestPathHealthHealthy(t *testing.T) {
	now := time.Now()
	var samples []PathSample
	for i := 0; i < 100; i++ {
		samples = append(samples, PathSample{At: now, LatencyMs: 20, PacketLossPct: 0.01})
	}
	h := ScorePath("aws→gcp", samples, DefaultPathThresholds())
	if h.State != "HEALTHY" || h.Score < 85 {
		t.Fatalf("healthy path scored %s/%.0f", h.State, h.Score)
	}
}

func TestPathHealthDegradesOnLoss(t *testing.T) {
	now := time.Now()
	var samples []PathSample
	for i := 0; i < 100; i++ {
		samples = append(samples, PathSample{At: now, LatencyMs: 20, PacketLossPct: 1.0}) // 1% loss
	}
	h := ScorePath("aws→gcp", samples, DefaultPathThresholds())
	if h.State == "HEALTHY" {
		t.Fatalf("1%% packet loss must degrade: %s/%.0f", h.State, h.Score)
	}
	if h.AvgLossPct < 0.99 || h.AvgLossPct > 1.01 {
		t.Fatalf("avg loss = %.3f", h.AvgLossPct)
	}
}

func TestPathHealthCriticalAndFlaps(t *testing.T) {
	now := time.Now()
	var samples []PathSample
	for i := 0; i < 50; i++ {
		samples = append(samples, PathSample{At: now, LatencyMs: 400, PacketLossPct: 5.0, RouteChanged: i%5 == 0})
	}
	h := ScorePath("aws→gcp", samples, DefaultPathThresholds())
	if h.State != "CRITICAL" {
		t.Fatalf("400ms + 5%% loss must be CRITICAL: %s/%.0f", h.State, h.Score)
	}
	if h.RouteFlaps != 10 {
		t.Fatalf("flaps = %d, want 10", h.RouteFlaps)
	}
	if h.Recommendation == "" {
		t.Fatal("critical path must carry a recommendation")
	}
}

func TestPathHealthP99(t *testing.T) {
	now := time.Now()
	var samples []PathSample
	for i := 0; i < 100; i++ {
		lat := 20.0
		if i == 99 {
			lat = 500 // single outlier
		}
		samples = append(samples, PathSample{At: now, LatencyMs: lat})
	}
	h := ScorePath("p", samples, DefaultPathThresholds())
	if h.P99LatencyMs != 500 {
		t.Fatalf("p99 = %.0f, want 500", h.P99LatencyMs)
	}
	if h.AvgLatencyMs > 25 {
		t.Fatalf("avg = %.1f, must stay near 20", h.AvgLatencyMs)
	}
}

func TestPathHealthEmpty(t *testing.T) {
	h := ScorePath("p", nil, DefaultPathThresholds())
	if h.State != "UNKNOWN" {
		t.Fatalf("empty samples = %s", h.State)
	}
}

// ── Circuit capacity ────────────────────────────────────────────────────────

func TestCircuitForecastLinearGrowth(t *testing.T) {
	c := Circuit{Name: "scheme-direct-1", CapacityMbps: 100, Provider: "payment-scheme"}
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	var samples []CircuitSample
	// 60 Mbps growing exactly 0.2 Mbps/day → hits 80 (threshold) in 100 days.
	for i := 0; i <= 60; i++ {
		samples = append(samples, CircuitSample{At: base.Add(time.Duration(i) * 24 * time.Hour), UtilisedMbps: 60 + 0.2*float64(i)})
	}
	p := NewCapacityPlanner()
	f := p.Plan(c, samples)

	if f.Verdict != "EXPANSION_NEEDED_THIS_QUARTER" && f.Verdict != "EXPAND_WITHIN_30_DAYS" {
		t.Fatalf("verdict = %s (days=%d)", f.Verdict, f.DaysToThreshold)
	}
	// 72 Mbps now, threshold 80, slope 0.2 → (80-72)/0.2 = 40 days.
	if f.DaysToThreshold < 35 || f.DaysToThreshold > 45 {
		t.Fatalf("days to threshold = %d, want ~40", f.DaysToThreshold)
	}
	// 90-day projection: 72 + 0.2*90 = 90 Mbps = 90%.
	if got := f.ProjectedPctAt[90]; got < 89 || got > 91 {
		t.Fatalf("90-day projection = %.1f%%, want ~90%%", got)
	}
}

func TestCircuitAlreadyOverThreshold(t *testing.T) {
	c := Circuit{Name: "c2", CapacityMbps: 100}
	base := time.Now().UTC()
	samples := []CircuitSample{
		{At: base.Add(-24 * time.Hour), UtilisedMbps: 90},
		{At: base, UtilisedMbps: 92},
	}
	f := NewCapacityPlanner().Plan(c, samples)
	if f.Verdict != "OVER_THRESHOLD_NOW" {
		t.Fatalf("92%% utilisation must be over threshold now: %s", f.Verdict)
	}
	if f.DaysToThreshold >= 0 {
		t.Fatalf("already-over must report negative/zero days, got %d", f.DaysToThreshold)
	}
}

func TestCircuitDecliningTrafficNeverCrosses(t *testing.T) {
	c := Circuit{Name: "c3", CapacityMbps: 100}
	base := time.Now().UTC()
	samples := []CircuitSample{
		{At: base.Add(-48 * time.Hour), UtilisedMbps: 70},
		{At: base.Add(-24 * time.Hour), UtilisedMbps: 60},
		{At: base, UtilisedMbps: 50},
	}
	f := NewCapacityPlanner().Plan(c, samples)
	if f.DaysToThreshold != math.MaxInt32 {
		t.Fatalf("declining traffic must never cross: %d", f.DaysToThreshold)
	}
	if f.Verdict != "OK" {
		t.Fatalf("verdict = %s", f.Verdict)
	}
}

func TestCircuitInsufficientSamples(t *testing.T) {
	f := NewCapacityPlanner().Plan(Circuit{Name: "c", CapacityMbps: 100},
		[]CircuitSample{{At: time.Now(), UtilisedMbps: 50}})
	if f.Verdict != "insufficient samples" {
		t.Fatalf("verdict = %s", f.Verdict)
	}
}

// ── Cross-cloud cost ────────────────────────────────────────────────────────

func TestCrossCloudCostRanking(t *testing.T) {
	o := NewCrossCloudOptimizer(DefaultCostModel())
	// 100 GB/day cross-cloud = 100*30*0.08 = £240/month.
	o.Observe(TrafficFlow{Source: "analytics@aws", Destination: "insights@gcp", CrossCloud: true, BytesPerDay: 100 << 30})
	// 5 TB/day cross-cloud = 5000*30*0.08 = £12,000/month.
	o.Observe(TrafficFlow{Source: "events@aws", Destination: "warehouse@gcp", CrossCloud: true, BytesPerDay: 5000 << 30})
	// Same-cloud: free.
	o.Observe(TrafficFlow{Source: "payments@aws", Destination: "ledger@aws", CrossCloud: false, BytesPerDay: 99999 << 30})

	recs := o.Recommendations()
	if len(recs) != 2 {
		t.Fatalf("only cross-cloud flows rank: %d", len(recs))
	}
	if recs[0].MonthlyCost < 11_000 {
		t.Fatalf("top recommendation = £%.0f, want the £12k flow first", recs[0].MonthlyCost)
	}
	if !contains(recs[0].Recommendation, "co-locate") {
		t.Fatalf("expensive flow must recommend co-location: %s", recs[0].Recommendation)
	}
	if !contains(recs[1].Recommendation, "batch or cache") {
		t.Fatalf("mid-cost flow should get a batching/cache recommendation: %s", recs[1].Recommendation)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
