package multicurrency

import (
	"testing"
	"time"
)

func testRates() map[string]float64 {
	return map[string]float64{
		"EURGBP": 0.85,
		"USDGBP": 0.79,
		"JPYGBP": 0.0053,
		"GBPGBP": 1,
		"GBPEUR": 1.18,
		"GBPUSD": 1.27,
	}
}

func TestCreditDebitPerCurrency(t *testing.T) {
	l := NewLedger()
	if err := l.Credit("a-1", "GBP", 1000); err != nil {
		t.Fatalf("credit failed: %v", err)
	}
	if err := l.Credit("a-1", "EUR", 500); err != nil {
		t.Fatalf("credit eur failed: %v", err)
	}
	if got := l.Balance("a-1", "GBP"); got != 1000 {
		t.Fatalf("GBP = %d, want 1000", got)
	}
	if got := l.Balance("a-1", "EUR"); got != 500 {
		t.Fatalf("EUR = %d, want 500", got)
	}
	if err := l.Debit("a-1", "GBP", 400); err != nil {
		t.Fatalf("debit failed: %v", err)
	}
	if got := l.Balance("a-1", "GBP"); got != 600 {
		t.Fatalf("GBP = %d, want 600", got)
	}
	if err := l.Debit("a-1", "GBP", 99999); err == nil {
		t.Fatalf("overdraft must fail")
	}
}

func TestConversionMathWithSpread(t *testing.T) {
	l := NewLedger()
	if err := l.Credit("a-2", "EUR", 10000); err != nil {
		t.Fatalf("credit failed: %v", err)
	}
	snap, err := l.SnapshotRates(testRates(), time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	// 10000 EUR at 0.85 with 100bps (1%) spread: 10000*0.85*0.99 = 8415.
	got, err := l.Convert("a-2", "EUR", "GBP", 10000, snap, 100, time.Now().UTC())
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	if got != 8415 {
		t.Fatalf("converted = %d, want 8415", got)
	}
	if bal := l.Balance("a-2", "EUR"); bal != 0 {
		t.Fatalf("EUR = %d, want 0", bal)
	}
	if bal := l.Balance("a-2", "GBP"); bal != 8415 {
		t.Fatalf("GBP = %d, want 8415", bal)
	}
	// Zero spread converts at the raw rate.
	if err := l.Credit("a-2", "EUR", 1000); err != nil {
		t.Fatalf("credit 2 failed: %v", err)
	}
	got, err = l.Convert("a-2", "EUR", "GBP", 1000, snap, 0, time.Now().UTC())
	if err != nil {
		t.Fatalf("convert 2 failed: %v", err)
	}
	if got != 850 {
		t.Fatalf("zero-spread converted = %d, want 850", got)
	}
}

func TestSnapshotPinnedValuationStable(t *testing.T) {
	l := NewLedger()
	if err := l.Credit("a-3", "GBP", 1000); err != nil {
		t.Fatalf("credit gbp: %v", err)
	}
	if err := l.Credit("a-3", "EUR", 1000); err != nil {
		t.Fatalf("credit eur: %v", err)
	}
	snap1, err := l.SnapshotRates(testRates(), time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot 1: %v", err)
	}
	v1, err := l.ValuateTotal("a-3", "GBP", snap1)
	if err != nil {
		t.Fatalf("valuate 1: %v", err)
	}
	if v1 != 1000+850 {
		t.Fatalf("valuation = %d, want %d", v1, 1850)
	}
	// Rates move sharply; the pinned valuation must not move.
	moved := testRates()
	moved["EURGBP"] = 0.50
	snap2, err := l.SnapshotRates(moved, time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}
	v1again, err := l.ValuateTotal("a-3", "GBP", snap1)
	if err != nil {
		t.Fatalf("valuate pinned: %v", err)
	}
	if v1again != v1 {
		t.Fatalf("pinned valuation moved: %d vs %d", v1again, v1)
	}
	v2, err := l.ValuateTotal("a-3", "GBP", snap2)
	if err != nil {
		t.Fatalf("valuate 2: %v", err)
	}
	if v2 != 1000+500 {
		t.Fatalf("new snapshot valuation = %d, want 1500", v2)
	}
	// Missing snapshot id is an error (consistency token required).
	if _, err := l.ValuateTotal("a-3", "GBP", "nope"); err == nil {
		t.Fatalf("unknown snapshot must fail")
	}
	if _, err := l.ValuateTotal("a-3", "GBP", ""); err == nil {
		t.Fatalf("empty snapshot must fail")
	}
}

func TestConversionAuditTrail(t *testing.T) {
	l := NewLedger()
	if err := l.Credit("a-4", "USD", 2000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	snap, err := l.SnapshotRates(testRates(), time.Now().UTC())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if _, err := l.Convert("a-4", "USD", "GBP", 1000, snap, 50, time.Now().UTC()); err != nil {
		t.Fatalf("convert 1: %v", err)
	}
	if _, err := l.Convert("a-4", "USD", "GBP", 500, snap, 0, time.Now().UTC()); err != nil {
		t.Fatalf("convert 2: %v", err)
	}
	audit := l.Audit("a-4")
	if len(audit) != 2 {
		t.Fatalf("audit len = %d, want 2", len(audit))
	}
	if audit[0].From != "USD" || audit[0].To != "GBP" || audit[0].Amount != 1000 || audit[0].SpreadBps != 50 {
		t.Fatalf("audit[0] wrong: %+v", audit[0])
	}
	if audit[0].SnapshotID != snap || audit[1].SnapshotID != snap {
		t.Fatalf("audit must pin snapshot id")
	}
	if audit[0].Rate != 0.79 {
		t.Fatalf("audit rate = %v, want 0.79", audit[0].Rate)
	}
}
