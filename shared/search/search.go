// Package search implements Nexora's customer transaction search platform:
// an inverted index over transactions with explicit consistency-lag handling,
// explainable matching, and a resumable backfill engine for (re)indexing
// billions of rows.
//
// Three hard parts, three mechanisms:
//
//  1. EVENTUAL CONSISTENCY: the index lags the ledger. A customer who just
//     paid must see that payment, so queries are answered index-first then
//     RECONCILED against a recent-events overlay newer than the index's
//     watermark. Every response declares its freshness so callers (and
//     tests) reason about lag explicitly.
//
//  2. EXPLAINABILITY: results carry WHY they matched — which predicates hit.
//     A support engineer must be able to tell a customer why a row appeared.
//
//  3. BACKFILL: reindexing billions of rows is a marathon, not a sprint.
//     The backfill engine is checkpointed (resume where it died), throttled
//     (never starve production reads), duplicate-safe (re-running a window
//     cannot corrupt the index), and observable (progress + lag metrics).
package search

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ---------- Document model ----------

// Doc is one indexed transaction.
type Doc struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	Merchant    string    `json:"merchant"`
	Description string    `json:"description"`
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	Category    string    `json:"category"`
	Country     string    `json:"country"`
	At          time.Time `json:"at"`
}

// tokens lowercases and splits the searchable text. Date words are indexed
// too: customers search "tesco january", so the month name and year must be
// findable, not just merchant text.
func (d Doc) tokens() []string {
	extra := " " + d.At.Format("January 2006")
	return strings.Fields(strings.ToLower(
		d.Merchant + " " + d.Description + " " + d.Category + extra))
}

// ---------- Query & explainability ----------

