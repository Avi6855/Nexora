package financeops

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Isolated sub-ledger domains.
var SubledgerDomains = []string{"card", "loans", "savings", "investments", "fees"}

func validDomain(domain string) bool {
	for _, d := range SubledgerDomains {
		if d == domain {
			return true
		}
	}
	return false
}

// SubledgerEntry is one posting inside an isolated domain ledger.
type SubledgerEntry struct {
	ID            string    `json:"id"`
	Domain        string    `json:"domain"`
	DebitAccount  string    `json:"debit_account"`
	CreditAccount string    `json:"credit_account"`
	Amount        int64     `json:"amount"`
	CreatedAt     time.Time `json:"created_at"`
}

// TrialBalance is the per-domain trial: total debits vs credits.
type TrialBalance struct {
	Domain   string `json:"domain"`
	Debits   int64  `json:"debits"`
	Credits  int64  `json:"credits"`
	Balanced bool   `json:"balanced"`
	Count    int    `json:"count"`
}

// ConsolidatedView is the controlled general view across domains.
type ConsolidatedView struct {
	Debits    int64                   `json:"debits"`
	Credits   int64                   `json:"credits"`
	Balanced  bool                    `json:"balanced"`
	PerDomain map[string]TrialBalance `json:"per_domain"`
}

// PostSubledger appends a balanced posting to an isolated domain ledger.
func (s *Store) PostSubledger(domain, debitAccount, creditAccount string, amount int64) (*SubledgerEntry, error) {
	if !validDomain(domain) {
		return nil, fmt.Errorf("unknown domain %q: want one of card, loans, savings, investments, fees", domain)
	}
	if debitAccount == "" || creditAccount == "" {
		return nil, fmt.Errorf("debit_account and credit_account are required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := SubledgerEntry{
		ID:            uuid.NewString(),
		Domain:        domain,
		DebitAccount:  debitAccount,
		CreditAccount: creditAccount,
		Amount:        amount,
		CreatedAt:     time.Now().UTC(),
	}
	s.subledgers[domain] = append(s.subledgers[domain], e)
	s.logger.Info().Str("domain", domain).Int64("amount", amount).Msg("financeops subledger posted")
	cp := e
	return &cp, nil
}

// TrialBalance returns the trial for one domain.
func (s *Store) TrialBalance(domain string) (TrialBalance, error) {
	if !validDomain(domain) {
		return TrialBalance{}, fmt.Errorf("unknown domain %q", domain)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var debits, credits int64
	entries := s.subledgers[domain]
	for _, e := range entries {
		debits += e.Amount
		credits += e.Amount
	}
	return TrialBalance{Domain: domain, Debits: debits, Credits: credits, Balanced: debits == credits, Count: len(entries)}, nil
}

// Consolidated folds every domain trial into the general view.
func (s *Store) Consolidated() ConsolidatedView {
	s.mu.Lock()
	defer s.mu.Unlock()
	view := ConsolidatedView{PerDomain: make(map[string]TrialBalance, len(SubledgerDomains))}
	for _, domain := range SubledgerDomains {
		var debits, credits int64
		entries := s.subledgers[domain]
		for _, e := range entries {
			debits += e.Amount
			credits += e.Amount
		}
		tb := TrialBalance{Domain: domain, Debits: debits, Credits: credits, Balanced: debits == credits, Count: len(entries)}
		view.PerDomain[domain] = tb
		view.Debits += debits
		view.Credits += credits
	}
	view.Balanced = view.Debits == view.Credits
	// Keep output deterministic for tests: domains are fixed above.
	_ = sort.StringSlice(SubledgerDomains)
	return view
}
