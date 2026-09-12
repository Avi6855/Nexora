package schemes

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func schemeNop() zerolog.Logger { return zerolog.Nop() }

var certT0 = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

func TestCertHarnessPassFail(t *testing.T) {
	h := NewCertHarness(schemeNop())
	// Passing AUTH case.
	if _, err := h.RegisterCase(ScenarioAuth,
		map[string]string{"amount": "1000", "currency": "GBP", "pan": "fp-1"},
		map[string]string{"response_code": "00"}); err != nil {
		t.Fatal(err)
	}
	// Failing CLEARING case: expected CLEARED but message lacks auth_code.
	if _, err := h.RegisterCase(ScenarioClearing,
		map[string]string{"amount": "1000"},
		map[string]string{"clearing_status": "CLEARED"}); err != nil {
		t.Fatal(err)
	}
	rep, err := h.RunAll(certT0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed != 1 || rep.Failed != 1 {
		t.Fatalf("want 1/1, got %+v", rep)
	}
	if rep.AllPassed {
		t.Fatal("mixed run must not be all-passed")
	}
	if len(rep.Results) != 2 {
		t.Fatalf("want 2 results, got %d", len(rep.Results))
	}
	got, err := h.GetRun(rep.RunID)
	if err != nil || got.RunID != rep.RunID {
		t.Fatalf("GetRun: %v", err)
	}
	if _, err := h.GetRun("missing"); !errors.Is(err, ErrCertRunNotFound) {
		t.Fatalf("missing run: %v", err)
	}
	// Full suite of passing kinds.
	h2 := NewCertHarness(schemeNop())
	passing := []struct {
		kind ScenarioKind
		msg  map[string]string
		exp  map[string]string
	}{
		{ScenarioAuth, map[string]string{"amount": "10", "currency": "GBP", "pan": "p"}, map[string]string{"response_code": "00"}},
		{ScenarioClearing, map[string]string{"auth_code": "A1", "amount": "10"}, map[string]string{"clearing_status": "CLEARED"}},
		{ScenarioReversal, map[string]string{"original_auth": "A1"}, map[string]string{"reversal_status": "REVERSED"}},
		{ScenarioRefund, map[string]string{"original_tx": "T1", "amount": "10"}, map[string]string{"refund_status": "REFUNDED"}},
		{ScenarioChargeback, map[string]string{"original_tx": "T1", "reason": "fraud"}, map[string]string{"chargeback_status": "ACCEPTED"}},
		{ScenarioAdvice, map[string]string{"advice_code": "X"}, map[string]string{"advice_status": "RECORDED"}},
	}
	for _, p := range passing {
		if _, err := h2.RegisterCase(p.kind, p.msg, p.exp); err != nil {
			t.Fatal(err)
		}
	}
	rep2, err := h2.RunAll(certT0)
	if err != nil {
		t.Fatal(err)
	}
	if !rep2.AllPassed || rep2.Failed != 0 {
		t.Fatalf("full suite must pass: %+v", rep2)
	}
	if _, err := h2.RegisterCase("NOPE", map[string]string{"a": "b"}, map[string]string{"c": "d"}); err == nil {
		t.Fatal("unknown kind must error")
	}
}

func TestRoutingOptimizer(t *testing.T) {
	r := NewRouter(schemeNop())
	rails := []Rail{
		{Name: "FPS", CostBps: 10, P50LatencyMs: 200, Availability: 0.999, Currencies: []string{"GBP"}, AmountMinMinor: 1, AmountMaxMinor: 1000000, Destinations: []string{"UK"}},
		{Name: "SWIFT", CostBps: 50, P50LatencyMs: 2000, Availability: 0.99, Currencies: []string{"GBP", "USD"}, AmountMinMinor: 100, AmountMaxMinor: 10000000, Destinations: []string{"UK", "US"}},
		{Name: "SEPA", CostBps: 5, P50LatencyMs: 500, Availability: 0.995, Currencies: []string{"EUR"}, AmountMinMinor: 1, AmountMaxMinor: 500000, Destinations: []string{"FR"}},
	}
	for _, rail := range rails {
		if _, err := r.AddRail(rail); err != nil {
			t.Fatal(err)
		}
	}
	opts, err := r.Route(10000, "GBP", "UK")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 2 {
		t.Fatalf("want 2 GBP/UK options, got %+v", opts)
	}
	if opts[0].Rail != "FPS" {
		t.Fatalf("cheapest must rank first, got %+v", opts)
	}
	if opts[0].Reason == "" || opts[1].Reason == "" {
		t.Fatal("each option needs a reason")
	}
	// Amount bounds filter.
	opts, err = r.Route(50, "GBP", "UK")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts[0].Rail != "FPS" {
		t.Fatalf("small amount must route FPS only, got %+v", opts)
	}
	if _, err := r.Route(100, "GBP", "FR"); !errors.Is(err, ErrNoRoute) {
		t.Fatalf("no matching rail must be ErrNoRoute, got %v", err)
	}
	if _, err := r.Route(0, "GBP", "UK"); err == nil {
		t.Fatal("zero amount must error")
	}
}

func TestSettlementWindows(t *testing.T) {
	c := NewSettlementCalendar(schemeNop())
	if _, err := c.AddScheme(SchemeWindow{Scheme: "VISA", CycleDays: 1, Cutoff: "15:00", Holidays: []string{"2026-09-14"}}); err != nil {
		t.Fatal(err)
	}
	// Monday 2026-09-14 is a holiday in this calendar; use known weekdays:
	// 2026-09-14 is Monday. Friday 2026-09-11 14:00 before cutoff T+1 -> Monday 2026-09-14? No, that's a holiday -> Tuesday 2026-09-15.
	monday := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC) // holiday Monday
	next, err := c.NextSettlement("visa", monday)
	if err != nil {
		t.Fatal(err)
	}
	// Monday is a holiday so base rolls to Tuesday 15th, +1 business day -> Wednesday 16th 15:00.
	want := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("holiday Monday T+1 got %s want %s", next, want)
	}
	// Tuesday before cutoff -> Wednesday.
	tue := time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC)
	next, _ = c.NextSettlement("VISA", tue)
	want = time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Tue before cutoff got %s want %s", next, want)
	}
	// Tuesday after cutoff -> Thursday.
	tueLate := time.Date(2026, 9, 15, 16, 0, 0, 0, time.UTC)
	next, _ = c.NextSettlement("VISA", tueLate)
	want = time.Date(2026, 9, 17, 15, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Tue after cutoff got %s want %s", next, want)
	}
	// T+0 same-day before cutoff.
	if _, err := c.AddScheme(SchemeWindow{Scheme: "FPS", CycleDays: 0, Cutoff: "15:00"}); err != nil {
		t.Fatal(err)
	}
	next, _ = c.NextSettlement("FPS", tue)
	want = time.Date(2026, 9, 15, 15, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("T+0 got %s want %s", next, want)
	}
	if _, err := c.NextSettlement("NOPE", tue); !errors.Is(err, ErrSchemeNotFound) {
		t.Fatalf("unknown scheme: %v", err)
	}
	if _, err := c.AddScheme(SchemeWindow{Scheme: "BAD", CycleDays: 5, Cutoff: "15:00"}); err == nil {
		t.Fatal("cycle >2 must error")
	}
}

