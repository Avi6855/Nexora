// Package statements implements Nexora's statement integrity platform:
// versioned statement generation (v1, v2, v3 with full provenance) and
// tamper-evident verification so any third party can prove a statement PDF/CSV
// came from us unaltered.
//
// Two rules drive the design:
//
//  1. A generated statement is an ARCHIVE, not a view. Corrections produce a
//     new version with a new hash; the old version is never mutated. An
//     auditor must be able to diff v1→v3 and see exactly what changed, when,
//     and why.
//
//  2. Tamper-evidence is over the CANONICAL representation, not the bytes of
//     a PDF. PDFs differ across renderers; the canonical line format hashes
//     identically everywhere.
package statements

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrVersionImmutable = errors.New("statement versions are immutable")
	ErrUnknownVersion   = errors.New("unknown statement version")
	ErrNotAuthorized    = errors.New("signing key not configured")
)

// Line is one canonical statement line.
type Line struct {
	Date         time.Time `json:"date"`
	Description  string    `json:"description"`
	AmountMinor  int64     `json:"amount_minor"`
	Currency     string    `json:"currency"`
	BalanceMinor int64     `json:"balance_after_minor"`
	Ref          string    `json:"ref"`
}

// Canonical renders the line in the stable hash format. Field order, number
// formatting and date layout are part of the hash contract — changing them
// invalidates every historical statement, so this format is versioned.
func (l Line) Canonical() string {
	return fmt.Sprintf("%s|%s|%d|%s|%d|%s",
		l.Date.UTC().Format(time.RFC3339),
		strings.ReplaceAll(l.Description, "|", "/"),
		l.AmountMinor, l.Currency, l.BalanceMinor, l.Ref)
}

// CanonicalLines renders the full ordered line set.
func CanonicalLines(lines []Line) string {
	sorted := make([]Line, len(lines))
	copy(sorted, lines)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date.Equal(sorted[j].Date) {
			return sorted[i].Ref < sorted[j].Ref
		}
		return sorted[i].Date.Before(sorted[j].Date)
	})
	var b strings.Builder
	for _, l := range sorted {
		b.WriteString(l.Canonical())
		b.WriteString("\n")
	}
	return b.String()
}

// Version is one immutable statement revision.
type Version struct {
	Number      int       `json:"number"`
	GeneratedAt time.Time `json:"generated_at"`
	DataCutoff  time.Time `json:"data_cutoff"` // transactions included up to this instant
	Corrections []string  `json:"corrections"` // human-readable change notes vs prior version
	Lines       []Line    `json:"lines"`
	Hash        string    `json:"hash"`      // sha256 of canonical lines
	Signature   string    `json:"signature"` // HMAC-SHA256 over hash with platform key
	PrevHash    string    `json:"prev_hash"` // chains to prior version (tamper-evident chain)
}

// Statement is the version set for one account+period.
type Statement struct {
	AccountID string    `json:"account_id"`
	Period    string    `json:"period"` // "2026-08"
	Versions  []Version `json:"versions"`
}

// Generate produces v1 from the cutoff data.
func Generate(accountID, period string, cutoff time.Time, lines []Line) (*Statement, error) {
	if accountID == "" || period == "" {
		return nil, errors.New("account and period required")
	}
	if len(lines) == 0 {
		return nil, errors.New("statement with no lines")
	}
	v := Version{Number: 1, GeneratedAt: time.Now(), DataCutoff: cutoff, Lines: lines}
	v.Hash = hashCanonical(v.Lines)
	return &Statement{AccountID: accountID, Period: period, Versions: []Version{v}}, nil
}

// Revise appends a new version. The prior version is checked for immutability
// (hash re-verified) and chained via PrevHash.
func (s *Statement) Revise(cutoff time.Time, lines []Line, corrections []string) (*Version, error) {
	if len(s.Versions) == 0 {
		return nil, ErrUnknownVersion
	}
	prev := s.Versions[len(s.Versions)-1]
	// Re-verify the stored prior version first: if someone tampered with the
	// archive, refuse to build on it.
	if hashCanonical(prev.Lines) != prev.Hash {
		return nil, fmt.Errorf("%w: prior version hash mismatch", ErrVersionImmutable)
	}
	if len(corrections) == 0 {
		return nil, errors.New("a revision must declare what changed")
	}
	v := Version{
		Number:      prev.Number + 1,
		GeneratedAt: time.Now(),
		DataCutoff:  cutoff,
		Corrections: corrections,
		Lines:       lines,
		PrevHash:    prev.Hash,
	}
	v.Hash = hashCanonical(v.Lines)
	s.Versions = append(s.Versions, v)
	return &v, nil
}

