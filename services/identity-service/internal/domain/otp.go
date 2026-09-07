package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

type OTPPurpose string

const (
	OTPPurposeLogin  OTPPurpose = "LOGIN"
	OTPPurposeVerify OTPPurpose = "VERIFY"
	OTPPurposeReset  OTPPurpose = "RESET"
)

// MaxOTPAttempts bounds guesses per issued code before it is invalidated.
const MaxOTPAttempts = 5

type OTP struct {
	OTPID     uuid.UUID  `json:"otp_id"`
	UserID    uuid.UUID  `json:"user_id"`
	Code      string     `json:"code"` // SHA-256 hex digest of the code, never the code itself
	Purpose   OTPPurpose `json:"purpose"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	Used      bool       `json:"used"`
	Attempts  int        `json:"attempts"`
}

// NewOTP generates a random code and stores only its digest.
func NewOTP(userID uuid.UUID, purpose OTPPurpose, ttl time.Duration) (*OTP, error) {
	code, err := generateOTPCode(6)
	if err != nil {
		return nil, fmt.Errorf("generating OTP code: %w", err)
	}

	now := time.Now().UTC()
	return &OTP{
		OTPID:     uuid.New(),
		UserID:    userID,
		Code:      HashOTPCode(code),
		Purpose:   purpose,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Used:      false,
		Attempts:  0,
	}, nil
}

// HashOTPCode digests a plaintext code (constant output length for timing).
func HashOTPCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func generateOTPCode(length int) (string, error) {
	max := big.NewInt(1)
	max.Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n.Int64()), nil
}

func (o *OTP) IsExpired() bool {
	return time.Now().UTC().After(o.ExpiresAt)
}

// OutOfAttempts reports whether the code has been guessed too many times.
func (o *OTP) OutOfAttempts() bool {
	return o.Attempts >= MaxOTPAttempts
}

// IsValid compares a submitted code against the stored digest in
// constant time and checks expiry/consumption/attempt bounds.
func (o *OTP) IsValid(code string) bool {
	if o.Used || o.IsExpired() || o.OutOfAttempts() {
		return false
	}
	got := HashOTPCode(code)
	return subtle.ConstantTimeCompare([]byte(got), []byte(o.Code)) == 1
}
