// Package regreport implements the Regulatory Reporting Pipeline with
// Evidence Provenance.
//
// The pipeline: operational data → transformation → regulatory dataset →
// validation → report → submission. The differentiating feature is that every
// NUMBER in a submitted report carries its evidence chain: which source
// datasets, which query, which transformation version produced it — so the
// regulator's question "where exactly did this figure come from?" has a
// machine-checkable answer, not a narrative.
package regreport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrValidationFailed = errors.New("report failed validation gates")
	ErrAlreadySubmitted = errors.New("report already submitted")
	ErrNotValidated     = errors.New("report must pass validation before submission")
	ErrUnknownFigure    = errors.New("figure not found in report")
)

// Provenance is the evidence chain for ONE number in the report.
type Provenance struct {
	// Figure the chain explains.
	FigureKey   string `json:"figure_key"`
	FigureValue int64  `json:"figure_value"` // minor units or counts
	// Source datasets read (e.g. "ledger.entries@2026-08").
	Sources []string `json:"sources"`
	// Query that extracted the raw rows.
	Query string `json:"query"`
	// Transform version that shaped the raw rows into the figure.
	Transform    string `json:"transform"`
	TransformVer string `json:"transform_version"`
	// Row counts for reconciliation.
	RowsIn  int64 `json:"rows_in"`
	RowsOut int64 `json:"rows_out"`
	// ComputedAt and a content hash make the chain verifiable.
	ComputedAt time.Time `json:"computed_at"`
	ChainHash  string    `json:"chain_hash"`
}

// Report is a regulatory report under construction.
type Report struct {
	ReportID      string       `json:"report_id"`
	Regime        string       `json:"regime"` // e.g. "FCA_COR007", "HMRC_FI_RETURN"
	Period        string       `json:"period"` // e.g. "2026-08"
	Status        string       `json:"status"` // DRAFT → VALIDATED → SUBMITTED
	Figures       []Provenance `json:"figures"`
	CreatedAt     time.Time    `json:"created_at"`
	ValidatedAt   *time.Time   `json:"validated_at,omitempty"`
	SubmittedAt   *time.Time   `json:"submitted_at,omitempty"`
	SubmissionRef string       `json:"submission_ref,omitempty"`
}

// Builder assembles reports figure-by-figure with provenance.
type Builder struct {
	mu      sync.Mutex
	reports map[string]*Report
}

func NewBuilder() *Builder {
	return &Builder{reports: map[string]*Report{}}
}

// CreateReport opens a draft.
func (b *Builder) CreateReport(reportID, regime, period string) (*Report, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.reports[reportID]; ok {
		return nil, fmt.Errorf("report %s already exists", reportID)
	}
	r := &Report{ReportID: reportID, Regime: regime, Period: period, Status: "DRAFT", CreatedAt: time.Now().UTC()}
	b.reports[reportID] = r
	return r, nil
}

// AddFigure records a figure WITH its evidence chain. A figure without
// provenance is rejected — unexplained numbers never reach a submission.
func (b *Builder) AddFigure(reportID string, p Provenance) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.reports[reportID]
	if !ok {
		return fmt.Errorf("report %s not found", reportID)
	}
	if r.Status != "DRAFT" {
		return fmt.Errorf("cannot add figures to a %s report", r.Status)
	}
	if p.FigureKey == "" || p.Query == "" || p.Transform == "" || len(p.Sources) == 0 {
		return fmt.Errorf("figure %q missing provenance: sources, query and transform are all required", p.FigureKey)
	}
	if p.ComputedAt.IsZero() {
		p.ComputedAt = time.Now().UTC()
	}
	p.ChainHash = chainHash(p)
	r.Figures = append(r.Figures, p)
	return nil
}

// chainHash binds the evidence chain content — auditors re-hash and compare.
func chainHash(p Provenance) string {
	h := sha256.New()
	fmt.Fprintf(h, "key=%s|value=%d\n", p.FigureKey, p.FigureValue)
	src := append([]string(nil), p.Sources...)
	sort.Strings(src)
	for _, s := range src {
		fmt.Fprintf(h, "source=%s\n", s)
	}
	fmt.Fprintf(h, "query=%s|transform=%s@%s|rows=%d/%d|at=%s\n",
		p.Query, p.Transform, p.TransformVer, p.RowsIn, p.RowsOut, p.ComputedAt.UTC().Format(time.RFC3339Nano))
	return hex.EncodeToString(h.Sum(nil))
}

