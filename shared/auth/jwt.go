package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var (
	ErrInvalidToken  = errors.New("invalid or expired token")
	ErrTokenRequired = errors.New("token is required")
)

type Claims struct {
	UserID   string   `json:"user_id"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
	DeviceID string   `json:"device_id,omitempty"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

type TokenManager struct {
	secret        []byte
	accessTTL     time.Duration
	refreshTTL    time.Duration
}

func NewTokenManager(secret string, accessTTL, refreshTTL time.Duration) *TokenManager {
	return &TokenManager{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

// AccessTokenTTL exposes the configured access-token lifetime (used by
// services to report token expiry without parsing the token).
func (tm *TokenManager) AccessTokenTTL() time.Duration {
	return tm.accessTTL
}

func (tm *TokenManager) GenerateAccessToken(userID, email string, roles []string, deviceID string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		Email:    email,
		Roles:    roles,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(tm.accessTTL)),
			Issuer:    "nexora",
			Subject:   userID,
			Audience:  jwt.ClaimStrings{"nexora-api"},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(tm.secret)
}

func (tm *TokenManager) GenerateRefreshToken(userID, deviceID string) (string, error) {
	claims := &Claims{
		UserID:   userID,
		DeviceID: deviceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uuid.New().String(),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(time.Now().UTC().Add(tm.refreshTTL)),
			Issuer:    "nexora",
			Subject:   userID,
			Audience:  jwt.ClaimStrings{"nexora-api"},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(tm.secret)
}

func (tm *TokenManager) GenerateTokenPair(userID, email string, roles []string, deviceID string) (*TokenPair, error) {
	accessToken, err := tm.GenerateAccessToken(userID, email, roles, deviceID)
	if err != nil {
		return nil, err
	}

	refreshToken, err := tm.GenerateRefreshToken(userID, deviceID)
	if err != nil {
		return nil, err
	}

	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().UTC().Add(tm.accessTTL).Unix(),
	}, nil
}

func (tm *TokenManager) ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrTokenRequired
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return tm.secret, nil
	}, jwt.WithIssuer("nexora"), jwt.WithAudience("nexora-api"))

	if err != nil {
		return nil, ErrInvalidToken
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
