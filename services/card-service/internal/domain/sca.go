package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/shared/schemes"
)

// SCAChallenge is the persisted PSD2 / 3-D Secure step-up raised against one
// card presentment.
//
// It carries the presentment context (cardholder, account, amount, merchant)
// so the challenge is bound to what the cardholder is actually approving —
// a challenge answered for a different amount or payee is not valid. The
// one-time code itself is never stored: only its SHA-256 digest, which the
// cardholder's app answers against.
//
// The attempt/expiry state machine lives in shared/schemes so the card
// platform, the acquirer-facing 3-D Secure server and every future consumer
// share one implementation of the rules.
type SCAChallenge struct {
	ChallengeID     uuid.UUID               `json:"challenge_id"`
	AuthorizationID uuid.UUID               `json:"authorization_id"`
	CardID          uuid.UUID               `json:"card_id"`
	UserID          uuid.UUID               `json:"user_id"`
	AccountID       uuid.UUID               `json:"account_id"`
	Amount          int64                   `json:"amount"`
	Currency        string                  `json:"currency"`
	Merchant        string                  `json:"merchant"`
	OTPHash         string                  `json:"-"`
	Attempts        int                     `json:"attempts"`
	MaxAttempts     int                     `json:"max_attempts"`
	Status          schemes.ChallengeStatus `json:"status"`
	FailureReason   string                  `json:"failure_reason,omitempty"`
	ExpiresAt       time.Time               `json:"expires_at"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	CompletedAt     *time.Time              `json:"completed_at,omitempty"`
}

// AsSchemeChallenge projects the persisted row onto the shared state machine.
func (c *SCAChallenge) AsSchemeChallenge() *schemes.Challenge {
	return &schemes.Challenge{
		ID:              c.ChallengeID.String(),
		AuthorizationID: c.AuthorizationID.String(),
		OTPHash:         c.OTPHash,
		Attempts:        c.Attempts,
		MaxAttempts:     c.MaxAttempts,
		Status:          c.Status,
		FailureReason:   c.FailureReason,
		ExpiresAt:       c.ExpiresAt,
		CreatedAt:       c.CreatedAt,
		CompletedAt:     c.CompletedAt,
		UpdatedAt:       c.UpdatedAt,
	}
}

// Verify answers the challenge with a one-time code and copies the resulting
// state (attempt count, terminal status, completion timestamp) back onto the
// row. The caller persists it; on error the state may still have advanced
// (a wrong code consumes an attempt, an expired challenge becomes EXPIRED), so
// a failed verification must still be written back.
func (c *SCAChallenge) Verify(otp string, now time.Time) error {
	ch := c.AsSchemeChallenge()
	err := ch.Verify(otp, now)
	c.Attempts = ch.Attempts
	c.MaxAttempts = ch.MaxAttempts
	c.Status = ch.Status
	c.FailureReason = ch.FailureReason
	c.UpdatedAt = ch.UpdatedAt
	c.CompletedAt = ch.CompletedAt
	return err
}

// Pending reports whether the challenge can still be answered.
func (c *SCAChallenge) Pending() bool {
	return c.Status == schemes.ChallengePending
}

// Expire closes a challenge whose deadline passed without an answer and copies
// the resulting state back onto the row (the caller persists it).
func (c *SCAChallenge) Expire(now time.Time) error {
	ch := c.AsSchemeChallenge()
	err := ch.Expire(now)
	c.Status = ch.Status
	c.FailureReason = ch.FailureReason
	c.UpdatedAt = ch.UpdatedAt
	return err
}

// SCAExpiring is one entry of the time-bucketed expiry index: a step-up that
// becomes due at ExpiresAt. Cassandra cannot scan a table by a non-key column,
// so challenges are also written into an hourly bucket where the deadline is a
// clustering column and the due set can be read with a range query.
type SCAExpiring struct {
	Bucket      string    `json:"bucket"`
	ExpiresAt   time.Time `json:"expires_at"`
	ChallengeID uuid.UUID `json:"challenge_id"`
}

// ExpiryBucket names the hourly partition a challenge's deadline belongs to.
func ExpiryBucket(t time.Time) string {
	return t.UTC().Format("2006010215")
}

// CompleteSCAChallengeRequest is the cardholder's answer to a step-up: the
// challenge they were shown plus the one-time code delivered to their app.
type CompleteSCAChallengeRequest struct {
	ChallengeID uuid.UUID `json:"challenge_id"`
	OTP         string    `json:"otp"`
}

func (r *CompleteSCAChallengeRequest) Validate() error {
	if r.ChallengeID == uuid.Nil {
		return errors.New("challenge_id is required")
	}
	if strings.TrimSpace(r.OTP) == "" {
		return errors.New("otp is required")
	}
	return nil
}
