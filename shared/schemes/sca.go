package schemes

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"
)

// ── PSD2 strong customer authentication (SCA) ───────────────────────────────
//
// This file is the scheme-side half of SCA / EMV 3-D Secure. It owns the two
// pieces an issuer must not re-invent per service:
//
//  1. The exemption policy — when a presentment may rely on a PSD2 exemption
//     (card-present CVM, low value, transaction-risk analysis, merchant-
//     initiated, trusted beneficiary) instead of stepping the cardholder up.
//     The policy is a pure function of the presentment context, so it is
//     exhaustively testable and identical on every decision path.
//  2. The challenge lifecycle — the one-time-code state machine behind a
//     step-up. A code is minted from a cryptographic source, stored only as a
//     digest, compared in constant time, bounded by a fixed number of attempts
//     and pinned to an expiry, and a settled challenge can never be re-opened.

var (
	// ErrChallengeNotPending rejects any attempt to answer a challenge that has
	// already been completed, failed or expired (single use).
	ErrChallengeNotPending = errors.New("sca challenge is not pending")
	// ErrChallengeExpired means the cardholder took too long to answer.
	ErrChallengeExpired = errors.New("sca challenge has expired")
	// ErrChallengeNotExpired rejects an expiry sweep that reached a challenge
	// whose deadline has not passed yet.
	ErrChallengeNotExpired = errors.New("sca challenge has not reached its expiry")
	// ErrOTPInvalid is a wrong code with attempts still remaining.
	ErrOTPInvalid = errors.New("sca one-time code does not match")
	// ErrOTPAttemptsExhausted is a wrong code on the last allowed attempt.
	ErrOTPAttemptsExhausted = errors.New("sca one-time code attempts exhausted")
)

// SCAExemption names the PSD2 exemption a presentment relies on. The empty
// value means no exemption applies and the presentment must step up.
type SCAExemption string

const (
	SCAExemptionNone               SCAExemption = ""
	SCAExemptionIssuerDisabled     SCAExemption = "issuer_policy_disabled"
	SCAExemptionNotRequired        SCAExemption = "issuer_not_required"
	SCAExemptionCardPresent        SCAExemption = "card_present_cvm"
	SCAExemptionLowValue           SCAExemption = "low_value"
	SCAExemptionTransactionRisk    SCAExemption = "transaction_risk_analysis"
	SCAExemptionMerchantInitiated  SCAExemption = "merchant_initiated"
	SCAExemptionTrustedBeneficiary SCAExemption = "trusted_beneficiary"
	SCAExemptionWhitelisted        SCAExemption = "cardholder_whitelisted"
)

// SCAContext is everything the exemption policy is allowed to look at. It is
// deliberately data, not behaviour: the caller resolves terminal entry mode,
// stored-credential status and the fraud score before asking the policy.
type SCAContext struct {
	AmountMinor      int64  `json:"amount_minor"`
	Currency         string `json:"currency"`
	MerchantCategory string `json:"merchant_category"`
	MerchantCountry  string `json:"merchant_country"`
	// CardPresent is true for a terminal presentment (chip & PIN, contactless
	// under the CVM limit). False means the PAN was keyed or stored —
	// e-commerce / MOTO — which is what PSD2 requires step-up for.
	CardPresent bool `json:"card_present"`
	// RiskScore is the fraud engine's score for this presentment. It is the
	// input to the transaction-risk-analysis exemption, so the exemption is
	// granted on a real model output rather than a static amount check.
	RiskScore float64 `json:"risk_score"`
	// MerchantInitiated marks a follow-on merchant transaction after an
	// authenticated initial payment (recurring billing, no-show fee).
	MerchantInitiated bool `json:"merchant_initiated"`
	// TrustedBeneficiary / Whitelisted mark the cardholder's own approved
	// merchants (PSD2 Art. 13 / Art. 14).
	TrustedBeneficiary bool `json:"trusted_beneficiary"`
	Whitelisted        bool `json:"whitelisted"`
}

// SCAPolicy is the issuer's SCA configuration, expressed in the card's minor
// currency unit.
//
// Honest limit: PSD2's low-value exemption also carries cumulative limits (max
// five low-value presentments or €100 since the last SCA). Those counters need
// per-account state and are not modelled here — see ADR-037.
type SCAPolicy struct {
	Enabled bool `json:"enabled"`
	// CardPresentExempt accepts chip & PIN / contactless CVM as
	// authentication, so a terminal presentment never steps up.
	CardPresentExempt bool `json:"card_present_exempt"`
	// RequireForCardNotPresent is the core PSD2 rule: a card-not-present
	// presentment must step up unless an exemption applies.
	RequireForCardNotPresent bool `json:"require_for_card_not_present"`
	// LowValueExemptionMinor is the PSD2 Art. 16 low-value ceiling
	// (€30 equivalent).
	LowValueExemptionMinor int64 `json:"low_value_exemption_minor"`
	// TransactionRiskCapMinor / TransactionRiskMaxScore are the PSD2 Art. 18
	// transaction-risk-analysis window: presentments up to the cap whose fraud
	// score stays under the threshold may skip step-up.
	TransactionRiskCapMinor int64   `json:"transaction_risk_cap_minor"`
	TransactionRiskMaxScore float64 `json:"transaction_risk_max_score"`
	// ExemptMerchantCategories are the MCCs eligible for the
	// transaction-risk-analysis exemption (transport, fuel, parking, tolls).
	ExemptMerchantCategories []string `json:"exempt_merchant_categories"`
}

