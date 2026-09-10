package cardnet

import (
	"fmt"
	"testing"
	"time"
)

func v2Auth(stan string, amount string) map[string]string {
	return map[string]string{
		"mti": "0100", "amount": amount, "currency": "GBP", "stan": stan,
		"rrn": "RRN" + stan, "response_code": "00", "acquirer": "ACQ1",
		"terminal": "T1", "merchant": "M1", "mcc": "5411",
	}
}

// ── Gateway parsing across versions ─────────────────────────────────────────

func TestParseV2(t *testing.T) {
	g := NewGateway()
	m, err := g.Parse("v2", v2Auth("000123", "12.34"))
	if err != nil {
		t.Fatalf("parse v2: %v", err)
	}
	if m.Type != MsgAuth || m.AmountMinor != 1234 || m.Currency != "GBP" {
		t.Fatalf("canonical mismatch: %+v", m)
	}
	if !m.Approved() {
		t.Fatal("response 00 must be approved")
	}
}

func TestParseV1FlatFields(t *testing.T) {
	g := NewGateway()
	m, err := g.Parse("v1", map[string]string{
		"MTI": "0400", "DE4": "50.00", "DE49": "GBP", "DE11": "000777", "DE37": "RRN777",
		"DE39": "00", "DE32": "ACQ1", "DE41": "T9", "DE42": "M9", "DE18": "5411",
		"DE7": "0907123456",
	})
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	if m.Type != MsgReversal || m.AmountMinor != 5000 {
		t.Fatalf("v1 mismatch: %+v", m)
	}
	// DE7 (Sep 07, 12:34:56) must parse to a sane UTC time.
	if m.TransmissionTime.Month() != time.September || m.TransmissionTime.Day() != 7 {
		t.Fatalf("DE7 time = %s", m.TransmissionTime)
	}
}

func TestParseV3PrefixedFields(t *testing.T) {
	g := NewGateway()
	m, err := g.Parse("v3", map[string]string{
		"hdr.mti": "0200", "txn.amount_minor": "999", "txn.currency": "GBP",
		"txn.stan": "55", "txn.rrn": "RRN55", "txn.response_code": "00",
		"party.acquirer": "A", "party.terminal": "T", "party.merchant": "M", "party.mcc": "5411",
	})
	if err != nil {
		t.Fatalf("parse v3: %v", err)
	}
	if m.AmountMinor != 999 || m.Type != MsgCapture {
		t.Fatalf("v3 mismatch: %+v", m)
	}
}

func TestUnknownVersionRejected(t *testing.T) {
	g := NewGateway()
	if _, err := g.Parse("v99", v2Auth("1", "1.00")); err == nil {
		t.Fatal("unknown format version must be rejected")
	}
}

func TestMalformedMessagesRejected(t *testing.T) {
	g := NewGateway()
	// Missing RRN.
	bad := v2Auth("1", "1.00")
	delete(bad, "rrn")
	if _, err := g.Parse("v2", bad); err == nil {
		t.Fatal("missing RRN must fail validation")
	}
	// Negative amount.
	neg := v2Auth("2", "-5.00")
	if _, err := g.Parse("v2", neg); err == nil {
		t.Fatal("negative amount must fail")
	}
	// Unknown MTI.
	unk := v2Auth("3", "1.00")
	unk["mti"] = "0999"
	if _, err := g.Parse("v2", unk); err == nil {
		t.Fatal("unknown MTI must fail")
	}
}

func TestAmountParsingEdgeCases(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
	}{
		{"0.01", 1}, {"1", 100}, {"999.99", 99999}, {"0.10", 10}, {"1234.5", 123450},
	}
	for _, tc := range cases {
		got, err := parseAmount(tc.raw, 2)
		if err != nil || got != tc.want {
			t.Errorf("parseAmount(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
		}
	}
}

// ── Replay Lab ──────────────────────────────────────────────────────────────