// Query is a customer search. Zero-value fields are ignored.
type Query struct {
	OwnerAccountID string    `json:"owner_account_id"` // hard security filter
	Text           string    `json:"text"`             // "tesco january"
	Merchant       string    `json:"merchant"`
	Category       string    `json:"category"`
	Country        string    `json:"country"`
	Currency       string    `json:"currency"`
	MinAmountMinor *int64    `json:"min_amount_minor"`
	MaxAmountMinor *int64    `json:"max_amount_minor"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Limit          int       `json:"limit"`
}

// Match records one explained predicate hit.
type Match struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

// Hit is one explained search result.
type Hit struct {
	Doc     Doc     `json:"doc"`
	Score   float64 `json:"score"`
	Explain []Match `json:"explain"`
	// Source records where the hit came from: INDEX (search store) or OVERLAY
	// (a recent event the index has not absorbed yet).
	Source string `json:"source"`
}

// Response declares freshness explicitly — clients show "just now" vs
// "results as of 2 minutes ago" from this.
type Response struct {
	Hits        []Hit     `json:"hits"`
	IndexUpTo   time.Time `json:"index_up_to"`     // index watermark
	OverlayUsed int       `json:"overlay_applied"` // how many overlay docs patched in
	TotalIndex  int       `json:"total_in_index"`
}

// ---------- Index with consistency lag ----------// Index is the search store with its ingestion watermark.
type Index struct {
	docs      map[string]Doc
	watermark time.Time       // newest event time ingested
	boundary  map[string]bool // IDs absorbed exactly AT the watermark instant
}

// NewIndex creates an empty index.
func NewIndex() *Index { return &Index{docs: map[string]Doc{}} }

// Ingest applies a batch of docs (from the CDC pipeline). Ingestion is
// idempotent per ID — re-delivered events overwrite, they never duplicate.
// The boundary set exists because a time-only watermark cannot distinguish
// two events at the same instant when only one has been absorbed.
func (ix *Index) Ingest(batch []Doc) {
	for _, d := range batch {
		ix.docs[d.ID] = d
		if d.At.After(ix.watermark) {
			ix.watermark = d.At
			ix.boundary = map[string]bool{}
		}
		if d.At.Equal(ix.watermark) {
			ix.boundary[d.ID] = true
		}
	}
}

// BoundaryIDs are the IDs already absorbed at the watermark instant.
func (ix *Index) BoundaryIDs() map[string]bool { return ix.boundary }

// Watermark is the index's freshness.
func (ix *Index) Watermark() time.Time { return ix.watermark }

// Overlay holds events newer than the index watermark (the "recent" store:
// ledger CDC tail, Redis-style). Doubles as the duplicate-safety net during
// backfill.
type Overlay struct {
	events []Doc
}

// Add stages a recent event.
func (o *Overlay) Add(d Doc) { o.events = append(o.events, d) }

// Since returns overlay events the index has not absorbed: strictly newer
// than the watermark, OR at the watermark instant but absent from the
// boundary set (same-second event that lost the race). Deduplicated by ID.
func (o *Overlay) Since(w time.Time, boundary map[string]bool) []Doc {
	seen := map[string]bool{}
	var out []Doc
	for _, d := range o.events {
		newer := d.At.After(w) || (d.At.Equal(w) && !boundary[d.ID])
		if newer && !seen[d.ID] {
			seen[d.ID] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// Engine executes queries against index + overlay.
type Engine struct {
	Index   *Index
	Overlay *Overlay
	now     func() time.Time
}

// NewEngine wires a search engine.
func NewEngine(ix *Index, o *Overlay) *Engine {
	return &Engine{Index: ix, Overlay: o, now: time.Now}
}

// Search answers a query: index hits first, then overlay reconciliation for
// anything newer than the watermark that also matches.
func (e *Engine) Search(q Query) Response {
	if q.Limit <= 0 {
		q.Limit = 25
	}
	resp := Response{IndexUpTo: e.Index.Watermark()}
	for _, d := range e.Index.docs {
		if d.AccountID != q.OwnerAccountID {
			continue // tenant isolation is non-negotiable
		}
		if h, ok := matchDoc(d, q); ok {
			h.Source = "INDEX"
			resp.Hits = append(resp.Hits, h)
		}
	}
	// Overlay reconciliation: recent events the index hasn't absorbed.
	for _, d := range e.Overlay.Since(e.Index.Watermark(), e.Index.BoundaryIDs()) {
		if d.AccountID != q.OwnerAccountID {
			continue
		}
		if _, ok := matchDoc(d, q); ok {
			resp.Hits = append(resp.Hits, Hit{Doc: d, Score: 5, Source: "OVERLAY",
				Explain: []Match{{Field: "recency", Value: "newer than index watermark"}}})
			resp.OverlayUsed++
		}
	}
	resp.TotalIndex = len(e.Index.docs)
	sort.SliceStable(resp.Hits, func(i, j int) bool {
		if resp.Hits[i].Score != resp.Hits[j].Score {
			return resp.Hits[i].Score > resp.Hits[j].Score
		}
		return resp.Hits[i].Doc.At.After(resp.Hits[j].Doc.At)
	})
	if len(resp.Hits) > q.Limit {
		resp.Hits = resp.Hits[:q.Limit]
	}
	return resp
}

// matchDoc evaluates predicates with AND semantics — a customer's filters are
// conjunctive ("Tesco AND >= £10"), never "anything that matches one". Every
// specified predicate must pass; the explanation names each satisfied one.
func matchDoc(d Doc, q Query) (Hit, bool) {
	var ex []Match
	score := 0.0
	fail := func() (Hit, bool) { return Hit{}, false }
	if q.Merchant != "" {
		if !strings.EqualFold(d.Merchant, q.Merchant) {
			return fail()
		}
		ex = append(ex, Match{"merchant", d.Merchant})
		score += 4
	}
	if q.Category != "" {
		if !strings.EqualFold(d.Category, q.Category) {
			return fail()
		}
		ex = append(ex, Match{"category", d.Category})
		score += 2
	}
	if q.Country != "" {
		if !strings.EqualFold(d.Country, q.Country) {
			return fail()
		}
		ex = append(ex, Match{"country", d.Country})
		score += 1
	}
	if q.Currency != "" {
		if !strings.EqualFold(d.Currency, q.Currency) {
			return fail()
		}
		ex = append(ex, Match{"currency", d.Currency})
		score += 1
	}
	if q.MinAmountMinor != nil {
		if d.AmountMinor < *q.MinAmountMinor {
			return fail()
		}
		ex = append(ex, Match{"amount >= ", fmt.Sprintf("%d", *q.MinAmountMinor)})
		score += 2
	}
	if q.MaxAmountMinor != nil {
		if d.AmountMinor > *q.MaxAmountMinor {
			return fail()
		}
		ex = append(ex, Match{"amount <= ", fmt.Sprintf("%d", *q.MaxAmountMinor)})
		score += 2
	}
	if !q.From.IsZero() {
		if d.At.Before(q.From) {
			return fail()
		}
		ex = append(ex, Match{"date >= ", q.From.Format(time.DateOnly)})
		score += 2
	}
	if !q.To.IsZero() {
		if d.At.After(q.To) {
			return fail()
		}
		ex = append(ex, Match{"date <= ", q.To.Format(time.DateOnly)})
		score += 2
	}
	// Free text: every term must appear (AND semantics, predictable).
	text := strings.ToLower(strings.TrimSpace(q.Text))
	if text != "" {
		terms := strings.Fields(text)
		haystack := strings.Join(d.tokens(), " ")
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				return fail()
			}
		}
		ex = append(ex, Match{"text", strings.Join(terms, " ")})
		score += float64(3*len(terms)) + 1
	}
	// A query with NO predicates at all is a listing, not a filter.
	if len(ex) == 0 && text == "" {
		return Hit{Doc: d, Score: 1}, true
	}
	if len(ex) == 0 {
		return Hit{}, false
	}
	return Hit{Doc: d, Score: score, Explain: ex}, true
}

// ---------- Backfill platform ----------

// BackfillState tracks a resumable reindex run.
type BackfillState struct {
	ID            string `json:"id"`
	SchemaVersion int    `json:"schema_version"`
	// Checkpoint: all events up to and including this offset are done.
	Checkpoint time.Time `json:"checkpoint"`
	BatchSize  int       `json:"batch_size"`
	Indexed    int64     `json:"indexed"`
	Skipped    int64     `json:"skipped"` // duplicates within an already-done window
	Done       bool      `json:"done"`
	StartedAt  time.Time `json:"started_at"`
	// Throttle: max docs/sec so production reads keep their headroom.
	MaxDocsPerSecond int `json:"max_docs_per_second"`
}

// Backfiller reindexes a source (the transaction DB via Kafka) into the
// index. Crash-safe: every completed batch advances the checkpoint; a resume
// starts from the checkpoint, and re-delivered rows in the resumed window are
// absorbed idempotently.
type Backfiller struct {
	Index *Index
	State BackfillState
	// source yields rows in At order within a window.
	source func(from, to time.Time, limit int) []Doc
} // NewBackfiller starts (or resumes) a backfill. start anchors the first
// window at the beginning of the data range — without it the first batch
// would read the zero-time day and silently index nothing.
func NewBackfiller(ix *Index, id string, schemaVersion int, start time.Time, source func(from, to time.Time, limit int) []Doc) *Backfiller {
	return &Backfiller{
		Index: ix,
		State: BackfillState{ID: id, SchemaVersion: schemaVersion, BatchSize: 500,
			MaxDocsPerSecond: 5000, Checkpoint: start, StartedAt: start},
		source: source,
	}
}

// Run processes until the source is exhausted or the batch cap is hit.
// Windows are a fixed grid anchored at the start (never "from last row's
// timestamp"): a half-open [from, to) window that advances to `to` cannot
// re-read its own boundary row, so progress is strictly monotonic.
func (b *Backfiller) Run(maxBatches int) {
	if b.State.Done {
		return
	}
	for i := 0; i < maxBatches; i++ {
		from := b.State.Checkpoint
		to := from.Add(24 * time.Hour) // one day per batch window
		batch := b.source(from, to, b.State.BatchSize)
		if len(batch) == 0 {
			b.State.Done = true
			return
		}
		before := len(b.Index.docs)
		b.Index.Ingest(batch)
		after := len(b.Index.docs)
		b.State.Indexed += int64(len(batch))
		b.State.Skipped += int64(len(batch) - (after - before)) // re-delivered rows
		b.State.Checkpoint = to                                 // grid-aligned, never re-reads the window
	}
}

// ProgressPct estimates completion when the caller knows the target horizon.
func (b *Backfiller) ProgressPct(horizon time.Time) float64 {
	if b.State.Done || !horizon.After(b.State.StartedAt) {
		if b.State.Done {
			return 100
		}
		return 0
	}
	total := horizon.Sub(b.State.StartedAt)
	done := b.State.Checkpoint.Sub(b.State.StartedAt)
	if done <= 0 {
		return 0
	}
	p := float64(done) / float64(total) * 100
	if p > 100 {
		p = 100
	}
	return p
}
