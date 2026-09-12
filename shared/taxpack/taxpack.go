// Package taxpack builds UK tax-year packs from posted ledger entries:
//
//   - entries carry {category, amount, date, doc_id} for interest,
//     dividends, transactions, donations and expenses
//   - packs aggregate one UK tax year (6 April → 5 April) with DRAFT →
//     FINALIZED lifecycle (immutable once finalized)
//   - JSON + CSV renderers plus a manifest for the PDF/accountant
//     package listing doc ids.
package taxpack

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Entry categories aggregated into the pack.
const (
	CategoryInterest     = "INTEREST"
	CategoryDividends    = "DIVIDENDS"
	CategoryTransactions = "TRANSACTIONS"
	CategoryDonations    = "DONATIONS"
	CategoryExpenses     = "EXPENSES"
)

// AllCategories lists every pack category.
var AllCategories = []string{
	CategoryInterest, CategoryDividends, CategoryTransactions,
	CategoryDonations, CategoryExpenses,
}

// Pack statuses.
const (
	StatusDraft     = "DRAFT"
	StatusFinalized = "FINALIZED"
)

var (
	// ErrNotFound is returned for unknown packs.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned for bad categories, amounts or dates.
	ErrInvalid = errors.New("invalid request")
	// ErrFinalized is returned when a finalized pack is mutated.
	ErrFinalized = errors.New("pack is finalized and immutable")
)

// Entry is one posted tax-relevant line.
type Entry struct {
	ID       string    `json:"id"`
	Category string    `json:"category"`
	Amount   int64     `json:"amount_minor"`
	Currency string    `json:"currency"`
	Date     time.Time `json:"date"`
	DocID    string    `json:"doc_id,omitempty"`
}

