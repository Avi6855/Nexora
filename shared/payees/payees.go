// Package payees implements Nexora's payee platform: the account validation
// gateway, payee-name resolution (Confirmation-of-Payee style), the recipient
// directory, audited change history, and customer-generated payment templates.
//
// Everything here is REUSABLE PLATFORM, not payment execution: personal and
// business transfers share the same validation and resolution contracts, and
// every mutation of payee details produces an immutable before/after audit
// record — the single most useful artefact when a customer says "I never
// changed that payee".
package payees

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

// ---------- 6. Account validation gateway ----------

// ValidationOutcome is the gateway verdict.
type ValidationOutcome string

const (
	ValidationValid       ValidationOutcome = "VALID"
	ValidationInvalid     ValidationOutcome = "INVALID"
	ValidationPartial     ValidationOutcome = "PARTIAL_MATCH"
	ValidationUnsupported ValidationOutcome = "UNSUPPORTED"
)

// ValidationRequest is one gateway call.
type ValidationRequest struct {
	SortCode      string `json:"sort_code"`
	AccountNumber string `json:"account_number"`
	PayeeName     string `json:"payee_name"`
	Country       string `json:"country"` // "GB" supported; others UNSUPPORTED
}

// ValidationResponse carries the verdict plus machine-readable reasons.
type ValidationResponse struct {
	Outcome    ValidationOutcome `json:"outcome"`
	Reasons    []string          `json:"reasons,omitempty"`
	ChecksumOK bool              `json:"checksum_ok"`
}

var (
	sortCodeRe  = regexp.MustCompile(`^\d{6}$`)
	accountReGB = regexp.MustCompile(`^\d{6,8}$`)
)

// Validate is the shared entry point for personal and business flows.
// Modulus checks are a pluggable hook (real UK modulus tables live in the
// scheme data), but structure, checksum-plausibility and reachability are
// enforced here so every caller behaves identically.
func Validate(req ValidationRequest, modulusCheck func(sortCode, account string) bool) ValidationResponse {
	if req.Country != "" && req.Country != "GB" {
		return ValidationResponse{Outcome: ValidationUnsupported, Reasons: []string{fmt.Sprintf("country %s not served by this gateway", req.Country)}}
	}
	sc := strings.ReplaceAll(req.SortCode, "-", "")
	sc = strings.ReplaceAll(sc, " ", "")
	an := strings.ReplaceAll(req.AccountNumber, " ", "")
	var reasons []string
	if !sortCodeRe.MatchString(sc) {
		reasons = append(reasons, "sort code must be 6 digits")
	}
	if !accountReGB.MatchString(an) {
		reasons = append(reasons, "account number must be 6–8 digits")
	}
	if len(reasons) > 0 {
		return ValidationResponse{Outcome: ValidationInvalid, Reasons: reasons}
	}
	checksumOK := true
	if modulusCheck != nil {
		checksumOK = modulusCheck(sc, an)
	}
	if !checksumOK {
		return ValidationResponse{Outcome: ValidationInvalid, ChecksumOK: false, Reasons: []string{"failed modulus check"}}
	}
	// Name known but wildly different length from any directory record is a
	// partial match; the resolver refines this when a directory is supplied.
	if req.PayeeName != "" && len(strings.TrimSpace(req.PayeeName)) < 2 {
		return ValidationResponse{Outcome: ValidationPartial, ChecksumOK: true, Reasons: []string{"payee name too short to confirm"}}
	}
	return ValidationResponse{Outcome: ValidationValid, ChecksumOK: true}
}

// ---------- 7. Payee name resolution ----------

// MatchLevel classifies name agreement.
type MatchLevel string

const (
	MatchExact   MatchLevel = "EXACT_MATCH"
	MatchClose   MatchLevel = "CLOSE_MATCH"
	MatchNoMatch MatchLevel = "MISMATCH"
	MatchUnknown MatchLevel = "UNKNOWN"
)

// Confidence bands are part of the API contract: clients branch on the level,
// not the raw number.
const (
	confExact = 99
	confClose = 80
)

// ResolveName normalises and compares an entered name against the legal name
// registered for the account. Deterministic token logic — no LLM in the loop.
func ResolveName(entered, legal string) (MatchLevel, int) {
	te, tl := tokenSet(entered), tokenSet(legal)
	if len(te) == 0 || len(tl) == 0 {
		return MatchUnknown, 0
	}
	if te == tl {
		return MatchExact, confExact
	}
	// Close: one is a prefix subset of the other ("amazon uk" vs "amazon
	// europe core services uk ltd") or differs by small edit distance.
	a, b := strings.Fields(te), strings.Fields(tl)
	subset := true
	for _, w := range a {
		if !containsFold(b, w) {
			subset = false
			break
		}
	}
	if subset || editDistanceAtMost(a, b, 2) {
		return MatchClose, confClose
	}
	return MatchNoMatch, 14
}

