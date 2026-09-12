package paycycle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BeneficiaryState is the trust lifecycle of a saved beneficiary:
//
//	CREATED -> VERIFIED -> TRUSTED -> DORMANT -+
//	                                              +-> REMOVED
//	detail change: any live state -> RISK_INCREASED -> REVERIFICATION -> VERIFIED
type BeneficiaryState string

const (
	BeneficiaryCreated        BeneficiaryState = "CREATED"
	BeneficiaryVerified       BeneficiaryState = "VERIFIED"
	BeneficiaryTrusted        BeneficiaryState = "TRUSTED"
	BeneficiaryDormant        BeneficiaryState = "DORMANT"
	BeneficiaryRiskIncreased  BeneficiaryState = "RISK_INCREASED"
	BeneficiaryReverification BeneficiaryState = "REVERIFICATION"
	BeneficiaryRemoved        BeneficiaryState = "REMOVED"
)

var (
	// ErrBeneficiaryNotFound maps to HTTP 404.
	ErrBeneficiaryNotFound = errors.New("beneficiary not found")
	// ErrBeneficiaryExists maps to HTTP 409 (identical account already saved).
	ErrBeneficiaryExists = errors.New("beneficiary already exists")
	// ErrBeneficiaryState maps to HTTP 409 (transition not allowed from here).
	ErrBeneficiaryState = errors.New("beneficiary state does not allow this transition")
	// ErrBeneficiaryCooling maps to HTTP 409 (detail change cooling active).
	ErrBeneficiaryCooling = errors.New("beneficiary is in detail-change cooling period")
)

// CoolingPeriod is how long payments are held after account details change.
const CoolingPeriod = 24 * time.Hour

