package financeops

import (
	"fmt"
)

// PostingRule maps a transaction type to its debit/credit legs.
type PostingRule struct {
	Type          string `json:"type"`
	DebitAccount  string `json:"debit_account"`
	CreditAccount string `json:"credit_account"`
	Version       int    `json:"version"`
}

// PutRule stores a new version of a posting rule, bumping the version.
func (s *Store) PutRule(txType, debitAccount, creditAccount string) (*PostingRule, error) {
	if txType == "" {
		return nil, fmt.Errorf("type is required")
	}
	if debitAccount == "" || creditAccount == "" {
		return nil, fmt.Errorf("debit_account and credit_account are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.rules[txType]
	rule := PostingRule{
		Type:          txType,
		DebitAccount:  debitAccount,
		CreditAccount: creditAccount,
		Version:       len(history) + 1,
	}
	s.rules[txType] = append(history, rule)
	s.logger.Info().Str("type", txType).Int("version", rule.Version).Msg("financeops posting rule stored")
	cp := rule
	return &cp, nil
}

// Rollback drops the latest version of a rule, restoring the prior one.
func (s *Store) Rollback(txType string) (*PostingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, ok := s.rules[txType]
	if !ok || len(history) == 0 {
		return nil, ErrRuleNotFound
	}
	if len(history) == 1 {
		return nil, ErrNoPriorVersion
	}
	s.rules[txType] = history[:len(history)-1]
	restored := s.rules[txType][len(s.rules[txType])-1]
	s.logger.Info().Str("type", txType).Int("version", restored.Version).Msg("financeops posting rule rolled back")
	cp := restored
	return &cp, nil
}

// Resolve returns a rule version. Version 0 (or negative) resolves the
// latest; otherwise the exact version is returned.
func (s *Store) Resolve(txType string, version int) (*PostingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, ok := s.rules[txType]
	if !ok || len(history) == 0 {
		return nil, ErrRuleNotFound
	}
	if version <= 0 {
		latest := history[len(history)-1]
		cp := latest
		return &cp, nil
	}
	for _, r := range history {
		if r.Version == version {
			cp := r
			return &cp, nil
		}
	}
	return nil, fmt.Errorf("type %q version %d: %w", txType, version, ErrRuleVersionNotFound)
}

// RuleHistory returns every version of a rule in order.
func (s *Store) RuleHistory(txType string) ([]PostingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history, ok := s.rules[txType]
	if !ok || len(history) == 0 {
		return nil, ErrRuleNotFound
	}
	return append([]PostingRule(nil), history...), nil
}