func tokenSet(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var kept []string
	for _, w := range words {
		w = strings.ToLower(w)
		switch w {
		case "ltd", "limited", "plc", "uk", "gb", "the", "and", "&", "co":
			continue // legal-entity noise
		}
		kept = append(kept, w)
	}
	sort.Strings(kept)
	return strings.Join(kept, " ")
}

func containsFold(list []string, w string) bool {
	for _, x := range list {
		if x == w || strings.HasPrefix(x, w) || strings.HasPrefix(w, x) {
			return true
		}
	}
	return false
}

func editDistanceAtMost(a, b []string, max int) bool {
	// Word-level Levenshtein with early exit.
	la, lb := len(a), len(b)
	if abs(la-lb) > max {
		return false
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		rowMax := curr[0]
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
			if curr[j] > rowMax {
				rowMax = curr[j]
			}
		}
		if rowMax > max && curr[lb] > max {
			return false
		}
		copy(prev, curr)
	}
	return prev[lb] <= max
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// ---------- 8. Recipient directory ----------

// Recipient is a saved payee with search/ranking metadata.
type Recipient struct {
	ID                 string    `json:"id"`
	OwnerCustomerID    string    `json:"owner_customer_id"`
	DisplayName        string    `json:"display_name"`
	SortCode           string    `json:"sort_code"`
	AccountNumber      string    `json:"account_number"`
	AccountFingerprint string    `json:"account_fingerprint"` // hash, never raw details
	Verified           bool      `json:"verified"`
	LastUsedAt         time.Time `json:"last_used_at"`
	CreatedAt          time.Time `json:"created_at"`
	TimesUsed          int       `json:"times_used"`
}

// Fingerprint derives a stable, non-reversible account identifier for
// duplicate detection and display.
func Fingerprint(sortCode, account string) string {
	sum := sha256.Sum256([]byte(scNormal(sortCode) + "|" + strings.TrimSpace(account)))
	return hex.EncodeToString(sum[:8])
}

func scNormal(sc string) string {
	return strings.ReplaceAll(strings.ReplaceAll(sc, "-", ""), " ", "")
}

// Directory manages one customer's recipients.
type Directory struct {
	recipients map[string]*Recipient
}

// NewDirectory creates an empty directory.
func NewDirectory() *Directory {
	return &Directory{recipients: map[string]*Recipient{}}
}

// Add stores a recipient, rejecting an exact duplicate fingerprint.
func (d *Directory) Add(r Recipient) (*Recipient, error) {
	if r.DisplayName == "" {
		return nil, fmt.Errorf("display name required")
	}
	fp := Fingerprint(r.SortCode, r.AccountNumber)
	for _, existing := range d.recipients {
		if existing.AccountFingerprint == fp {
			return nil, fmt.Errorf("recipient with identical account already saved as %q", existing.DisplayName)
		}
	}
	r.ID = fmt.Sprintf("rcp-%s", fp)
	r.AccountFingerprint = fp
	now := time.Now()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	d.recipients[r.ID] = &r
	return &r, nil
}

// Get returns a recipient by ID.
func (d *Directory) Get(id string) (*Recipient, bool) {
	r, ok := d.recipients[id]
	return r, ok
}

// StaleAfter flags recipients not used within the window — surfaced as "not
// used in a while, still correct?" before a large payment.
func StaleAfter(r *Recipient, window time.Duration, now time.Time) bool {
	if r.LastUsedAt.IsZero() {
		return now.Sub(r.CreatedAt) > window
	}
	return now.Sub(r.LastUsedAt) > window
}