// Beneficiary is one saved payee with its trust state. Raw account details
// stay on the record (services need them to pay); Fingerprint is the safe
// join key for logs and duplicate detection.
type Beneficiary struct {
	ID            string           `json:"id"`
	OwnerID       string           `json:"owner_id,omitempty"`
	Name          string           `json:"name"`
	SortCode      string           `json:"sort_code"`
	AccountNumber string           `json:"account_number"`
	Fingerprint   string           `json:"fingerprint"`
	State         BeneficiaryState `json:"state"`
	// FirstPaymentEver is true until the first successful payment completes,
	// so callers can force step-up auth on the maiden payment.
	FirstPaymentEver bool       `json:"first_payment_ever"`
	CoolingUntil     time.Time  `json:"cooling_until,omitempty"`
	Payments         int        `json:"payments"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LastPaidAt       *time.Time `json:"last_paid_at,omitempty"`
}

// InCooling reports whether a detail-change cooling period is active at now.
func (b *Beneficiary) InCooling(now time.Time) bool {
	if b == nil || b.CoolingUntil.IsZero() {
		return false
	}
	return now.Before(b.CoolingUntil)
}

// Fingerprint derives a stable, non-reversible account identifier for
// duplicate detection and change detection.
func Fingerprint(sortCode, accountNumber string) string {
	norm := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(sortCode), "-", ""), " ", "") +
		"|" + strings.TrimSpace(accountNumber)
	sum := sha256.Sum256([]byte(norm))
	return hex.EncodeToString(sum[:8])
}

// Store is the in-memory beneficiary directory. It is deliberately
// mutex-free: services own concurrency (see internal/paycycle).
type Store struct {
	items map[string]*Beneficiary
}

// NewStore creates an empty directory.
func NewStore() *Store {
	return &Store{items: map[string]*Beneficiary{}}
}

func copyOf(b *Beneficiary) *Beneficiary {
	if b == nil {
		return nil
	}
	cp := *b
	if b.LastPaidAt != nil {
		t := *b.LastPaidAt
		cp.LastPaidAt = &t
	}
	return &cp
}

// Add saves a new beneficiary in CREATED with the first-payment-ever flag
// set. An identical account (same fingerprint) in any live state is rejected.
func (s *Store) Add(ownerID, name, sortCode, accountNumber string, now time.Time) (*Beneficiary, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("beneficiary name is required")
	}
	if strings.TrimSpace(sortCode) == "" {
		return nil, fmt.Errorf("sort code is required")
	}
	if strings.TrimSpace(accountNumber) == "" {
		return nil, fmt.Errorf("account number is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	fp := Fingerprint(sortCode, accountNumber)
	for _, e := range s.items {
		if e.Fingerprint == fp && e.State != BeneficiaryRemoved {
			return nil, fmt.Errorf("identical account already saved as %q: %w", e.Name, ErrBeneficiaryExists)
		}
	}
	id := "ben-" + fp[:12]
	b := &Beneficiary{
		ID:               id,
		OwnerID:          ownerID,
		Name:             name,
		SortCode:         strings.TrimSpace(sortCode),
		AccountNumber:    strings.TrimSpace(accountNumber),
		Fingerprint:      fp,
		State:            BeneficiaryCreated,
		FirstPaymentEver: true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.items[id] = b
	return copyOf(b), nil
}

// Verify advances trust one step: CREATED->VERIFIED, RISK_INCREASED->
// REVERIFICATION, REVERIFICATION->VERIFIED (cooling cleared), DORMANT->
// VERIFIED (reactivation after dormancy requires re-verification).
func (s *Store) Verify(id string) (*Beneficiary, error) {
	b, ok := s.items[id]
	if !ok {
		return nil, ErrBeneficiaryNotFound
	}
	switch b.State {
	case BeneficiaryCreated:
		b.State = BeneficiaryVerified
	case BeneficiaryRiskIncreased:
		b.State = BeneficiaryReverification
	case BeneficiaryReverification:
		b.State = BeneficiaryVerified
		b.CoolingUntil = time.Time{}
	case BeneficiaryDormant:
		b.State = BeneficiaryVerified
	default:
		return nil, fmt.Errorf("cannot verify beneficiary in state %s: %w", b.State, ErrBeneficiaryState)
	}
	b.UpdatedAt = time.Now().UTC()
	return copyOf(b), nil
}

// MarkPaid records a successful payment. First success promotes VERIFIED->
// TRUSTED and always clears the first-payment-ever flag; DORMANT reactivates
// to TRUSTED. Payments during a cooling period or before verification are
// refused.
func (s *Store) MarkPaid(id string, now time.Time) (*Beneficiary, error) {
	b, ok := s.items[id]
	if !ok {
		return nil, ErrBeneficiaryNotFound
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if b.State == BeneficiaryRemoved {
		return nil, fmt.Errorf("cannot pay removed beneficiary: %w", ErrBeneficiaryState)
	}
	if b.InCooling(now) {
		return nil, fmt.Errorf("cooling active until %s: %w", b.CoolingUntil.Format(time.RFC3339), ErrBeneficiaryCooling)
	}
	switch b.State {
	case BeneficiaryVerified, BeneficiaryTrusted, BeneficiaryDormant:
		b.State = BeneficiaryTrusted
	case BeneficiaryCreated:
		return nil, fmt.Errorf("beneficiary must be verified before first payment: %w", ErrBeneficiaryState)
	default:
		return nil, fmt.Errorf("beneficiary must complete reverification before payment (state %s): %w", b.State, ErrBeneficiaryState)
	}
	b.Payments++
	last := now
	b.LastPaidAt = &last
	b.FirstPaymentEver = false
	b.UpdatedAt = now
	return copyOf(b), nil
}

// UpdateDetails replaces account details. When the fingerprint changes, trust
// resets to RISK_INCREASED and a cooling period starts; payments and
// verification must go through reverification first. Returns changed=false
// (no state touched) when details are identical.
func (s *Store) UpdateDetails(id, sortCode, accountNumber string, now time.Time) (bool, *Beneficiary, error) {
	b, ok := s.items[id]
	if !ok {
		return false, nil, ErrBeneficiaryNotFound
	}
	if b.State == BeneficiaryRemoved {
		return false, nil, fmt.Errorf("cannot edit removed beneficiary: %w", ErrBeneficiaryState)
	}
	if strings.TrimSpace(sortCode) == "" || strings.TrimSpace(accountNumber) == "" {
		return false, nil, fmt.Errorf("sort code and account number are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	fp := Fingerprint(sortCode, accountNumber)
	if fp == b.Fingerprint {
		return false, copyOf(b), nil
	}
	b.SortCode = strings.TrimSpace(sortCode)
	b.AccountNumber = strings.TrimSpace(accountNumber)
	b.Fingerprint = fp
	b.State = BeneficiaryRiskIncreased
	b.CoolingUntil = now.Add(CoolingPeriod)
	b.UpdatedAt = now
	return true, copyOf(b), nil
}

// TouchDormant parks a VERIFIED or TRUSTED beneficiary as DORMANT
// (long-unused payees need re-verification before large payments).
func (s *Store) TouchDormant(id string) (*Beneficiary, error) {
	b, ok := s.items[id]
	if !ok {
		return nil, ErrBeneficiaryNotFound
	}
	switch b.State {
	case BeneficiaryVerified, BeneficiaryTrusted:
		b.State = BeneficiaryDormant
	case BeneficiaryDormant:
		// idempotent: already parked
	default:
		return nil, fmt.Errorf("cannot mark beneficiary dormant from state %s: %w", b.State, ErrBeneficiaryState)
	}
	b.UpdatedAt = time.Now().UTC()
	return copyOf(b), nil
}

// Remove tombstones a beneficiary. The record is kept so audit trails and
// fingerprint history stay intact.
func (s *Store) Remove(id string) (*Beneficiary, error) {
	b, ok := s.items[id]
	if !ok {
		return nil, ErrBeneficiaryNotFound
	}
	if b.State == BeneficiaryRemoved {
		return nil, fmt.Errorf("beneficiary already removed: %w", ErrBeneficiaryState)
	}
	b.State = BeneficiaryRemoved
	b.UpdatedAt = time.Now().UTC()
	return copyOf(b), nil
}

// Status returns a copy of one beneficiary.
func (s *Store) Status(id string) (*Beneficiary, error) {
	b, ok := s.items[id]
	if !ok {
		return nil, ErrBeneficiaryNotFound
	}
	return copyOf(b), nil
}
