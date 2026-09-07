package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/identity-service/internal/domain"
	"github.com/nexora/nexora/services/identity-service/internal/repository"
	"github.com/nexora/nexora/shared/auth"
	domainerrors "github.com/nexora/nexora/shared/errors"
	"github.com/rs/zerolog"
)

type AuthService struct {
	userRepo     repository.UserRepository
	otpRepo      repository.OTPRepository
	deviceRepo   repository.DeviceRepository
	refreshRepo  repository.RefreshTokenRepository
	tokenMgr     *auth.TokenManager
	otpTTL       time.Duration
	refreshTTL   time.Duration
	loginLimiter *RateLimiter
	logger       zerolog.Logger
}

func NewAuthService(
	userRepo repository.UserRepository,
	otpRepo repository.OTPRepository,
	deviceRepo repository.DeviceRepository,
	refreshRepo repository.RefreshTokenRepository,
	tokenMgr *auth.TokenManager,
	otpTTL time.Duration,
	refreshTTL time.Duration,
	logger zerolog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:     userRepo,
		otpRepo:      otpRepo,
		deviceRepo:   deviceRepo,
		refreshRepo:  refreshRepo,
		tokenMgr:     tokenMgr,
		otpTTL:       otpTTL,
		refreshTTL:   refreshTTL,
		loginLimiter: NewRateLimiter(5, 15*time.Minute, 15*time.Minute),
		logger:       logger,
	}
}

func (s *AuthService) Register(ctx context.Context, req *domain.RegisterRequest) (*domain.User, error) {
	s.logger.Info().Str("email", req.Email).Msg("user registration attempt")

	existing, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err == nil && existing != nil {
		// Do not leak whether the address is already registered.
		return nil, domainerrors.ErrValidation
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	user := domain.NewUser(req.Email, req.Phone, passwordHash, req.FirstName, req.LastName)

	if err := s.userRepo.Create(ctx, user); err != nil {
		s.logger.Warn().Err(err).Str("email", req.Email).Msg("user registration failed")
		return nil, fmt.Errorf("creating user: %w", err)
	}

	s.logger.Info().Str("user_id", user.UserID.String()).Msg("user registered successfully")
	return user, nil
}

func (s *AuthService) Login(ctx context.Context, req *domain.LoginRequest) (*auth.TokenPair, error) {
	s.logger.Info().Str("email", req.Email).Msg("login attempt")

	limiterKey := "login:" + req.Email + ":" + remoteIP(ctx)
	if !s.loginLimiter.Allow(limiterKey, false) {
		return nil, domainerrors.ErrRateLimited
	}

	user, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err != nil || user == nil {
		// Generic message: do not disclose that the account exists.
		s.loginLimiter.Allow(limiterKey, true)
		s.logger.Warn().Str("email", req.Email).Msg("login failed: unknown user")
		return nil, domainerrors.ErrUnauthorized
	}

	if user.Status != domain.UserStatusActive ||
		!auth.VerifyPassword(req.Password, user.PasswordHash) {
		s.loginLimiter.Allow(limiterKey, true)
		s.logger.Warn().Str("email", req.Email).Msg("login failed: bad credentials")
		return nil, domainerrors.ErrUnauthorized
	}

	s.loginLimiter.Reset(limiterKey)

	roles := []string{"user"}
	tokenPair, err := s.issueTokenPair(ctx, user, roles, req.DeviceID)
	if err != nil {
		s.logger.Error().Err(err).Msg("login failed: token issuance error")
		return nil, err
	}

	s.logger.Info().Str("user_id", user.UserID.String()).Msg("login successful")
	return tokenPair, nil
}

func (s *AuthService) RequestOTP(ctx context.Context, userID uuid.UUID, purpose domain.OTPPurpose) error {
	s.logger.Info().Str("user_id", userID.String()).Msg("OTP request")

	otp, err := domain.NewOTP(userID, purpose, s.otpTTL)
	if err != nil {
		return fmt.Errorf("generating OTP: %w", err)
	}

	if err := s.otpRepo.Create(ctx, otp); err != nil {
		return fmt.Errorf("storing OTP: %w", err)
	}

	s.logger.Info().Str("user_id", userID.String()).Str("purpose", string(purpose)).Msg("OTP generated")
	return nil
}

func (s *AuthService) VerifyOTP(ctx context.Context, userID uuid.UUID, code string, purpose domain.OTPPurpose, deviceID string) (*auth.TokenPair, error) {
	s.logger.Info().Str("user_id", userID.String()).Str("purpose", string(purpose)).Msg("OTP verification")

	otp, err := s.otpRepo.GetLatestByUserAndPurpose(ctx, userID, purpose)
	if err != nil {
		return nil, fmt.Errorf("fetching OTP: %w", err)
	}
	if otp == nil {
		return nil, domainerrors.ErrUnauthorized
	}

	if !otp.IsValid(code) {
		if !otp.Used && !otp.IsExpired() && !otp.OutOfAttempts() {
			_ = s.otpRepo.IncrementAttempts(ctx, userID, purpose, otp.CreatedAt)
		}
		s.logger.Warn().Str("user_id", userID.String()).Msg("invalid OTP")
		return nil, domainerrors.ErrUnauthorized
	}

	// Mark the OTP as consumed so replaying the same code fails.
	if err := s.otpRepo.MarkUsed(ctx, userID, purpose, otp.CreatedAt); err != nil {
		s.logger.Warn().Err(err).Str("user_id", userID.String()).Msg("failed to mark OTP as used")
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, domainerrors.ErrUserNotFound
	}

	if purpose == domain.OTPPurposeVerify {
		user.EmailVerified = true
		user.UpdatedAt = time.Now().UTC()
		if err := s.userRepo.Update(ctx, user); err != nil {
			return nil, fmt.Errorf("updating user: %w", err)
		}
	}

	roles := []string{"user"}
	tokenPair, err := s.issueTokenPair(ctx, user, roles, deviceID)
	if err != nil {
		return nil, err
	}

	s.logger.Info().Str("user_id", userID.String()).Msg("OTP verified successfully")
	return tokenPair, nil
}

// RefreshToken rotates the presented opaque refresh token: the old digest is
// revoked and a fresh pair is issued, so a stolen token cannot be replayed and
// dies on first use.
func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*auth.TokenPair, error) {
	if refreshToken == "" {
		return nil, domainerrors.ErrUnauthorized
	}

	rec, err := s.refreshRepo.GetByHash(ctx, hashToken(refreshToken))
	if err != nil {
		return nil, domainerrors.ErrUnauthorized
	}
	if rec == nil || rec.Revoked || time.Now().UTC().After(rec.ExpiresAt) {
		return nil, domainerrors.ErrUnauthorized
	}

	user, err := s.userRepo.GetByID(ctx, rec.UserID)
	if err != nil || user == nil {
		return nil, domainerrors.ErrUnauthorized
	}
	if user.Status != domain.UserStatusActive {
		return nil, domainerrors.ErrUnauthorized
	}

	// Rotate: revoke the old token before issuing the new one.
	if err := s.refreshRepo.Revoke(ctx, rec.TokenHash); err != nil {
		s.logger.Warn().Err(err).Msg("failed to revoke rotated refresh token")
		return nil, domainerrors.ErrInternal
	}

	roles := []string{"user"}
	tokenPair, err := s.issueTokenPair(ctx, user, roles, rec.DeviceID)
	if err != nil {
		return nil, err
	}

	s.logger.Info().Str("user_id", user.UserID.String()).Msg("token refreshed")
	return tokenPair, nil
}