// DefaultSCAPolicy is the policy the card platform ships with: step-up for
// card-not-present presentments above the low-value ceiling unless the fraud
// score is low and the merchant category is a low-risk one.
func DefaultSCAPolicy() SCAPolicy {
	return SCAPolicy{
		Enabled:                  true,
		CardPresentExempt:        true,
		RequireForCardNotPresent: true,
		LowValueExemptionMinor:   3_000,  // £30 equivalent of the PSD2 €30 window
		TransactionRiskCapMinor:  25_000, // £250
		TransactionRiskMaxScore:  25,     // low-risk band of the fraud engine
		ExemptMerchantCategories: []string{
			"4111", // local/suburban commuter transport
			"4121", // taxicabs and limousines
			"4131", // bus lines
			"5541", // service stations (fuel)
			"5542", // automated fuel dispensers
			"7523", // parking lots and garages
			"4784", // tolls and bridge fees
		},
	}
}

// RequiresStepUp reports whether the presentment must go through a 3-D Secure
// challenge under this policy.
func (p SCAPolicy) RequiresStepUp(c SCAContext) bool {
	return p.Evaluate(c) == SCAExemptionNone
}

// Evaluate returns the exemption the presentment may rely on, or
// SCAExemptionNone when the cardholder must authenticate.
func (p SCAPolicy) Evaluate(c SCAContext) SCAExemption {
	if !p.Enabled {
		return SCAExemptionIssuerDisabled
	}
	// Explicit stored-credential and cardholder-approved cases never step up:
	// either the initial payment was already authenticated (merchant
	// initiated, trusted beneficiary) or the cardholder authorised the
	// merchant themselves (whitelisting).
	if c.MerchantInitiated {
		return SCAExemptionMerchantInitiated
	}
	if c.TrustedBeneficiary {
		return SCAExemptionTrustedBeneficiary
	}
	if c.Whitelisted {
		return SCAExemptionWhitelisted
	}
	if c.CardPresent && p.CardPresentExempt {
		return SCAExemptionCardPresent
	}
	// The issuer can switch the card-not-present requirement off entirely
	// (non-EEA card, or a portfolio where SCA is delegated downstream).
	if !c.CardPresent && !p.RequireForCardNotPresent {
		return SCAExemptionNotRequired
	}
	// PSD2 Art. 16: at or below the low-value ceiling.
	if p.LowValueExemptionMinor > 0 && c.AmountMinor > 0 && c.AmountMinor <= p.LowValueExemptionMinor {
		return SCAExemptionLowValue
	}
	// PSD2 Art. 18: above the low-value ceiling the issuer may still exempt a
	// presentment when the transaction-risk analysis says it is low risk and
	// the merchant category is a known low-risk one.
	if p.TransactionRiskCapMinor > 0 && c.AmountMinor <= p.TransactionRiskCapMinor &&
		c.RiskScore < p.TransactionRiskMaxScore && p.categoryExempt(c.MerchantCategory) {
		return SCAExemptionTransactionRisk
	}
	return SCAExemptionNone
}

func (p SCAPolicy) categoryExempt(mcc string) bool {
	mcc = strings.TrimSpace(mcc)
	for _, c := range p.ExemptMerchantCategories {
		if strings.EqualFold(strings.TrimSpace(c), mcc) {
			return true
		}
	}
	return false
}

// ── One-time-code challenge lifecycle ───────────────────────────────────────

const (
	// OTPDigits is the length of a step-up one-time code.
	OTPDigits = 6
	// OTPTTL is how long a challenge stays answerable before it expires. PSD2
	// requires the authentication to stay bound to the amount and payee and to
	// be time-limited.
	OTPTTL = 5 * time.Minute
	// MaxOTPAttempts is the number of wrong codes a cardholder may enter
	// before the challenge fails terminally. Three attempts is the accepted
	// 3-D Secure bound on a six-digit code.
	MaxOTPAttempts = 3
)

// NewOTP mints a numeric one-time code from the supplied cryptographic source.
// The caller passes crypto/rand.Reader; tests pass a deterministic reader.
func NewOTP(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("otp source is required")
	}
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(OTPDigits), nil)
	n, err := rand.Int(r, limit)
	if err != nil {
		return "", fmt.Errorf("minting one-time code: %w", err)
	}
	return fmt.Sprintf("%0*d", OTPDigits, n), nil
}