func TestLabReplayAndGenerate(t *testing.T) {
	g := NewGateway()
	lab := NewLab(g)
	msgs := Generate(1000, 10, func(i int) string { return fmt.Sprintf("%06d", i) })
	res := lab.Replay(msgs)
	if res.Malformed != 0 {
		t.Fatalf("generated messages must all parse: %d malformed", res.Malformed)
	}
	// 1000 auths + 1000 captures + ~100 reversals.
	if res.Total < 2000 || res.Approved != res.Total {
		t.Fatalf("replay totals: %+v", res)
	}
	if res.ByType[MsgAuth] != 1000 || res.ByType[MsgCapture] != 1000 {
		t.Fatalf("by-type counts: %+v", res.ByType)
	}
	if res.ByType[MsgReversal] != 100 {
		t.Fatalf("reversal count = %d, want 100", res.ByType[MsgReversal])
	}
	if res.Throughput <= 0 {
		t.Fatal("throughput must be reported")
	}
}

func TestLabCountsMalformed(t *testing.T) {
	g := NewGateway()
	lab := NewLab(g)
	msgs := []RawMessage{
		{FormatVersion: "v2", Fields: v2Auth("1", "1.00")},
		{FormatVersion: "v2", Fields: map[string]string{"mti": "0100"}}, // missing everything
		{FormatVersion: "vX", Fields: v2Auth("2", "2.00")},              // unknown version
	}
	res := lab.Replay(msgs)
	if res.Total != 1 || res.Malformed != 2 {
		t.Fatalf("lab must count malformed: %+v", res)
	}
}

// ── Correlation ─────────────────────────────────────────────────────────────

func TestCorrelationTraceFromAnyIdentifier(t *testing.T) {
	e := NewCorrelationEngine()
	now := time.Now()
	e.Link(CorrelationKey{Kind: "app_payment", Value: "AP1"}, CorrelationKey{Kind: "internal_payment", Value: "IP1"}, now)
	e.Link(CorrelationKey{Kind: "internal_payment", Value: "IP1"}, CorrelationKey{Kind: "network_trace", Value: "RRN1"}, now)
	e.Link(CorrelationKey{Kind: "network_trace", Value: "RRN1"}, CorrelationKey{Kind: "processor_ref", Value: "PR1"}, now)
	e.Link(CorrelationKey{Kind: "processor_ref", Value: "PR1"}, CorrelationKey{Kind: "settlement_ref", Value: "SR1"}, now)

	// From ANY identifier, the full lifecycle is reachable.
	trace := e.Trace(CorrelationKey{Kind: "settlement_ref", Value: "SR1"})
	if len(trace) != 5 {
		t.Fatalf("trace must cover all 5 identifiers, got %d: %+v", len(trace), trace)
	}
	kinds := map[string]bool{}
	for _, k := range trace {
		kinds[k.Kind] = true
	}
	for _, want := range []string{"app_payment", "internal_payment", "network_trace", "processor_ref", "settlement_ref"} {
		if !kinds[want] {
			t.Fatalf("trace missing %s: %+v", want, trace)
		}
	}
}

// ── Advice sequencing ───────────────────────────────────────────────────────

func TestAdviceOutOfOrderAndDuplicates(t *testing.T) {
	tr := NewAdviceTracker()
	applied := []int64{}
	apply := func(seq int64) func() error {
		return func() error { applied = append(applied, seq); return nil }
	}

	// seq 2 arrives before seq 1 → held.
	res, err := tr.Apply("TXN1", 2, apply(2))
	if err != nil || res.Outcome != AdviceGap {
		t.Fatalf("seq 2 first must be a gap: %+v %v", res, err)
	}
	// seq 2 again → still stale/gap (lastSeq still 0).
	res, _ = tr.Apply("TXN1", 2, apply(2))
	if res.Outcome != AdviceGap {
		t.Fatalf("duplicate gap must stay gap: %+v", res)
	}
	// seq 1 arrives → applies, then drains held seq 2.
	res, err = tr.Apply("TXN1", 1, apply(1))
	if err != nil || res.Outcome != AdviceApplied || res.AppliedSeq != 2 {
		t.Fatalf("seq 1 must apply and drain to 2: %+v %v", res, err)
	}
	if len(applied) != 2 || applied[0] != 1 || applied[1] != 2 {
		t.Fatalf("applied order = %v, want [1 2]", applied)
	}
	// Old seq 1 replay → stale, must NOT re-apply.
	res, _ = tr.Apply("TXN1", 1, apply(1))
	if res.Outcome != AdviceStale {
		t.Fatalf("replay of seq 1 must be stale: %+v", res)
	}
	if len(applied) != 2 {
		t.Fatalf("stale message must not mutate: %v", applied)
	}
}