// Diff describes line-level changes between two versions.
type Diff struct {
	Added     []Line `json:"added"`
	Removed   []Line `json:"removed"`
	Corrected []struct {
		Before Line `json:"before"`
		After  Line `json:"after"`
	} `json:"corrected"`
}

// DiffVersions answers "what changed between these two statements?".
func (s *Statement) DiffVersions(from, to int) (*Diff, error) {
	var a, b *Version
	for i := range s.Versions {
		switch s.Versions[i].Number {
		case from:
			a = &s.Versions[i]
		case to:
			b = &s.Versions[i]
		}
	}
	if a == nil || b == nil {
		return nil, ErrUnknownVersion
	}
	key := func(l Line) string { return l.Ref + "|" + l.Date.Format(time.DateOnly) }
	bm := map[string]Line{}
	for _, l := range b.Lines {
		bm[key(l)] = l
	}
	am := map[string]Line{}
	for _, l := range a.Lines {
		am[key(l)] = l
	}
	d := &Diff{}
	for k, l := range bm {
		if _, ok := am[k]; !ok {
			d.Added = append(d.Added, l)
		}
	}
	for k, l := range am {
		if nb, ok := bm[k]; !ok {
			d.Removed = append(d.Removed, l)
		} else if nb != l {
			type correctedPair = struct {
				Before Line `json:"before"`
				After  Line `json:"after"`
			}
			d.Corrected = append(d.Corrected, correctedPair{Before: l, After: nb})
		}
	}
	sort.Slice(d.Added, func(i, j int) bool { return d.Added[i].Ref < d.Added[j].Ref })
	sort.Slice(d.Removed, func(i, j int) bool { return d.Removed[i].Ref < d.Removed[j].Ref })
	sort.Slice(d.Corrected, func(i, j int) bool { return d.Corrected[i].Before.Ref < d.Corrected[j].Before.Ref })
	return d, nil
}

// ---------- Tamper-evident verification ----------

// Verifier signs and verifies canonical statement hashes with an HMAC key.
// HMAC (not bare SHA) means a tampered statement cannot be re-signed by the
// attacker without the platform key.
type Verifier struct {
	key []byte
}

// NewVerifier builds a verifier from the platform signing key.
func NewVerifier(key []byte) *Verifier { return &Verifier{key: key} }

// Sign stamps a version's signature.
func (v *Verifier) Sign(ver *Version) error {
	if v == nil || len(v.key) == 0 {
		return ErrNotAuthorized
	}
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte(ver.Hash))
	ver.Signature = hex.EncodeToString(mac.Sum(nil))
	return nil
}

// VerifyResult is the external verification verdict.
type VerifyResult string

const (
	VerifyValid   VerifyResult = "VALID"
	VerifyAltered VerifyResult = "ALTERED"
	VerifyUnknown VerifyResult = "UNKNOWN"
)

// Verify checks a statement presented for verification: canonical hash
// recomputation, signature check, and version-chain integrity.
func (v *Verifier) Verify(s Statement) (VerifyResult, string) {
	if v == nil || len(v.key) == 0 {
		return VerifyUnknown, "verifier has no key"
	}
	prevHash := ""
	for i := range s.Versions {
		ver := &s.Versions[i]
		if hashCanonical(ver.Lines) != ver.Hash {
			return VerifyAltered, fmt.Sprintf("version %d: content hash mismatch", ver.Number)
		}
		mac := hmac.New(sha256.New, v.key)
		mac.Write([]byte(ver.Hash))
		if !hmac.Equal([]byte(ver.Signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			return VerifyAltered, fmt.Sprintf("version %d: signature invalid", ver.Number)
		}
		if i > 0 && ver.PrevHash != prevHash {
			return VerifyAltered, fmt.Sprintf("version %d: chain broken", ver.Number)
		}
		prevHash = ver.Hash
	}
	return VerifyValid, "all versions verified"
}

// VerifyExport is the entry point for externally-presented statements
// (re-uploaded PDFs' canonical data): same verdict contract, single version.
func (v *Verifier) VerifyExport(accountID, period string, lines []Line, hash, signature string) (VerifyResult, string) {
	if hashCanonical(lines) != hash {
		return VerifyAltered, "content hash mismatch"
	}
	mac := hmac.New(sha256.New, v.key)
	mac.Write([]byte(hash))
	if !hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return VerifyAltered, "signature invalid"
	}
	return VerifyValid, "verified"
}

func hashCanonical(lines []Line) string {
	sum := sha256.Sum256([]byte(CanonicalLines(lines)))
	return hex.EncodeToString(sum[:])
}