func TestFeeAttribution(t *testing.T) {
	l := NewFeeLedger(schemeNop())
	t1, err := l.Attribute(FeeBreakdown{TxID: "tx-1", Scheme: "visa", Currency: "GBP", Network: 10, Processor: 5, FX: 0, Interchange: 20, Internal: 2}, certT0)
	if err != nil {
		t.Fatal(err)
	}
	if t1.TotalMinor != 37 {
		t.Fatalf("total 37, got %d", t1.TotalMinor)
	}
	if _, err := l.Attribute(FeeBreakdown{TxID: "tx-2", Scheme: "VISA", Currency: "GBP", Network: 10, Processor: 5, FX: 1, Interchange: 20, Internal: 3}, certT0); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Attribute(FeeBreakdown{TxID: "tx-3", Scheme: "MC", Currency: "GBP", Network: 7, Processor: 7, FX: 7, Interchange: 7, Internal: 7}, certT0); err != nil {
		t.Fatal(err)
	}
	sum := l.Summary("visa")
	if sum.Count != 2 || sum.TotalMinor != 37+39 {
		t.Fatalf("visa summary wrong: %+v", sum)
	}
	if sum.ByComponent["network"] != 20 {
		t.Fatalf("visa network sum wrong: %+v", sum.ByComponent)
	}
	if sum.AvgMinor != float64(76)/2 {
		t.Fatalf("avg wrong: %+v", sum)
	}
	all := l.SummaryAll()
	if len(all) != 2 || all[0].Scheme != "MC" {
		t.Fatalf("summary all must be sorted MC,VISA: %+v", all)
	}
	if _, err := l.Attribute(FeeBreakdown{TxID: "tx-bad", Scheme: "VISA", Currency: "GBP", Network: -1}, certT0); err == nil {
		t.Fatal("negative fee must error")
	}
}
