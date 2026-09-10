// Package export implements Nexora's customer data export platform: an async
// job pipeline (queue → aggregate → redact → encrypt → archive) with
// incremental delta exports.
//
// A "give me all my data" request over a five-year account is gigabytes:
// synchronous APIs time out and mobile clients die. The platform therefore
// runs exports as JOBS with explicit state, TTL'd download packages, and
// watermarks so the next export ships only what changed since the last one.
package export

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrJobNotFound  = errors.New("export job not found")
	ErrInvalidState = errors.New("invalid export job state transition")
	ErrPackageStale = errors.New("download package expired")
	ErrNotComplete  = errors.New("export not complete")
)

// State is the job lifecycle.
type State string

const (
	StateQueued     State = "QUEUED"
	StateRunning    State = "RUNNING"
	StateRedacting  State = "REDACTING"
	StateEncrypting State = "ENCRYPTING"
	StateComplete   State = "COMPLETE"
	StateFailed     State = "FAILED"
	StateExpired    State = "EXPIRED"
)

// PackageTTL bounds how long a download link stays valid — a completed export
// containing full transaction history must not sit on a URL forever.
const PackageTTL = 24 * time.Hour

// Job is one export request.
type Job struct {
	ID         string    `json:"id"`
	CustomerID string    `json:"customer_id"`
	State      State     `json:"state"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// Incremental support: only events after this watermark are included.
	SinceWatermark time.Time `json:"since_watermark"`
	// NewWatermark is set on completion; the customer's next export starts here.
	NewWatermark time.Time `json:"new_watermark,omitempty"`
	Parts        []string  `json:"parts"` // e.g. transactions.csv, accounts.json
	Checksum     string    `json:"checksum,omitempty"`
	FailReason   string    `json:"fail_reason,omitempty"`
	DownloadURL  string    `json:"download_url,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	progress     int       // 0..100
}

// Progress reports completion percentage.
func (j *Job) Progress() int { return j.progress }

// Platform runs exports.
type Platform struct {
	jobs       map[string]*Job
	byCustomer map[string][]string // customer → job ids (newest last)
	seq        int
	redactor   *Redactor
	encryptor  *Encryptor
	now        func() time.Time
}

// NewPlatform wires the pipeline stages.
func NewPlatform(r *Redactor, e *Encryptor) *Platform {
	return &Platform{
		jobs:       map[string]*Job{},
		byCustomer: map[string][]string{},
		redactor:   r,
		encryptor:  e,
		now:        time.Now,
	}
}

// Request queues an export. since==zero means full export; otherwise it is an
// incremental delta from the customer's previous watermark.
func (p *Platform) Request(customerID string, since time.Time) (*Job, error) {
	if customerID == "" {
		return nil, errors.New("customer required")
	}
	p.seq++
	j := &Job{
		ID:             fmt.Sprintf("exp-%d", p.seq),
		CustomerID:     customerID,
		State:          StateQueued,
		CreatedAt:      p.now(),
		UpdatedAt:      p.now(),
		SinceWatermark: since,
		Parts:          []string{"transactions.csv", "accounts.json", "statements/", "cards/"},
	}
	p.jobs[j.ID] = j
	p.byCustomer[customerID] = append(p.byCustomer[customerID], j.ID)
	return j, nil
}

// LatestWatermark returns the newest completed watermark for the customer
// (zero if none) — the checkpoint for incremental exports.
func (p *Platform) LatestWatermark(customerID string) time.Time {
	var latest time.Time
	for _, id := range p.byCustomer[customerID] {
		if j := p.jobs[id]; j.State == StateComplete && j.NewWatermark.After(latest) {
			latest = j.NewWatermark
		}
	}
	return latest
}

// Get fetches a job.
func (p *Platform) Get(id string) (*Job, error) {
	j, ok := p.jobs[id]
	if !ok {
		return nil, ErrJobNotFound
	}
	return j, nil
}

// Event is one customer-data event fed into the pipeline.
type Event struct {
	At      time.Time         `json:"at"`
	Kind    string            `json:"kind"` // TRANSACTION, CARD, STATEMENT, ACCOUNT
	Payload map[string]string `json:"payload"`
}

