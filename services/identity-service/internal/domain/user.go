package domain

import (
	"time"

	"github.com/google/uuid"
)

type UserStatus string

const (
	UserStatusActive    UserStatus = "ACTIVE"
	UserStatusInactive  UserStatus = "INACTIVE"
	UserStatusSuspended UserStatus = "SUSPENDED"
)

type User struct {
	UserID        uuid.UUID  `json:"user_id"`
	Email         string     `json:"email"`
	Phone         string     `json:"phone"`
	PasswordHash  string     `json:"-"`
	FirstName     string     `json:"first_name"`
	LastName      string     `json:"last_name"`
	Status        UserStatus `json:"status"`
	EmailVerified bool       `json:"email_verified"`
	PhoneVerified bool       `json:"phone_verified"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

func NewUser(email, phone, passwordHash, firstName, lastName string) *User {
	now := time.Now().UTC()
	return &User{
		UserID:        uuid.New(),
		Email:         email,
		Phone:         phone,
		PasswordHash:  passwordHash,
		FirstName:     firstName,
		LastName:      lastName,
		Status:        UserStatusActive,
		EmailVerified: false,
		PhoneVerified: false,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

type RegisterRequest struct {
	DeviceID  string `json:"device_id"`
	Email     string `json:"email"`
	Phone     string `json:"phone_number"`
	Password  string `json:"password"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	DeviceID string `json:"device_id"`
}

type OTPVerifyRequest struct {
	Code     string `json:"code"`
	Purpose  string `json:"purpose"`
	DeviceID string `json:"device_id"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type LogoutRequest struct {
	RefreshToken string `json:"refresh_token"`
	DeviceID     string `json:"device_id"`
}

type DeviceRequest struct {
	DeviceName string `json:"device_name"`
	DeviceType string `json:"device_type"`
	FCMToken   string `json:"fcm_token"`
}

type AuthResponse struct {
	User  *User  `json:"user,omitempty"`
	Token string `json:"token,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}
