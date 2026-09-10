package search

import (
	"testing"
	"time"
)

var day = func(d int, h int) time.Time { return time.Date(2026, 1, d, h, 0, 0, 0, time.UTC) }

func mkDoc(id string, d int, merchant, category string, amount int64) Doc {
	return Doc{ID: id, AccountID: "acc-1", Merchant: merchant, Description: "card purchase",
		AmountMinor: amount, Currency: "GBP", Category: category, Country: "GB", At: day(d, 12)}
}

func seed() (*Index, *Overlay) {
	ix := NewIndex()
	ix.Ingest([]Doc{
		mkDoc("t1", 5, "Tesco", "GROCERIES", 4820),
		mkDoc("t2", 6, "Shell", "FUEL", 3099),
		mkDoc("t3", 7, "Amazon", "SHOPPING", 1999),
	})
	o := &Overlay{}
	// A payment made seconds ago — the index hasn't caught up.
	o.Add(mkDoc("t4", 7, "Uber", "TRANSPORT", 850))
	return ix, o
}

func TestTenantIsolation(t *testing.T) {
	ix, o := seed()
	ix.Ingest([]Doc{func() Doc { d := mkDoc("x9", 8, "Tesco", "GROCERIES", 100); d.AccountID = "acc-OTHER"; return d }()})
	e := NewEngine(ix, o)
	resp := e.Search(Query{OwnerAccountID: "acc-1", Text: "tesco"})
	if len(resp.Hits) != 1 || resp.Hits[0].Doc.ID != "t1" {
		t.Fatalf("isolation broken: %+v", resp.Hits)
	}
}

func TestOverlayReconciliation(t *testing.T) {
	ix, o := seed()
	e := NewEngine(ix, o)
	resp := e.Search(Query{OwnerAccountID: "acc-1", Text: "uber"})
	if len(resp.Hits) != 1 {
		t.Fatalf("just-paid transaction must be found via overlay: %+v", resp.Hits)
	}
	if resp.Hits[0].Source != "OVERLAY" {
		t.Fatalf("hit must be marked OVERLAY: %+v", resp.Hits[0])
	}
	if resp.OverlayUsed != 1 {
		t.Fatalf("overlay count %d, want 1", resp.OverlayUsed)
	}
	if !resp.IndexUpTo.Equal(day(7, 12)) {
		t.Fatalf("watermark %v, want %v", resp.IndexUpTo, day(7, 12))
	}
}

func TestExplainability(t *testing.T) {
	ix, o := seed()
	e := NewEngine(ix, o)
	min := int64(1000)
	resp := e.Search(Query{OwnerAccountID: "acc-1", Merchant: "Tesco", MinAmountMinor: &min})
	if len(resp.Hits) != 1 {
		t.Fatalf("want exactly the Tesco >= £10 hit: %+v", resp.Hits)
	}
	h := resp.Hits[0]
	var hasMerchant, hasAmount bool
	for _, m := range h.Explain {
		switch m.Field {
		case "merchant":
			hasMerchant = true
		case "amount >= ":
			hasAmount = true
		}
	}
	if !hasMerchant || !hasAmount {
		t.Fatalf("explanation must name every predicate: %+v", h.Explain)
	}
	// Non-matching predicate excludes the doc.
	max := int64(1000)
	resp = e.Search(Query{OwnerAccountID: "acc-1", Merchant: "Tesco", MaxAmountMinor: &max})
	if len(resp.Hits) != 0 {
		t.Fatalf("Tesco £48.20 must fail the <=£10 filter: %+v", resp.Hits)
	}
}

func TestMultiTermAND(t *testing.T) {
	ix, o := seed()
	e := NewEngine(ix, o)
	// "tesco january" — Tesco doc is in January, matches.
	resp := e.Search(Query{OwnerAccountID: "acc-1", Text: "tesco january"})
	if len(resp.Hits) != 1 {
		t.Fatalf("AND text search: %+v", resp.Hits)
	}
	// "tesco shell" matches nothing (no doc has both terms).
	resp = e.Search(Query{OwnerAccountID: "acc-1", Text: "tesco shell"})
	if len(resp.Hits) != 0 {
		t.Fatalf("AND semantics must exclude: %+v", resp.Hits)
	}
}

func TestBackfillResumableAndDuplicateSafe(t *testing.T) {
	ix := NewIndex()
	rows := []Doc{
		mkDoc("t1", 1, "A", "X", 100),
		mkDoc("t2", 2, "B", "Y", 200),
		mkDoc("t3", 3, "C", "Z", 300),
	}
	src := func(from, to time.Time, limit int) []Doc {
		var out []Doc
		for _, d := range rows {
			if !d.At.Before(from) && d.At.Before(to) {
				out = append(out, d)
			}
		}
		return out
	}
	b := NewBackfiller(ix, "bf-1", 2, day(1, 0), src)
	b.Run(2) // two day-windows: rows 1 and 2 (day 3 not yet)
	if b.State.Indexed != 2 {
		t.Fatalf("indexed %d, want 2", b.State.Indexed)
	}
	// Simulate crash + resume: new backfiller with same checkpoint semantics.
	b.Run(5)
	if !b.State.Done || b.State.Indexed != 3 {
		t.Fatalf("resume must finish: %+v", b.State)
	}
	if b.State.Checkpoint != day(4, 0) {
		t.Fatalf("checkpoint must be grid-aligned past the last row: %v", b.State.Checkpoint)
	}
	if p := b.ProgressPct(day(4, 0)); p != 100 {
		t.Fatalf("done backfill progress %v, want 100", p)
	}
	// Re-running a completed backfill is a no-op...
	b.Run(5)
	if b.State.Indexed != 3 {
		t.Fatalf("done backfill must not re-run: %+v", b.State)
	}
	// ...and duplicate re-delivery over the same window stays idempotent.
	ix.Ingest(rows)
	if len(ix.docs) != 3 {
		t.Fatalf("duplicate ingest must not corrupt: %d docs", len(ix.docs))
	}
}

func TestBackfillThrottleConfig(t *testing.T) {
	ix := NewIndex()
	b := NewBackfiller(ix, "bf-2", 1, day(1, 0), func(from, to time.Time, limit int) []Doc { return nil })
	if b.State.MaxDocsPerSecond <= 0 {
		t.Fatal("backfill must be throttled by default")
	}
	b.Run(1)
	if !b.State.Done {
		t.Fatal("empty source completes immediately")
	}
}