// Validate runs the report's validation gates. All must pass for the report
// to move DRAFT → VALIDATED.
func (b *Builder) Validate(reportID string, gates []ValidationGate) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.reports[reportID]
	if !ok {
		return fmt.Errorf("report %s not found", reportID)
	}
	if r.Status != "DRAFT" {
		return fmt.Errorf("report %s is %s, not DRAFT", reportID, r.Status)
	}
	if len(r.Figures) == 0 {
		return fmt.Errorf("%w: report has no figures", ErrValidationFailed)
	}
	for _, g := range gates {
		if err := g.Validate(r); err != nil {
			return fmt.Errorf("%w: gate %q: %v", ErrValidationFailed, g.Name(), err)
		}
	}
	now := time.Now().UTC()
	r.ValidatedAt = &now
	r.Status = "VALIDATED"
	return nil
}

// ValidationGate is one check over the assembled report.
type ValidationGate interface {
	Name() string
	Validate(r *Report) error
}

// ReconciliationGate checks rows_in/rows_out coherence per figure.
type ReconciliationGate struct{ MaxLossRatio float64 }

func (ReconciliationGate) Name() string { return "reconciliation" }
func (g ReconciliationGate) Validate(r *Report) error {
	for _, f := range r.Figures {
		if f.RowsIn == 0 {
			continue
		}
		loss := 1 - float64(f.RowsOut)/float64(f.RowsIn)
		if loss < 0 {
			loss = -loss
		}
		if loss > g.MaxLossRatio {
			return fmt.Errorf("figure %s lost %.2f%% of rows (max %.2f%%)", f.FigureKey, loss*100, g.MaxLossRatio*100)
		}
	}
	return nil
}

// CompletenessGate requires the report to carry every mandatory figure key.
type CompletenessGate struct{ Required []string }

func (CompletenessGate) Name() string { return "completeness" }
func (g CompletenessGate) Validate(r *Report) error {
	have := map[string]bool{}
	for _, f := range r.Figures {
		have[f.FigureKey] = true
	}
	for _, k := range g.Required {
		if !have[k] {
			return fmt.Errorf("mandatory figure %q missing", k)
		}
	}
	return nil
}

// VarianceGate compares against the prior period; big unexplained swings are
// the classic regulator query, so catch them before submission.
type VarianceGate struct {
	Prior     map[string]int64 // figure_key → prior value
	MaxPctBps int64            // allowed variance in basis points
}

func (VarianceGate) Name() string { return "variance" }
func (g VarianceGate) Validate(r *Report) error {
	for _, f := range r.Figures {
		prior, ok := g.Prior[f.FigureKey]
		if !ok || prior == 0 {
			continue
		}
		var bps int64
		if f.FigureValue >= prior {
			bps = (f.FigureValue - prior) * 10000 / prior
		} else {
			bps = (prior - f.FigureValue) * 10000 / prior
		}
		if bps > g.MaxPctBps {
			return fmt.Errorf("figure %s moved %d bps vs prior period (limit %d)", f.FigureKey, bps, g.MaxPctBps)
		}
	}
	return nil
}

// Submit finalises the report. Only VALIDATED reports can be submitted, and
// submission is immutable — corrections require a NEW report version.
func (b *Builder) Submit(reportID string, submissionRef string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.reports[reportID]
	if !ok {
		return fmt.Errorf("report %s not found", reportID)
	}
	if r.Status == "SUBMITTED" {
		return ErrAlreadySubmitted
	}
	if r.Status != "VALIDATED" {
		return ErrNotValidated
	}
	now := time.Now().UTC()
	r.SubmittedAt = &now
	r.SubmissionRef = submissionRef
	r.Status = "SUBMITTED"
	return nil
}

// Get returns the report (for API reads).
func (b *Builder) Get(reportID string) (*Report, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.reports[reportID]
	if !ok {
		return nil, fmt.Errorf("report %s not found", reportID)
	}
	return r, nil
}

// EvidenceFor answers "where exactly did this number come from?" — the whole
// chain for one figure, hash-verifiable.
func (b *Builder) EvidenceFor(reportID, figureKey string) (*Provenance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.reports[reportID]
	if !ok {
		return nil, fmt.Errorf("report %s not found", reportID)
	}
	for _, f := range r.Figures {
		if f.FigureKey == figureKey {
			p := f
			// Re-hash to detect tampering with the stored chain.
			if chainHash(p) != p.ChainHash {
				return nil, fmt.Errorf("evidence chain for %s FAILED hash verification — tampered or corrupted", figureKey)
			}
			return &p, nil
		}
	}
	return nil, ErrUnknownFigure
}
