// Package docintel implements Nexora's document-intelligence platform:
//
//   - keyword-based classification over STATEMENT/TAX/INTEREST/LOAN/
//     INSURANCE/INVOICE/RECEIPT with confidence scoring
//   - metadata extraction (merchant, amount_minor, currency, date,
//     UK tax year) from parser output text at demo scale
//   - encrypted-at-rest marker (sha256 content hash + key id)
//   - full-text search index (token → doc ids) with structured queries
//     ("all X purchases above £N in YEAR", "mortgage interest document",
//     "total interest earned last tax year").
package docintel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Document kinds supported at demo scale.
const (
	KindStatement = "STATEMENT"
	KindTax       = "TAX"
	KindInterest  = "INTEREST"
	KindLoan      = "LOAN"
	KindInsurance = "INSURANCE"
	KindInvoice   = "INVOICE"
	KindReceipt   = "RECEIPT"
)

// AllKinds lists every supported document kind.
var AllKinds = []string{
	KindStatement, KindTax, KindInterest, KindLoan,
	KindInsurance, KindInvoice, KindReceipt,
}

// KeyID marks the encryption key guarding document content at rest.
const KeyID = "docintel-key-v1"

var (
	// ErrNotFound is returned for unknown document ids.
	ErrNotFound = errors.New("not found")
	// ErrInvalid is returned for bad kinds, empty filenames/content or
	// malformed queries.
	ErrInvalid = errors.New("invalid request")
)

// Classification is the keyword-model verdict for one document.
type Classification struct {
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
}

