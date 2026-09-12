package financeops

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Entry is one balanced double-entry posting.
type Entry struct {
	ID            string    `json:"id"`
	DebitAccount  string    `json:"debit_account"`
	CreditAccount string    `json:"credit_account"`
	Amount        int64     `json:"amount"`
	Period        string    `json:"period"`
	Reference     string    `json:"reference,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// AdjustmentStatus is PENDING or APPROVED.
type AdjustmentStatus string

const (
	AdjustmentPending  AdjustmentStatus = "PENDING"
	AdjustmentApproved AdjustmentStatus = "APPROVED"
)

// Adjustment is a request to correct an immutable original entry.
type Adjustment struct {
	ID             string           `json:"id"`
	OriginalID     string           `json:"original_id"`
	Reason         string           `json:"reason"`
	Status         AdjustmentStatus `json:"status"`
	CompensatingID string           `json:"compensating_id,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
}

// CreateEntry posts an original balanced entry. Originals are never mutated
// afterwards; corrections go through the adjustment workflow.
func (s *Store) CreateEntry(debitAccount, creditAccount string, amount int64, period string) (*Entry, error) {
	if debitAccount == "" || creditAccount == "" {
		return nil, fmt.Errorf("debit_account and credit_account are required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if period == "" {
		return nil, fmt.Errorf("period is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := &Entry{
		ID:            uuid.NewString(),
		DebitAccount:  debitAccount,
		CreditAccount: creditAccount,
		Amount:        amount,
		Period:        period,
		CreatedAt:     time.Now().UTC(),
	}
	s.entries[e.ID] = e
	if _, ok := s.periods[period]; !ok {
		s.periods[period] = false
	}
	s.logger.Info().Str("entry_id", e.ID).Str("period", period).Int64("amount", amount).Msg("financeops entry posted")
	cp := *e
	return &cp, nil
}

// GetEntry returns a copy of an entry.
func (s *Store) GetEntry(id string) (*Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return nil, ErrEntryNotFound
	}
	cp := *e
	return &cp, nil
}

// RequestAdjustment opens a PENDING adjustment against an immutable original.
func (s *Store) RequestAdjustment(originalID, reason string) (*Adjustment, error) {
	if reason == "" {
		return nil, fmt.Errorf("reason is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[originalID]; !ok {
		return nil, ErrEntryNotFound
	}
	a := &Adjustment{
		ID:         uuid.NewString(),
		OriginalID: originalID,
		Reason:     reason,
		Status:     AdjustmentPending,
		CreatedAt:  time.Now().UTC(),
	}
	s.adjustments[a.ID] = a
	s.logger.Info().Str("adjustment_id", a.ID).Str("original_id", originalID).Msg("financeops adjustment requested")
	cp := *a
	return &cp, nil
}

// ApproveAdjustment posts the compensating entry (reversing legs referencing
// the original) and marks the adjustment APPROVED. The original is never
// mutated.
func (s *Store) ApproveAdjustment(id string) (*Adjustment, *Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.adjustments[id]
	if !ok {
		return nil, nil, ErrAdjustmentNotFound
	}
	if a.Status != AdjustmentPending {
		return nil, nil, ErrAdjustmentState
	}
	orig, ok := s.entries[a.OriginalID]
	if !ok {
		return nil, nil, ErrEntryNotFound
	}
	comp := &Entry{
		ID:            uuid.NewString(),
		DebitAccount:  orig.CreditAccount,
		CreditAccount: orig.DebitAccount,
		Amount:        orig.Amount,
		Period:        s.openPeriodForLocked(orig.Period),
		Reference:     orig.ID,
		CreatedAt:     time.Now().UTC(),
	}
	s.entries[comp.ID] = comp
	a.Status = AdjustmentApproved
	a.CompensatingID = comp.ID
	s.logger.Info().Str("adjustment_id", id).Str("compensating_id", comp.ID).Msg("financeops adjustment approved")
	acp := *a
	ecp := *comp
	return &acp, &ecp, nil
}

// GetAdjustment returns a copy of an adjustment.
func (s *Store) GetAdjustment(id string) (*Adjustment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.adjustments[id]
	if !ok {
		return nil, ErrAdjustmentNotFound
	}
	cp := *a
	return &cp, nil
}

// VerifyBalanced checks that the compensating entry reverses the original
// legs for the same amount (net zero), i.e. the pair is balanced.
func (s *Store) VerifyBalanced(adjustmentID string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.adjustments[adjustmentID]
	if !ok {
		return false, "", ErrAdjustmentNotFound
	}
	if a.Status != AdjustmentApproved || a.CompensatingID == "" {
		return false, "adjustment not yet approved", nil
	}
	orig, ok := s.entries[a.OriginalID]
	if !ok {
		return false, "", ErrEntryNotFound
	}
	comp, ok := s.entries[a.CompensatingID]
	if !ok {
		return false, "", ErrEntryNotFound
	}
	if comp.DebitAccount != orig.CreditAccount || comp.CreditAccount != orig.DebitAccount {
		return false, "compensating legs do not reverse the original", nil
	}
	if comp.Amount != orig.Amount {
		return false, "compensating amount differs from original", nil
	}
	if comp.Reference != orig.ID {
		return false, "compensating entry must reference the original", nil
	}
	return true, "balanced: compensating entry reverses the original", nil
}