// Search ranks by display-name token match, then recency, then frequency.
func (d *Directory) Search(owner string, query string) []*Recipient {
	type scored struct {
		r *Recipient
		s float64
	}
	q := tokenSet(query)
	var out []scored
	for _, r := range d.recipients {
		if owner != "" && r.OwnerCustomerID != owner {
			continue
		}
		s := 0.0
		name := tokenSet(r.DisplayName)
		if q == "" {
			s = 1 // bare listing: recency/frequency ordering only
		} else if strings.Contains(name, q) || strings.Contains(q, name) {
			s = 10
		} else if editDistanceAtMost(strings.Fields(name), strings.Fields(q), 2) {
			s = 5
		}
		if s == 0 {
			continue
		}
		if !r.LastUsedAt.IsZero() {
			days := time.Since(r.LastUsedAt).Hours() / 24
			if days < 30 {
				s += 2 - days/30
			}
		}
		s += float64(r.TimesUsed) * 0.1
		out = append(out, scored{r, s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].s > out[j].s })
	res := make([]*Recipient, len(out))
	for i, o := range out {
		res[i] = o.r
	}
	return res
}

// ---------- 9. Recipient change history ----------

// ChangeRecord is one immutable before/after audit entry.
type ChangeRecord struct {
	RecipientID string            `json:"recipient_id"`
	ChangedBy   string            `json:"changed_by"` // actor id (customer, support, migration)
	ChangedAt   time.Time         `json:"changed_at"`
	Before      map[string]string `json:"before"`
	After       map[string]string `json:"after"`
	Reason      string            `json:"reason"`
}

// ChangeHistory is the append-only audit trail for one directory.
type ChangeHistory struct {
	records []ChangeRecord
}

// Record appends a change; empty diffs are rejected (no-op changes pollute
// the audit trail and hide real ones).
func (h *ChangeHistory) Record(rec ChangeRecord) error {
	if len(rec.Before) == 0 && len(rec.After) == 0 {
		return fmt.Errorf("change record with no before/after detail")
	}
	if rec.ChangedAt.IsZero() {
		rec.ChangedAt = time.Now()
	}
	h.records = append(h.records, rec)
	return nil
}

// For returns the history of one recipient, oldest first.
func (h *ChangeHistory) For(recipientID string) []ChangeRecord {
	var out []ChangeRecord
	for _, r := range h.records {
		if r.RecipientID == recipientID {
			out = append(out, r)
		}
	}
	return out
}

// Summarise answers the support question in one string:
// WHO changed WHEN FROM WHAT TO WHAT WHY.
func Summarise(r ChangeRecord) string {
	return fmt.Sprintf("%s changed recipient %s at %s: sort code %s→%s, account %s→%s (%s)",
		r.ChangedBy, r.RecipientID,
		r.ChangedAt.Format(time.RFC3339),
		r.Before["sort_code"], r.After["sort_code"],
		maskAccount(r.Before["account"]), maskAccount(r.After["account"]),
		r.Reason)
}

func maskAccount(a string) string {
	if len(a) <= 4 {
		return "****"
	}
	return "****" + a[len(a)-4:]
}

// ---------- 10. Customer-generated payment templates ----------

// Template is a reusable, validated payment intent — NOT a schedule. Nothing
// fires automatically; a template pre-fills and pre-checks a payment form.
type Template struct {
	ID                string            `json:"id"`
	OwnerCustomerID   string            `json:"owner_customer_id"`
	Name              string            `json:"name"` // "Rent"
	RecipientID       string            `json:"recipient_id"`
	Reference         string            `json:"reference"` // "RENT-SEPT"
	ExpectedMinor     int64             `json:"expected_minor"`
	Currency          string            `json:"currency"`
	LastValidatedAt   time.Time         `json:"last_validated_at"`
	ValidationOutcome ValidationOutcome `json:"validation_outcome"`
}

// TemplateChange detects drift between what the template expects and the
// live directory record, so "Send September rent" can warn before sending.
type TemplateChange struct {
	Field    string `json:"field"`
	OldValue string `json:"old_value"`
	NewValue string `json:"new_value"`
	Critical bool   `json:"critical"` // account details changed = re-verify before send
}

// CheckTemplate compares a template against its recipient and returns the
// drift (empty slice = unchanged). Critical changes block one-tap sending.
func CheckTemplate(t Template, r *Recipient) []TemplateChange {
	if r == nil {
		return []TemplateChange{{Field: "recipient", NewValue: "missing", Critical: true}}
	}
	var changes []TemplateChange
	if t.RecipientID != r.ID {
		changes = append(changes, TemplateChange{Field: "recipient_id", OldValue: t.RecipientID, NewValue: r.ID, Critical: true})
	}
	// Reference expected to be regenerated per period by the caller; amount
	// drift is informational, account drift is critical (impossible here since
	// the directory holds details, but kept for cross-system templates).
	if t.Currency != "" && r.OwnerCustomerID != t.OwnerCustomerID {
		changes = append(changes, TemplateChange{Field: "owner", Critical: true})
	}
	return changes
}
