package cash

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReserveConfirmRelease(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	u := NewMonthlyUsage("2026-09", 75000) // £750.00
	if u.Available() != 75000 {
		t.Fatalf("initial available %d, want 75000", u.Available())
	}
	r, err := u.Reserve(40000, now, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if u.Available() != 35000 {
		t.Fatalf("after reserve available %d, want 35000", u.Available())
	}
	// Second reserve exceeding remaining headroom fails.
	if _, err := u.Reserve(40000, now, "r2"); !errors.Is(err, ErrLimitExhausted) {
		t.Fatalf("want ErrLimitExhausted, got %v", err)
	}
	// Confirm settles; release frees.
	if err := u.Confirm(r.ID, 40000, now); err != nil {
		t.Fatal(err)
	}
	if u.Available() != 35000 {
		t.Fatalf("after confirm available %d, want 35000", u.Available())
	}
	r2, _ := u.Reserve(10000, now, "r2")
	if err := u.Release(r2.ID); err != nil {
		t.Fatal(err)
	}
	if u.Available() != 35000 {
		t.Fatalf("after release available %d, want 35000", u.Available())
	}
}

func TestReservationExpiryAndLateSettlement(t *testing.T) {
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	u := NewMonthlyUsage("2026-09", 75000)
	u.ReserveTTL = 30 * time.Minute
	if _, err := u.Reserve(40000, now, "r1"); err != nil {
		t.Fatal(err)
	}
	// Sweep at expiry: reservation vanishes, headroom returns.
	later := now.Add(31 * time.Minute)
	if _, err := u.Reserve(40000, later, "r2"); err != nil {
		t.Fatalf("expired reservation must free headroom: %v", err)
	}
	// Settlement for the swept reservation arrives late. The money moved, so
	// it must be admitted against the limit — rejecting it under-counts usage.
	if err := u.Reconfirm("r1", 40000); err != nil {
		t.Fatalf("late settlement must be admitted: %v", err)
	}
	if u.DepositedMinor != 40000 {
		t.Fatalf("deposited %d, want 40000", u.DepositedMinor)
	}
	// Replaying the same late settlement is rejected (superseded).
	if err := u.Reconfirm("r1", 40000); err == nil {
		t.Fatal("duplicate late settlement must fail")
	}
}

func TestProviderFailover(t *testing.T) {
	h := NewHealthState("paypoint")
	// Cascade of failures drains health below routable threshold.
	for i := 0; i < 4; i++ {
		h.RecordFailure(time.Date(2026, 9, 8, 10, 0, i, 0, time.UTC))
	}
	if h.Routable() {
		t.Fatalf("4 failures must take provider out: health=%.2f", h.Health)
	}
	// Sustained success recovers it (half-life style).
	for i := 0; i < 30; i++ {
		h.RecordSuccess(time.Date(2026, 9, 8, 11, i, 0, 0, time.UTC))
	}
	if !h.Routable() {
		t.Fatalf("provider must recover after sustained success: health=%.2f", h.Health)
	}
}

func TestRouterPicksBestAndFailsOver(t *testing.T) {
	pp := &Provider{
		Name: "PayPoint", Capability: "CASH_DEPOSIT",
		Locations: []Location{
			{ID: "pp1", Name: "Corner Shop", Supports: "CASH_DEPOSIT", CostPence: 50, QueueMinutes: 12, OpenNow: true},
		},
		Policies: map[string]int64{"PERSONAL": 75000},
	}
	po := &Provider{
		Name: "PostOffice", Capability: "CASH_DEPOSIT",
		Locations: []Location{
			{ID: "po1", Name: "High St PO", Supports: "CASH_DEPOSIT", CostPence: 0, QueueMinutes: 5, OpenNow: true},
		},
		Policies: map[string]int64{"PERSONAL": 75000},
	}
	router := &Router{
		Providers: map[string]*Provider{"PayPoint": pp, "PostOffice": po},
		Health:    map[string]*HealthState{"PayPoint": NewHealthState("PayPoint"), "PostOffice": NewHealthState("PostOffice")},
		Pricing:   map[string]int64{"PayPoint": 50, "PostOffice": 0},
	}
	usage := NewMonthlyUsage("2026-09", 75000)
	req := DepositRequest{CustomerID: "c1", AccountType: "PERSONAL", AmountMinor: 40000, At: time.Now()}

	route, err := router.Route(req, usage, TierNormal)
	if err != nil {
		t.Fatal(err)
	}
	if route.Provider != "PostOffice" {
		t.Fatalf("cheaper+quieter provider should win, got %s (%s)", route.Provider, route.Reason)
	}
	if len(route.Alternatives) == 0 || route.Alternatives[0].Provider != "PayPoint" {
		t.Fatalf("expected PayPoint as alternative: %+v", route.Alternatives)
	}
	if usage.ReservedMinor != 40000 {
		t.Fatalf("route must reserve limit: reserved=%d", usage.ReservedMinor)
	}

	// Failover: degrade PayPoint fully; PostOffice unreachable too → error
	// carries per-provider reasons.
	router.Health["PayPoint"].Health = 0.9 // still routable
	router.Health["PostOffice"].Health = 0.1
	_, err = router.Route(req, usage, TierNormal)
	var re *RouteError
	if !errors.As(err, &re) {
		t.Fatalf("want RouteError, got %v", err)
	}
	if !strings.Contains(re.Reasons["PostOffice"], "health") {
		t.Fatalf("exclusion reasons must name the health cause: %+v", re.Reasons)
	}
}

func TestTiering(t *testing.T) {
	req := DepositRequest{AmountMinor: 70000} // £700, under the £750 line
	// Normal customer: steady history, declared source.
	hist := DepositHistory{Count90d: 3, SourceDeclared: true, AccountAgeDays: 400}
	if got := TierDeposit(req, hist, 75000); got.Tier != TierNormal {
		t.Fatalf("steady customer must be NORMAL: %+v", got)
	}
	// Structuring: just-under-threshold pattern always escalates.
	hist.StructuringSignals = 2
	if got := TierDeposit(req, hist, 75000); got.Tier != TierReview || !got.Review {
		t.Fatalf("structuring must REVIEW: %+v", got)
	}
	// Structuring + prior escalation → RESTRICT.
	hist.PriorReviewOutcome = "ESCALATED"
	if got := TierDeposit(req, hist, 75000); got.Tier != TierRestrict {
		t.Fatalf("continued structuring must RESTRICT: %+v", got)
	}
	// Frequency growth alone watches but doesn't punish.
	hist2 := DepositHistory{FrequencyGrowth: 2.5, SourceDeclared: true, AccountAgeDays: 500}
	if got := TierDeposit(req, hist2, 75000); got.Tier != TierWatch {
		t.Fatalf("frequency growth must WATCH: %+v", got)
	}
	// At/over the reporting line reviews regardless of history.
	if got := TierDeposit(DepositRequest{AmountMinor: 75000}, DepositHistory{SourceDeclared: true, AccountAgeDays: 900}, 75000); got.Tier != TierReview {
		t.Fatalf("at-limit deposit must REVIEW: %+v", got)
	}
}
