// Package ledgerguard wires the shared ledger invariant monitor +
// reservation engine into ledger-service as a live HTTP surface.
package ledgerguard

import (
	"time"

	"github.com/nexora/nexora/shared/ledgerguard"
)

// Service is the ledger-guard live state: one monitor plus one reservation
// engine. It is in-memory by design — the guard is a safety overlay, not the
// ledger of record.
type Service struct {
	monitor *ledgerguard.Monitor
	engine  *ledgerguard.Engine
}

// NewService returns an empty guard service.
func NewService() *Service {
	return &Service{
		monitor: ledgerguard.NewMonitor(),
		engine:  ledgerguard.NewEngine(),
	}
}

// Post validates + appends a journal posting or records a violation.
func (s *Service) Post(journalID string, entries []ledgerguard.Entry) (*ledgerguard.CheckResult, error) {
	return s.monitor.Post(journalID, entries)
}

// Freeze locks a journal.
func (s *Service) Freeze(journalID string) error {
	return s.monitor.Freeze(journalID)
}

// Unfreeze re-opens a journal.
func (s *Service) Unfreeze(journalID string) error {
	return s.monitor.Unfreeze(journalID)
}

// GetJournal returns a journal copy.
func (s *Service) GetJournal(journalID string) (*ledgerguard.Journal, error) {
	return s.monitor.GetJournal(journalID)
}

// Incidents lists filed incidents in order.
func (s *Service) Incidents() []ledgerguard.Incident {
	return s.monitor.Incidents()
}

// ClearIncident closes an incident (lifting the freeze when no open
// incidents remain for its journal).
func (s *Service) ClearIncident(incidentID string) error {
	return s.monitor.ClearIncident(incidentID)
}

// Fund sets the engine ledger balance for an account (operator/test setup).
func (s *Service) Fund(account string, balance int64) {
	s.engine.SetBalance(account, balance)
}

// Authorize places a reservation hold.
func (s *Service) Authorize(id, account string, amount int64, expiresAt time.Time) (*ledgerguard.Reservation, error) {
	return s.engine.Authorize(id, account, amount, expiresAt)
}

// TopUp incrementally authorises more funds.
func (s *Service) TopUp(id string, additional int64) (*ledgerguard.Reservation, error) {
	return s.engine.TopUp(id, additional)
}

// Capture settles part or all of a hold.
func (s *Service) Capture(id string, amount int64) (*ledgerguard.Reservation, error) {
	return s.engine.Capture(id, amount)
}

// Reverse releases the remaining hold.
func (s *Service) Reverse(id string) (*ledgerguard.Reservation, error) {
	return s.engine.Reverse(id)
}

// SweepExpired expires holds past their TTL.
func (s *Service) SweepExpired(now time.Time) int {
	return s.engine.SweepExpired(now)
}

// SettleLate settles an expired hold once, flagging it for review.
func (s *Service) SettleLate(id string, amount int64) (*ledgerguard.Reservation, error) {
	return s.engine.SettleLate(id, amount)
}

// Balances reports ledger/reserved/available for an account.
func (s *Service) Balances(account string) ledgerguard.Balance {
	return s.engine.Balances(account)
}

// GetReservation returns one reservation.
func (s *Service) GetReservation(id string) (*ledgerguard.Reservation, error) {
	return s.engine.Get(id)
}
