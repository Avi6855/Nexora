package openfinance

import (
	"testing"
	"time"
)

func TestLinkHealthHealthy(t *testing.T) {
	h, err := LinkHealthScore(LinkHealthInputs{
		Authentication: LinkComponent{Score: 98, Weight: 3},
		Freshness:      FreshnessScore(time.Now().Add(-10*time.Minute), time.Now(), 24*time.Hour),
		Availability:   LinkComponent{Score: 99, Weight: 2},
		DataQuality:    LinkComponent{Score: 94, Weight: 2},
	})
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if h.State != "HEALTHY" || h.Score < 90 {
		t.Fatalf("expected HEALTHY >=90, got %.1f %s", h.Score, h.State)
	}
	// Freshness (100, just synced) should not be the weakest.
	if h.Weakest.Detail == "synced just now" {
		t.Fatalf("weakest should be a real weakness: %+v", h.Weakest)
	}
}

func TestLinkHealthDegradedFreshness(t *testing.T) {
	h, err := LinkHealthScore(LinkHealthInputs{
		Authentication: LinkComponent{Score: 98, Weight: 3},
		Freshness:      FreshnessScore(time.Now().Add(-30*24*time.Hour), time.Now(), 45*24*time.Hour),
		Availability:   LinkComponent{Score: 99, Weight: 2},
		DataQuality:    LinkComponent{Score: 94, Weight: 2},
	})
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if h.State == "HEALTHY" {
		t.Fatalf("30-day-stale link must not be HEALTHY: %.1f", h.Score)
	}
	if h.Weakest.Detail == "" || h.Weakest.Score >= 40 {
		t.Fatalf("freshness should be weakest: %+v", h.Weakest)
	}
}

func TestLinkHealthRangeValidation(t *testing.T) {
	_, err := LinkHealthScore(LinkHealthInputs{
		Authentication: LinkComponent{Score: 120, Weight: 1},
	})
	if err == nil {
		t.Fatal("score >100 must be rejected")
	}
	_, err = LinkHealthScore(LinkHealthInputs{})
	if err == nil {
		t.Fatal("zero weights must be rejected")
	}
}

func TestFreshnessScoreBounds(t *testing.T) {
	now := time.Now()
	if s := FreshnessScore(now, now, time.Hour); s.Score != 100 {
		t.Fatalf("just-synced should be 100, got %v", s.Score)
	}
	if s := FreshnessScore(now.Add(-2*time.Hour), now, time.Hour); s.Score != 0 {
		t.Fatalf("past max-stale should be 0, got %v", s.Score)
	}
}

func TestNormalizerProviderMappings(t *testing.T) {
	n := NewNormalizer()
	n.AddMapping("hsbc", "CARD PAYMENT", CanonCardPayment)
	n.AddMapping("lloyds", "DEBIT CARD PURCHASE", CanonCardPayment)
	n.AddMapping("barclays", "POS PURCHASE", CanonCardPayment)

	for _, tc := range []struct{ provider, raw string }{
		{"hsbc", "CARD PAYMENT"},
		{"lloyds", "DEBIT CARD PURCHASE"},
		{"barclays", "pos purchase"}, // case/space normalised
	} {
		if got := n.Normalize(tc.provider, tc.raw); got != CanonCardPayment {
			t.Fatalf("%s/%s → %s, want CARD_PAYMENT", tc.provider, tc.raw, got)
		}
	}
}

func TestNormalizerHeuristics(t *testing.T) {
	n := NewNormalizer()
	cases := map[string]CanonicalTransactionType{
		"HSBC CARD PAYMENT UNMAPPED": CanonCardPayment, // heuristic fallback
		"DD UTILITY CO":              CanonDirectDebit,
		"STANDING ORDER RENT":        CanonStandingOrder,
		"ATM WITHDRAWAL LONDON":      CanonCash,
		"FPS FROM SARAH":             CanonFasterPayment,
		"MONTHLY INTEREST":           CanonInterest,
		"SERVICE FEE":                CanonFee,
		"CARD REFUND TESCO":          CanonRefund, // refund outranks card
		"MYSTERY TEXT":               CanonUnknown,
	}
	for raw, want := range cases {
		if got := n.Normalize("unknownbank", raw); got != want {
			t.Fatalf("%q → %s, want %s", raw, got, want)
		}
	}
}

func TestConfidenceScoring(t *testing.T) {
	v, err := ScoreConfidence(Confidence{Merchant: 0.99, Category: 0.91, Location: 0.62, Amount: 1.0}, 0.9)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if v.Composite != 0.9 {
		t.Fatalf("composite %.2f, want 0.9", v.Composite)
	}
	// Low fields sorted ascending: location (0.62) only.
	if len(v.LowFields) != 1 || v.LowFields[0] != "location" {
		t.Fatalf("low fields %v, want [location]", v.LowFields)
	}
}

func TestConfidenceValidation(t *testing.T) {
	if _, err := ScoreConfidence(Confidence{Merchant: 1.5}, 0.9); err == nil {
		t.Fatal("confidence >1 must be rejected")
	}
}