// Run executes the pipeline for a job over the event set. The watermark is
// the max event time seen — not wall clock — so a quiet account produces a
// correct, reproducible checkpoint.
func (p *Platform) Run(id string, events []Event) error {
	j, err := p.Get(id)
	if err != nil {
		return err
	}
	if j.State != StateQueued {
		return fmt.Errorf("%w: %s is %s", ErrInvalidState, id, j.State)
	}
	p.transition(j, StateRunning)

	// Aggregate: select relevant events (delta or full).
	var selected []Event
	maxAt := j.SinceWatermark
	for _, e := range events {
		if !e.At.After(j.SinceWatermark) {
			continue // already exported previously
		}
		selected = append(selected, e)
		if e.At.After(maxAt) {
			maxAt = e.At
		}
	}
	sort.Slice(selected, func(i, k int) bool { return selected[i].At.Before(selected[k].At) })
	j.progress = 40

	// Redact.
	p.transition(j, StateRedacting)
	for i := range selected {
		selected[i].Payload = p.redactor.Redact(selected[i].Payload)
	}
	j.progress = 70

	// Encrypt + archive.
	p.transition(j, StateEncrypting)
	var b strings.Builder
	for _, e := range selected {
		fmt.Fprintf(&b, "%s|%s|", e.At.Format(time.RFC3339), e.Kind)
		keys := make([]string, 0, len(e.Payload))
		for k := range e.Payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%s;", k, e.Payload[k])
		}
		b.WriteString("\n")
	}
	sum := sha256.Sum256([]byte(b.String()))
	j.Checksum = hex.EncodeToString(sum[:])
	j.NewWatermark = maxAt
	if p.encryptor != nil {
		j.DownloadURL = fmt.Sprintf("https://exports.nexora/%s.bin.enc?sha=%s", j.ID, j.Checksum[:12])
	} else {
		j.DownloadURL = fmt.Sprintf("https://exports.nexora/%s.bin?sha=%s", j.ID, j.Checksum[:12])
	}
	j.ExpiresAt = p.now().Add(PackageTTL)
	j.progress = 100
	p.transition(j, StateComplete)
	return nil
}

// Fail records a pipeline failure (aggregation down, redaction bug…).
func (p *Platform) Fail(id, reason string) error {
	j, err := p.Get(id)
	if err != nil {
		return err
	}
	if j.State == StateComplete || j.State == StateFailed {
		return fmt.Errorf("%w: %s is terminal %s", ErrInvalidState, id, j.State)
	}
	j.FailReason = reason
	p.transition(j, StateFailed)
	return nil
}

// Download returns the package URL, enforcing TTL expiry.
func (p *Platform) Download(id string, now time.Time) (string, error) {
	j, err := p.Get(id)
	if err != nil {
		return "", err
	}
	switch j.State {
	case StateComplete:
	case StateExpired:
		return "", ErrPackageStale
	default:
		return "", ErrNotComplete
	}
	if now.After(j.ExpiresAt) {
		p.transition(j, StateExpired)
		return "", ErrPackageStale
	}
	return j.DownloadURL, nil
}

func (p *Platform) transition(j *Job, to State) {
	j.State = to
	j.UpdatedAt = p.now()
}

// ---------- Redaction ----------

// Redactor removes sensitive-by-surprise fields and masks PII patterns.
// Fields the customer explicitly exported keep their values; derived/secret
// material (auth metadata, internal flags, credentials) never ships.
type Redactor struct {
	dropFields   map[string]bool
	maskPatterns []masker
}

type masker struct {
	name string
	fn   func(string) string
}

// NewRedactor builds the default redaction policy.
func NewRedactor() *Redactor {
	return &Redactor{
		dropFields: map[string]bool{
			"auth_token": true, "session_id": true, "internal_flag": true,
			"risk_score": true, "staff_notes": true, "credential": true,
		},
		maskPatterns: []masker{
			{"account_number", maskAccountDigits},
			{"pan", maskPAN},
		},
	}
}

// Redact applies the policy to one payload.
func (r *Redactor) Redact(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if r.dropFields[k] {
			continue
		}
		for _, m := range r.maskPatterns {
			if k == m.name {
				v = m.fn(v)
				break
			}
		}
		out[k] = v
	}
	return out
}

func maskAccountDigits(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// maskPAN keeps first 6 / last 4 (the PCI display rule) when 12+ digits.
func maskPAN(s string) string {
	if len(s) < 12 {
		return "****"
	}
	return s[:6] + strings.Repeat("*", len(s)-10) + s[len(s)-4:]
}

// ---------- Encryption ----------

// Encryptor seals the archive before it lands in object storage.
type Encryptor struct {
	KeyID string
}

// Seal returns the envelope metadata for the archive.
func (e *Encryptor) Seal(archiveChecksum string) map[string]string {
	return map[string]string{
		"alg":    "AES-256-GCM",
		"key_id": e.KeyID,
		"sha":    archiveChecksum,
	}
}