func TestAdviceFullAndPartialReversal(t *testing.T) {
	tr := NewAdviceTracker()
	if err := tr.RecordReversal("T1", 10000, 10000); err != nil {
		t.Fatalf("full reversal: %v", err)
	}
	if tr.State("T1") != "REVERSED" {
		t.Fatalf("state = %s", tr.State("T1"))
	}
	if err := tr.RecordReversal("T2", 10000, 3000); err != nil {
		t.Fatalf("partial reversal: %v", err)
	}
	if tr.State("T2") != "PARTIALLY_REVERSED" {
		t.Fatalf("state = %s", tr.State("T2"))
	}
	if err := tr.RecordReversal("T3", 10000, 20000); err == nil {
		t.Fatal("reversal exceeding original must fail")
	}
}

// ── Offline presentment ─────────────────────────────────────────────────────

func TestOfflineDuplicateAndCeiling(t *testing.T) {
	h := NewOfflineHandler(5000, 2*time.Hour) // £50 ceiling, 2h max age
	now := time.Now()
	msg := &CanonicalMessage{STAN: "S1", AmountMinor: 4000, TransmissionTime: now.Add(-10 * time.Minute)}

	if got := h.Present(msg, now); got != OfflineAccept {
		t.Fatalf("first presentment = %s", got)
	}
	if got := h.Present(msg, now); got != OfflineDuplicate {
		t.Fatalf("re-presentment = %s, want DUPLICATE_SUPPRESSED", got)
	}
	over := &CanonicalMessage{STAN: "S2", AmountMinor: 6000, TransmissionTime: now.Add(-10 * time.Minute)}
	if got := h.Present(over, now); got != OfflineOverCeiling {
		t.Fatalf("over-ceiling = %s", got)
	}
	stale := &CanonicalMessage{STAN: "S3", AmountMinor: 1000, TransmissionTime: now.Add(-3 * time.Hour)}
	if got := h.Present(stale, now); got != OfflineStale {
		t.Fatalf("stale presentment = %s", got)
	}
	if h.SeenCount() != 1 {
		t.Fatalf("only accepted messages count: %d", h.SeenCount())
	}
}

// ── Contactless counters ────────────────────────────────────────────────────

func TestContactlessCounterLimits(t *testing.T) {
	c := NewContactlessCounter(3, 15000) // 3 taps or £150
	if !c.CanSpend(5000) {
		t.Fatal("first tap must be allowed")
	}
	if err := c.Record(5000, time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := c.Record(5000, time.Now().Add(time.Second)); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	if c.CanSpend(6000) {
		t.Fatal("tap exceeding cumulative amount must be refused")
	}
	if !c.CanSpend(5000) {
		t.Fatal("tap within limits must be allowed")
	}
	// Third tap hits count limit.
	if err := c.Record(5000, time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("record 3: %v", err)
	}
	if c.CanSpend(100) {
		t.Fatal("count limit exhausted")
	}
	// Reconcile with issuer's higher counter (network partition case).
	c.Reconcile(5, 25000)
	count, amount := c.Snapshot()
	if count != 5 || amount != 25000 {
		t.Fatalf("reconcile = %d/%d, want 5/25000", count, amount)
	}
	// Reconcile never reduces.
	c.Reconcile(1, 100)
	count, amount = c.Snapshot()
	if count != 5 || amount != 25000 {
		t.Fatalf("reconcile must not reduce: %d/%d", count, amount)
	}
}

func TestContactlessStaleUpdateRejected(t *testing.T) {
	c := NewContactlessCounter(10, 100000)
	now := time.Now()
	if err := c.Record(100, now); err != nil {
		t.Fatalf("first record: %v", err)
	}
	// Delayed duplicate with older timestamp must not double-count.
	if err := c.Record(100, now.Add(-time.Minute)); err == nil {
		t.Fatal("stale contactless update must be rejected")
	}
	count, _ := c.Snapshot()
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}
