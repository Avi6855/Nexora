package unit

import (
	"testing"
	"time"

	"github.com/nexora/nexora/shared/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword(t *testing.T) {
	hash, err := auth.HashPassword("securePassword123")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "securePassword123", hash)
}

func TestHashPasswordDifferentHashes(t *testing.T) {
	hash1, err := auth.HashPassword("password123")
	require.NoError(t, err)

	hash2, err := auth.HashPassword("password123")
	require.NoError(t, err)

	assert.NotEqual(t, hash1, hash2, "bcrypt should produce different hashes for same password")
}

func TestVerifyPassword(t *testing.T) {
	password := "mySecurePassword"
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)

	assert.True(t, auth.VerifyPassword(password, hash))
	assert.False(t, auth.VerifyPassword("wrongPassword", hash))
	assert.False(t, auth.VerifyPassword("", hash))
}

func TestTokenManagerGenerateAccessToken(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	token, err := tm.GenerateAccessToken("user-123", "test@example.com", []string{"user"}, "device-abc")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestTokenManagerGenerateRefreshToken(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	token, err := tm.GenerateRefreshToken("user-123", "device-abc")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestTokenManagerGenerateTokenPair(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	pair, err := tm.GenerateTokenPair("user-123", "test@example.com", []string{"user", "admin"}, "device-abc")
	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Greater(t, pair.ExpiresAt, time.Now().Unix())
}

func TestTokenManagerValidateToken(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	token, err := tm.GenerateAccessToken("user-123", "test@example.com", []string{"user"}, "device-abc")
	require.NoError(t, err)

	claims, err := tm.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
	assert.Equal(t, "test@example.com", claims.Email)
	assert.Equal(t, []string{"user"}, claims.Roles)
	assert.Equal(t, "device-abc", claims.DeviceID)
}

func TestTokenManagerValidateEmptyToken(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	_, err := tm.ValidateToken("")
	assert.ErrorIs(t, err, auth.ErrTokenRequired)
}

func TestTokenManagerValidateInvalidToken(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	_, err := tm.ValidateToken("invalid.token.here")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenManagerValidateExpiredToken(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", -1*time.Second, 7*24*time.Hour)

	token, err := tm.GenerateAccessToken("user-123", "test@example.com", []string{"user"}, "device-abc")
	require.NoError(t, err)

	_, err = tm.ValidateToken(token)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenManagerValidateWrongSecret(t *testing.T) {
	tm1 := auth.NewTokenManager("secret-one", 15*time.Minute, 7*24*time.Hour)
	tm2 := auth.NewTokenManager("secret-two", 15*time.Minute, 7*24*time.Hour)

	token, err := tm1.GenerateAccessToken("user-123", "test@example.com", []string{"user"}, "device-abc")
	require.NoError(t, err)

	_, err = tm2.ValidateToken(token)
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestTokenManagerRefreshTokenClaims(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	token, err := tm.GenerateRefreshToken("user-456", "device-xyz")
	require.NoError(t, err)

	claims, err := tm.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-456", claims.UserID)
	assert.Equal(t, "device-xyz", claims.DeviceID)
	assert.Equal(t, "nexora", claims.Issuer)
}

func TestTokenManagerAccessTokenHasRoles(t *testing.T) {
	tm := auth.NewTokenManager("test-secret-key-for-jwt-signing", 15*time.Minute, 7*24*time.Hour)

	roles := []string{"user", "admin", "support"}
	token, err := tm.GenerateAccessToken("user-123", "test@example.com", roles, "device-abc")
	require.NoError(t, err)

	claims, err := tm.ValidateToken(token)
	require.NoError(t, err)
	assert.Equal(t, roles, claims.Roles)
}