// Pack is one UK tax-year aggregation.
type Pack struct {
	Year        int              `json:"year"` // tax-year start year, e.g. 2024 == 2024/25
	Label       string           `json:"label"`
	Status      string           `json:"status"`
	Version     int              `json:"version"`
	Totals      map[string]int64 `json:"totals"`
	Count       int              `json:"count"`
	EntryIDs    []string         `json:"entry_ids"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	FinalizedAt *time.Time       `json:"finalized_at,omitempty"`
}

// Manifest lists the accountant-package contents for one pack.
type Manifest struct {
	Year     int              `json:"year"`
	Label    string           `json:"label"`
	Status   string           `json:"status"`
	Version  int              `json:"version"`
	Totals   map[string]int64 `json:"totals"`
	DocIDs   []string         `json:"doc_ids"`
	EntryIDs []string         `json:"entry_ids"`
	Count    int              `json:"count"`
}

// Store is the mutex-guarded in-memory tax-pack engine.
type Store struct {
	mu      sync.RWMutex
	entries map[string]*Entry
	packs   map[int]*Pack
}

// NewStore builds an empty tax-pack store.
func NewStore() *Store {
	return &Store{
		entries: map[string]*Entry{},
		packs:   map[int]*Pack{},
	}
}

func validCategory(c string) bool {
	switch strings.ToUpper(strings.TrimSpace(c)) {
	case CategoryInterest, CategoryDividends, CategoryTransactions,
		CategoryDonations, CategoryExpenses:
		return true
	default:
		return false
	}
}

// TaxYearStartFor maps a calendar date to its UK tax-year start year.
func TaxYearStartFor(t time.Time) int {
	y, m, d := t.Date()
	if m > time.April || (m == time.April && d >= 6) {
		return y
	}
	return y - 1
}

// TaxYearRange returns [start, end] for a tax-year start (end inclusive at
// 23:59:59 on 5 April of the following year).
func TaxYearRange(year int) (time.Time, time.Time) {
	start := time.Date(year, time.April, 6, 0, 0, 0, 0, time.UTC)
	end := time.Date(year+1, time.April, 5, 23, 59, 59, 0, time.UTC)
	return start, end
}

// TaxYearLabel formats a tax-year start as "YYYY/YY".
func TaxYearLabel(start int) string {
	return fmt.Sprintf("%d/%02d", start, (start+1)%100)
}

// PostEntry records one tax-relevant line. Posting into a finalized tax year
// is rejected to preserve pack immutability.
func (s *Store) PostEntry(category string, amount int64, date time.Time, docID string) (*Entry, error) {
	category = strings.ToUpper(strings.TrimSpace(category))
	if !validCategory(category) {
		return nil, fmt.Errorf("%w: category must be one of INTEREST, DIVIDENDS, TRANSACTIONS, DONATIONS, EXPENSES", ErrInvalid)
	}
	if amount <= 0 {
		return nil, fmt.Errorf("%w: amount must be positive", ErrInvalid)
	}
	if date.IsZero() {
		return nil, fmt.Errorf("%w: date is required", ErrInvalid)
	}
	year := TaxYearStartFor(date)
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.packs[year]; ok && p.Status == StatusFinalized {
		return nil, fmt.Errorf("tax year %d: %w", year, ErrFinalized)
	}
	e := &Entry{
		ID:       uuid.NewString(),
		Category: category,
		Amount:   amount,
		Currency: "GBP",
		Date:     date.UTC(),
		DocID:    strings.TrimSpace(docID),
	}
	s.entries[e.ID] = e
	out := *e
	return &out, nil
}

// BuildPack aggregates every entry in the UK tax year into a DRAFT pack
// (version bumps on each rebuild). Rebuilding a FINALIZED pack is rejected.
func (s *Store) BuildPack(year int) (*Pack, error) {
	if year < 1900 || year > 2100 {
		return nil, fmt.Errorf("%w: year must be a tax-year start like 2024", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.packs[year]; ok && p.Status == StatusFinalized {
		return nil, fmt.Errorf("tax year %d: %w", year, ErrFinalized)
	}
	start, end := TaxYearRange(year)
	totals := map[string]int64{}
	for _, c := range AllCategories {
		totals[c] = 0
	}
	var ids []string
	for _, e := range s.entries {
		if e.Date.Before(start) || e.Date.After(end) {
			continue
		}
		totals[e.Category] += e.Amount
		ids = append(ids, e.ID)
	}
	sort.Strings(ids)
	now := time.Now().UTC()
	version := 1
	if p, ok := s.packs[year]; ok {
		version = p.Version + 1
	}
	pack := &Pack{
		Year:      year,
		Label:     TaxYearLabel(year),
		Status:    StatusDraft,
		Version:   version,
		Totals:    totals,
		Count:     len(ids),
		EntryIDs:  ids,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if existing, ok := s.packs[year]; ok {
		pack.CreatedAt = existing.CreatedAt
	}
	s.packs[year] = pack
	out := clonePack(pack)
	return &out, nil
}

// Finalize moves a DRAFT pack to FINALIZED (immutable). Double-finalize and
// finalizing without a build are errors.
func (s *Store) Finalize(year int) (*Pack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.packs[year]
	if !ok {
		return nil, fmt.Errorf("pack %d: %w", year, ErrNotFound)
	}
	if p.Status == StatusFinalized {
		return nil, fmt.Errorf("pack %d: %w", year, ErrFinalized)
	}
	now := time.Now().UTC()
	p.Status = StatusFinalized
	p.FinalizedAt = &now
	p.UpdatedAt = now
	out := clonePack(p)
	return &out, nil
}

// GetPack returns one pack by tax-year start.
func (s *Store) GetPack(year int) (*Pack, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.packs[year]
	if !ok {
		return nil, fmt.Errorf("pack %d: %w", year, ErrNotFound)
	}
	out := clonePack(p)
	return &out, nil
}

// RenderJSON renders one pack plus its entries as JSON.
func (s *Store) RenderJSON(year int) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.packs[year]
	if !ok {
		return nil, fmt.Errorf("pack %d: %w", year, ErrNotFound)
	}
	start, end := TaxYearRange(year)
	var entries []Entry
	for _, id := range p.EntryIDs {
		if e, ok := s.entries[id]; ok && !e.Date.Before(start) && !e.Date.After(end) {
			entries = append(entries, *e)
		}
	}
	if entries == nil {
		entries = []Entry{}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	payload := map[string]interface{}{
		"pack":    clonePack(p),
		"entries": entries,
	}
	return json.MarshalIndent(payload, "", "  ")
}

// RenderCSV renders one pack's per-category totals as CSV.
func (s *Store) RenderCSV(year int) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.packs[year]
	if !ok {
		return "", fmt.Errorf("pack %d: %w", year, ErrNotFound)
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"year", "label", "category", "total_minor", "status", "version"})
	for _, c := range AllCategories {
		_ = w.Write([]string{
			fmt.Sprint(p.Year), p.Label, c,
			fmt.Sprint(p.Totals[c]), p.Status, fmt.Sprint(p.Version),
		})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// Manifest lists the accountant-package contents: entry ids + doc ids.
func (s *Store) Manifest(year int) (*Manifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.packs[year]
	if !ok {
		return nil, fmt.Errorf("pack %d: %w", year, ErrNotFound)
	}
	docSet := map[string]bool{}
	for _, id := range p.EntryIDs {
		if e, ok := s.entries[id]; ok && strings.TrimSpace(e.DocID) != "" {
			docSet[e.DocID] = true
		}
	}
	var docs []string
	for d := range docSet {
		docs = append(docs, d)
	}
	sort.Strings(docs)
	if docs == nil {
		docs = []string{}
	}
	totals := map[string]int64{}
	for k, v := range p.Totals {
		totals[k] = v
	}
	ids := append([]string(nil), p.EntryIDs...)
	return &Manifest{
		Year:     p.Year,
		Label:    p.Label,
		Status:   p.Status,
		Version:  p.Version,
		Totals:   totals,
		DocIDs:   docs,
		EntryIDs: ids,
		Count:    p.Count,
	}, nil
}

func clonePack(p *Pack) Pack {
	out := *p
	totals := map[string]int64{}
	for k, v := range p.Totals {
		totals[k] = v
	}
	out.Totals = totals
	out.EntryIDs = append([]string(nil), p.EntryIDs...)
	return out
}