// Document is one ingested parser-output text plus its derived metadata.
type Document struct {
	ID             string         `json:"id"`
	DeclaredKind   string         `json:"declared_kind"`
	Filename       string         `json:"filename"`
	ContentHash    string         `json:"content_hash"`
	KeyID          string         `json:"key_id"`
	Classification Classification `json:"classification"`
	Merchant       string         `json:"merchant,omitempty"`
	AmountMinor    int64          `json:"amount_minor"`
	Currency       string         `json:"currency"`
	Date           *time.Time     `json:"date,omitempty"`
	TaxYear        string         `json:"tax_year,omitempty"`
	TaxYearStart   int            `json:"tax_year_start,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// Query is the structured search over the full-text index plus metadata
// filters. Text tokens are AND-matched (case-insensitive); Kind,
// MinAmountMinor, Year and TaxYear narrow the result further.
type Query struct {
	Text           string
	Kind           string
	MinAmountMinor int64
	Year           int
	TaxYear        int // UK tax-year start year, e.g. 2024 == 2024/25
}

// KindTotal aggregates one kind for the totals endpoint.
type KindTotal struct {
	Kind       string `json:"kind"`
	Count      int    `json:"count"`
	TotalMinor int64  `json:"total_minor"`
	Currency   string `json:"currency"`
}

// Store is the mutex-guarded in-memory document intelligence engine.
type Store struct {
	mu    sync.RWMutex
	docs  map[string]*Document
	index map[string]map[string]bool // token -> doc ids
}

// NewStore builds an empty document store.
func NewStore() *Store {
	return &Store{
		docs:  map[string]*Document{},
		index: map[string]map[string]bool{},
	}
}

func validKind(k string) bool {
	switch strings.ToUpper(strings.TrimSpace(k)) {
	case KindStatement, KindTax, KindInterest, KindLoan,
		KindInsurance, KindInvoice, KindReceipt:
		return true
	default:
		return false
	}
}

// Ingest stores one parser-output text, classifying it, extracting metadata,
// marking encryption-at-rest and indexing its tokens.
func (s *Store) Ingest(kind, filename, contentText string) (*Document, error) {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if !validKind(kind) {
		return nil, fmt.Errorf("%w: kind must be one of STATEMENT, TAX, INTEREST, LOAN, INSURANCE, INVOICE, RECEIPT", ErrInvalid)
	}
	if strings.TrimSpace(filename) == "" {
		return nil, fmt.Errorf("%w: filename is required", ErrInvalid)
	}
	if strings.TrimSpace(contentText) == "" {
		return nil, fmt.Errorf("%w: content_text is required", ErrInvalid)
	}
	class := classifyKeywords(kind, contentText)
	merchant := extractMerchant(contentText)
	amount, currency := extractAmount(contentText)
	date := extractDate(contentText)
	taxYear, taxStart := "", 0
	if date != nil {
		taxStart = TaxYearStartFor(*date)
		taxYear = TaxYearLabel(taxStart)
	}
	sum := sha256.Sum256([]byte(contentText))
	doc := &Document{
		ID:             uuid.NewString(),
		DeclaredKind:   kind,
		Filename:       strings.TrimSpace(filename),
		ContentHash:    hex.EncodeToString(sum[:]),
		KeyID:          KeyID,
		Classification: class,
		Merchant:       merchant,
		AmountMinor:    amount,
		Currency:       currency,
		Date:           date,
		TaxYear:        taxYear,
		TaxYearStart:   taxStart,
		CreatedAt:      time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *doc
	s.docs[doc.ID] = &cp
	for _, tok := range tokenize(contentText + " " + merchant + " " + filename) {
		set, ok := s.index[tok]
		if !ok {
			set = map[string]bool{}
			s.index[tok] = set
		}
		set[doc.ID] = true
	}
	out := cp
	return &out, nil
}

// Classify returns the stored keyword classification for one document.
func (s *Store) Classify(id string) (*Classification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.docs[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("document %q: %w", id, ErrNotFound)
	}
	out := doc.Classification
	return &out, nil
}

// Get returns one document by id.
func (s *Store) Get(id string) (*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.docs[strings.TrimSpace(id)]
	if !ok {
		return nil, fmt.Errorf("document %q: %w", id, ErrNotFound)
	}
	out := *doc
	return &out, nil
}

// Search runs a structured query over the full-text index with metadata
// filters. Text tokens are AND-matched; empty text skips the index.
func (s *Store) Search(q Query) ([]Document, error) {
	if q.Kind != "" && !validKind(q.Kind) {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalid, q.Kind)
	}
	if q.MinAmountMinor < 0 {
		return nil, fmt.Errorf("%w: min_amount_minor must be >= 0", ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var candidateIDs map[string]bool
	tokens := tokenize(q.Text)
	if len(tokens) == 0 {
		candidateIDs = map[string]bool{}
		for id := range s.docs {
			candidateIDs[id] = true
		}
	} else {
		for i, tok := range tokens {
			set, ok := s.index[tok]
			if !ok {
				return []Document{}, nil
			}
			if i == 0 {
				candidateIDs = map[string]bool{}
				for id := range set {
					candidateIDs[id] = true
				}
			} else {
				for id := range candidateIDs {
					if !set[id] {
						delete(candidateIDs, id)
					}
				}
			}
			if len(candidateIDs) == 0 {
				return []Document{}, nil
			}
		}
	}
	kind := strings.ToUpper(strings.TrimSpace(q.Kind))
	var out []Document
	for id := range candidateIDs {
		doc := s.docs[id]
		if kind != "" && doc.Classification.Kind != kind {
			continue
		}
		if q.MinAmountMinor > 0 && doc.AmountMinor < q.MinAmountMinor {
			continue
		}
		if q.Year != 0 {
			if doc.Date == nil || doc.Date.Year() != q.Year {
				continue
			}
		}
		if q.TaxYear != 0 && doc.TaxYearStart != q.TaxYear {
			continue
		}
		out = append(out, *doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if out == nil {
		out = []Document{}
	}
	return out, nil
}

// TotalByKind aggregates count + amount for one classified kind.
func (s *Store) TotalByKind(kind string) (*KindTotal, error) {
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if !validKind(kind) {
		return nil, fmt.Errorf("%w: unknown kind %q", ErrInvalid, kind)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	total := &KindTotal{Kind: kind, Currency: "GBP"}
	for _, doc := range s.docs {
		if doc.Classification.Kind != kind {
			continue
		}
		total.Count++
		total.TotalMinor += doc.AmountMinor
		if total.Currency == "GBP" && doc.Currency != "" {
			total.Currency = doc.Currency
		}
	}
	return total, nil
}

// Totals aggregates every kind (including zero rows for kinds with no docs).
func (s *Store) Totals() []KindTotal {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byKind := map[string]*KindTotal{}
	for _, k := range AllKinds {
		byKind[k] = &KindTotal{Kind: k, Currency: "GBP"}
	}
	for _, doc := range s.docs {
		t := byKind[doc.Classification.Kind]
		if t == nil {
			continue
		}
		t.Count++
		t.TotalMinor += doc.AmountMinor
		if t.Currency == "GBP" && doc.Currency != "" {
			t.Currency = doc.Currency
		}
	}
	out := make([]KindTotal, 0, len(AllKinds))
	for _, k := range AllKinds {
		out = append(out, *byKind[k])
	}
	return out
}

// ── classification ──────────────────────────────────────────────────────────

var kindKeywords = map[string][]string{
	KindStatement: {"statement", "balance", "sort code", "account number", "transactions"},
	KindTax:       {"hmrc", "tax year", "self assessment", "p60", "p45", "tax return", "taxable"},
	KindInterest:  {"interest", "apr", "savings", "mortgage interest", "interest earned", "interest rate"},
	KindLoan:      {"loan", "repayment", "principal", "amortisation", "amortization", "borrower"},
	KindInsurance: {"insurance", "premium", "policy", "cover", "underwriter"},
	KindInvoice:   {"invoice", "vat", "bill to", "due date", "payment terms", "invoiced"},
	KindReceipt:   {"receipt", "till", "change", "paid", "purchase", "point of sale"},
}

func classifyKeywords(declared, content string) Classification {
	lower := strings.ToLower(content)
	hits := map[string]int{}
	total := 0
	for kind, kws := range kindKeywords {
		for _, kw := range kws {
			n := strings.Count(lower, kw)
			if n > 0 {
				hits[kind] += n
				total += n
			}
		}
	}
	if total == 0 {
		return Classification{Kind: declared, Confidence: 0.3}
	}
	best, bestHits := declared, -1
	for _, k := range AllKinds {
		if hits[k] > bestHits {
			best, bestHits = k, hits[k]
		}
	}
	if bestHits <= 0 {
		return Classification{Kind: declared, Confidence: 0.3}
	}
	conf := 0.55 + 0.45*float64(bestHits)/float64(total)
	if conf > 0.99 {
		conf = 0.99
	}
	conf = float64(int(conf*100+0.5)) / 100
	return Classification{Kind: best, Confidence: conf}
}

// ── metadata extraction ─────────────────────────────────────────────────────

var (
	poundRe    = regexp.MustCompile(`£\s?([\d,]+(?:\.\d{1,2})?)`)
	gbpRe      = regexp.MustCompile(`(?i)GBP\s?([\d,]+(?:\.\d{1,2})?)`)
	usdRe      = regexp.MustCompile(`\$\s?([\d,]+(?:\.\d{1,2})?)`)
	eurRe      = regexp.MustCompile(`(?i)(?:€|EUR)\s?([\d,]+(?:\.\d{1,2})?)`)
	isoDateRe  = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
	ukDateRe   = regexp.MustCompile(`(\d{1,2})/(\d{1,2})/(\d{4})`)
	textDateRe = regexp.MustCompile(`(?i)(\d{1,2})\s+(jan|feb|mar|apr|may|jun|jul|aug|sep|sept|oct|nov|dec)[a-z]*\s+(\d{4})`)
	merchantRe = regexp.MustCompile(`(?im)^\s*(merchant|from|retailer|supplier|employer|provider|billed by)\s*[:\-]\s*(.+?)\s*$`)
)

func parseMoneyNumber(raw string) int64 {
	clean := strings.ReplaceAll(strings.TrimSpace(raw), ",", "")
	f, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0
	}
	return int64(f*100 + 0.5)
}

func extractAmount(content string) (int64, string) {
	if m := poundRe.FindStringSubmatch(content); m != nil {
		return parseMoneyNumber(m[1]), "GBP"
	}
	if m := gbpRe.FindStringSubmatch(content); m != nil {
		return parseMoneyNumber(m[1]), "GBP"
	}
	if m := eurRe.FindStringSubmatch(content); m != nil {
		return parseMoneyNumber(m[1]), "EUR"
	}
	if m := usdRe.FindStringSubmatch(content); m != nil {
		return parseMoneyNumber(m[1]), "USD"
	}
	return 0, "GBP"
}

func extractMerchant(content string) string {
	if m := merchantRe.FindStringSubmatch(content); m != nil {
		v := strings.TrimSpace(m[2])
		if len(v) > 80 {
			v = v[:80]
		}
		return v
	}
	for _, line := range strings.Split(content, "\n") {
		v := strings.TrimSpace(line)
		if len(v) < 3 || len(v) > 80 {
			continue
		}
		lower := strings.ToLower(v)
		if strings.Contains(lower, "statement") || strings.Contains(lower, "invoice") ||
			strings.Contains(lower, "receipt") || strings.Contains(lower, "hmrc") {
			continue
		}
		return v
	}
	return ""
}

func monthNameToNum(s string) int {
	switch strings.ToLower(s[:3]) {
	case "jan":
		return 1
	case "feb":
		return 2
	case "mar":
		return 3
	case "apr":
		return 4
	case "may":
		return 5
	case "jun":
		return 6
	case "jul":
		return 7
	case "aug":
		return 8
	case "sep":
		return 9
	case "oct":
		return 10
	case "nov":
		return 11
	case "dec":
		return 12
	}
	return 0
}

func extractDate(content string) *time.Time {
	if m := isoDateRe.FindStringSubmatch(content); m != nil {
		if t, err := time.Parse("2006-01-02", m[0]); err == nil {
			utc := t.UTC()
			return &utc
		}
	}
	if m := ukDateRe.FindStringSubmatch(content); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		if d >= 1 && d <= 31 && mo >= 1 && mo <= 12 && y >= 1900 && y <= 2100 {
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			return &t
		}
	}
	if m := textDateRe.FindStringSubmatch(content); m != nil {
		d, _ := strconv.Atoi(m[1])
		mo := monthNameToNum(m[2])
		y, _ := strconv.Atoi(m[3])
		if mo > 0 && d >= 1 && d <= 31 {
			t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
			return &t
		}
	}
	return nil
}

// TaxYearStartFor maps a calendar date to its UK tax-year start year
// (tax year runs 6 April → 5 April).
func TaxYearStartFor(t time.Time) int {
	y, m, d := t.Date()
	if m > time.April || (m == time.April && d >= 6) {
		return y
	}
	return y - 1
}

// TaxYearLabel formats a tax-year start as "YYYY/YY".
func TaxYearLabel(start int) string {
	return fmt.Sprintf("%d/%02d", start, (start+1)%100)
}

// tokenize lowercases content into alphanumeric tokens (len >= 2).
func tokenize(s string) []string {
	lower := strings.ToLower(s)
	var b strings.Builder
	for _, r := range lower {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	var out []string
	for _, tok := range strings.Fields(b.String()) {
		if len(tok) >= 2 {
			out = append(out, tok)
		}
	}
	return out
}
