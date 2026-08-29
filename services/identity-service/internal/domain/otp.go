package domain

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
)

type OTPPurpose string

const (
	OTPPurposeLogin    OTPPurpose = "LOGIN"
	OTPPurposeVerify   OTPPurpose = "VERIFY"
	OTPPurposeReset    OTPPurpose = "RESET"
)

type OTP struct {
	OTPID     uuid.UUID   `json:"otp_id"`
	UserID    uuid.UUID   `json:"user_id"`
	Code      string      `json:"code"`
	Purpose   OTPPurpose  `json:"purpose"`
	CreatedAt time.Time   `json:"created_at"`
	ExpiresAt time.Time   `json:"expires_at"`
	Used      bool        `json:"used"`
}

func NewOTP(userID uuid.UUID, purpose OTPPurpose, ttl time.Duration) (*OTP, error) {
	code, err := generateOTPCode(6)
	if err != nil {
		return nil, fmt.Errorf("generating OTP code: %w", err)
	}

	now := time.Now().UTC()
	return &OTP{
		OTPID:     uuid.New(),
		UserID:    userID,
		Code:      code,
		Purpose:   purpose,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Used:      false,
	}, nil
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

func (o *OTP) IsValid(code string) bool {
	return o.Code == code && !o.IsExpired() && !o.Used
}