// Logout revokes the presented refresh token (and, when a device id is given,
// every session on that device).
func (s *AuthService) Logout(ctx context.Context, userID uuid.UUID, refreshToken, deviceID string) error {
	if refreshToken != "" {
		if err := s.refreshRepo.Revoke(ctx, hashToken(refreshToken)); err != nil {
			s.logger.Warn().Err(err).Msg("logout revocation failed")
			return fmt.Errorf("revoking session: %w", err)
		}
	}
	if deviceID != "" {
		if err := s.refreshRepo.RevokeAllForUserAndDevice(ctx, userID, deviceID); err != nil {
			return fmt.Errorf("revoking device sessions: %w", err)
		}
	}
	s.logger.Info().Str("user_id", userID.String()).Msg("logout")
	return nil
}

func (s *AuthService) RegisterDevice(ctx context.Context, userID uuid.UUID, req *domain.DeviceRequest) (*domain.Device, error) {
	s.logger.Info().Str("user_id", userID.String()).Msg("device registration")

	device := domain.NewDevice(userID, req.DeviceName, domain.DeviceType(req.DeviceType), req.FCMToken)

	if err := s.deviceRepo.Create(ctx, device); err != nil {
		return nil, fmt.Errorf("registering device: %w", err)
	}

	s.logger.Info().Str("user_id", userID.String()).Str("device_id", device.DeviceID.String()).Msg("device registered")
	return device, nil
}

// issueTokenPair mints an access JWT + a new opaque refresh token and persists
// the refresh token digest before returning.
func (s *AuthService) issueTokenPair(ctx context.Context, user *domain.User, roles []string, deviceID string) (*auth.TokenPair, error) {
	accessToken, err := s.tokenMgr.GenerateAccessToken(user.UserID.String(), user.Email, roles, deviceID)
	if err != nil {
		return nil, fmt.Errorf("generating access token: %w", err)
	}

	refreshToken, err := newOpaqueToken()
	if err != nil {
		return nil, fmt.Errorf("generating refresh token: %w", err)
	}

	now := time.Now().UTC()
	rec := &repository.RefreshTokenRecord{
		TokenHash: hashToken(refreshToken),
		UserID:    user.UserID,
		DeviceID:  deviceID,
		ExpiresAt: now.Add(s.refreshTTL),
		Revoked:   false,
		CreatedAt: now,
	}
	if err := s.refreshRepo.Save(ctx, rec); err != nil {
		return nil, fmt.Errorf("persisting refresh token: %w", err)
	}

	return &auth.TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    now.Add(s.tokenMgr.AccessTokenTTL()).Unix(),
	}, nil
}

func newOpaqueToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