// HashOTP returns the digest stored for a code. The clear code is delivered to
// the cardholder and never persisted, logged or returned to the merchant.
func HashOTP(otp string) string {
	sum := sha256.Sum256([]byte(otp))
	return hex.EncodeToString(sum[:])
}

// VerifyOTP compares a presented code against a stored digest in constant time
// so an attacker cannot learn the code from response timing.
func VerifyOTP(hash, otp string) bool {
	want, err := hex.DecodeString(hash)
	if err != nil || len(want) == 0 {
		return false
	}
	got := sha256.Sum256([]byte(otp))
	return subtle.ConstantTimeCompare(want, got[:]) == 1
}

// ChallengeStatus is the 3-D Secure challenge state machine.
type ChallengeStatus string

const (
	ChallengePending   ChallengeStatus = "PENDING"
	ChallengeCompleted ChallengeStatus = "COMPLETED"
	ChallengeFailed    ChallengeStatus = "FAILED"
	ChallengeExpired   ChallengeStatus = "EXPIRED"
)

// Challenge is one step-up raised against a presentment. It is a value type so
// the state machine can be unit-tested without storage, and the persistence
// layer stores exactly these fields.
type Challenge struct {
	ID              string          `json:"challenge_id"`
	AuthorizationID string          `json:"authorization_id"`
	OTPHash         string          `json:"-"`
	Attempts        int             `json:"attempts"`
	MaxAttempts     int             `json:"max_attempts"`
	Status          ChallengeStatus `json:"status"`
	FailureReason   string          `json:"failure_reason,omitempty"`
	ExpiresAt       time.Time       `json:"expires_at"`
	CreatedAt       time.Time       `json:"created_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// NewChallenge opens a challenge that expires at now+ttl.
func NewChallenge(id, authorizationID, otpHash string, now time.Time, ttl time.Duration) *Challenge {
	if ttl <= 0 {
		ttl = OTPTTL
	}
	return &Challenge{
		ID:              id,
		AuthorizationID: authorizationID,
		OTPHash:         otpHash,
		MaxAttempts:     MaxOTPAttempts,
		Status:          ChallengePending,
		ExpiresAt:       now.Add(ttl),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// Expired reports whether the challenge is past its expiry at now.
func (c *Challenge) Expired(now time.Time) bool {
	return !c.ExpiresAt.IsZero() && !now.Before(c.ExpiresAt)
}

// Expire closes a challenge that reached its deadline without an answer. It is
// the sweeper's transition: the cardholder never responded, so the presentment
// is declined and nothing was ever held. A challenge that was already answered
// cannot be expired (single use), and a live one is refused.
func (c *Challenge) Expire(now time.Time) error {
	if c.Status != ChallengePending {
		return fmt.Errorf("%w: status is %s", ErrChallengeNotPending, c.Status)
	}
	if !c.Expired(now) {
		return fmt.Errorf("%w: deadline is %s", ErrChallengeNotExpired, c.ExpiresAt)
	}
	c.Status = ChallengeExpired
	c.FailureReason = "challenge_expired"
	c.UpdatedAt = now
	return nil
}

// Verify answers the challenge with a presented code and mutates the state
// machine:
//
//	nil                    -> COMPLETED (single use from here on)
//	ErrOTPInvalid          -> still PENDING, one attempt consumed
//	ErrOTPAttemptsExhausted-> FAILED (terminal)
//	ErrChallengeExpired    -> EXPIRED (terminal)
//	ErrChallengeNotPending -> no change; the challenge was already settled
//
// The caller persists the returned state; the attempt counter is what bounds a
// brute-force search of the code space.
func (c *Challenge) Verify(otp string, now time.Time) error {
	if c.Status != ChallengePending {
		return fmt.Errorf("%w: status is %s", ErrChallengeNotPending, c.Status)
	}
	if c.Expired(now) {
		c.Status = ChallengeExpired
		c.FailureReason = "challenge_expired"
		c.UpdatedAt = now
		return ErrChallengeExpired
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = MaxOTPAttempts
	}
	if !VerifyOTP(c.OTPHash, otp) {
		c.Attempts++
		c.UpdatedAt = now
		if c.Attempts >= c.MaxAttempts {
			c.Status = ChallengeFailed
			c.FailureReason = "attempts_exhausted"
			return ErrOTPAttemptsExhausted
		}
		return fmt.Errorf("%w: %d of %d attempts used", ErrOTPInvalid, c.Attempts, c.MaxAttempts)
	}
	ts := now.UTC()
	c.Status = ChallengeCompleted
	c.CompletedAt = &ts
	c.UpdatedAt = ts
	return nil
}
