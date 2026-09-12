package moneymove

import (
	"strings"
	"time"
)

// Predicate names returned by the precondition engine.
const (
	PredicateBalanceSufficient = "balance-sufficient"
	PredicateAccountActive     = "account-active"
	PredicateRecipientValid    = "recipient-valid"
	PredicateNoLegalBlock      = "no-legal-block"
	PredicateCurrencySupported = "currency-supported"
	PredicateBeforeCutoff      = "before-cutoff"
)

// PreconditionPayment is the proposed payment under review.
type PreconditionPayment struct {
	Amount    int64  `json:"amount"`
	AccountID string `json:"account_id"`
	Recipient string `json:"recipient"`
	Currency  string `json:"currency"`
}

// PreconditionState is the world the payment is checked against.
type PreconditionState struct {
	Balance             int64    `json:"balance"`
	AccountActive       bool     `json:"account_active"`
	RecipientValid      bool     `json:"recipient_valid"`
	LegalBlocked        bool     `json:"legal_blocked"`
	SupportedCurrencies []string `json:"supported_currencies,omitempty"`
	Cutoff              string   `json:"cutoff,omitempty"` // RFC3339; empty disables the check
}

// EvaluationResult is PASS when every predicate holds, else FAIL with the
// failed predicate names.
type EvaluationResult struct {
	Verdict string   `json:"verdict"` // PASS or FAIL
	Passed  bool     `json:"passed"`
	Failed  []string `json:"failed,omitempty"`
}

// Evaluate checks P(payment,state,time). It is pure: it never executes, moves
// no funds and mutates no state, so callers must refuse execution on FAIL.
func (s *Store) Evaluate(payment PreconditionPayment, state PreconditionState, now time.Time) EvaluationResult {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var failed []string
	if payment.Amount <= 0 || state.Balance < payment.Amount {
		failed = append(failed, PredicateBalanceSufficient)
	}
	if !state.AccountActive {
		failed = append(failed, PredicateAccountActive)
	}
	if !state.RecipientValid || strings.TrimSpace(payment.Recipient) == "" {
		failed = append(failed, PredicateRecipientValid)
	}
	if state.LegalBlocked {
		failed = append(failed, PredicateNoLegalBlock)
	}
	if !currencySupported(payment.Currency, state.SupportedCurrencies) {
		failed = append(failed, PredicateCurrencySupported)
	}
	if !beforeCutoff(now, state.Cutoff) {
		failed = append(failed, PredicateBeforeCutoff)
	}
	if len(failed) == 0 {
		return EvaluationResult{Verdict: "PASS", Passed: true}
	}
	return EvaluationResult{Verdict: "FAIL", Passed: false, Failed: failed}
}

func currencySupported(currency string, supported []string) bool {
	if currency == "" {
		return false
	}
	if len(supported) == 0 {
		supported = []string{"GBP", "USD", "EUR"}
	}
	for _, c := range supported {
		if strings.EqualFold(c, currency) {
			return true
		}
	}
	return false
}

func beforeCutoff(now time.Time, cutoffRaw string) bool {
	if cutoffRaw == "" {
		return true
	}
	cutoff, err := time.Parse(time.RFC3339, cutoffRaw)
	if err != nil {
		return false
	}
	return !now.After(cutoff)
}
