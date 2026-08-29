package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/auth"
	domainerrors "github.com/nexora/nexora/shared/errors"
	"github.com/nexora/nexora/services/identity-service/internal/domain"
	"github.com/nexora/nexora/services/identity-service/internal/repository"
)

type AuthService struct {
	userRepo    repository.UserRepository
	otpRepo     repository.OTPRepository
	deviceRepo  repository.DeviceRepository
	tokenMgr    *auth.TokenManager
	otpTTL      time.Duration
	logger      zerolog.Logger
}

func NewAuthService(
	userRepo repository.UserRepository,
	otpRepo repository.OTPRepository,
	deviceRepo repository.DeviceRepository,
	tokenMgr *auth.TokenManager,
	otpTTL time.Duration,
	logger zerolog.Logger,
) *AuthService {
	return &AuthService{
		userRepo:   userRepo,
		otpRepo:    otpRepo,
		deviceRepo: deviceRepo,
		tokenMgr:   tokenMgr,
		otpTTL:     otpTTL,
		logger:     logger,
	}
}

func (s *AuthService) Register(ctx context.Context, req *domain.RegisterRequest) (*domain.User, error) {
	s.logger.Info().Str("email", req.Email).Msg("user registration attempt")

	existing, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("email already registered: %s", req.Email)
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	user := domain.NewUser(req.Email, req.Phone, passwordHash, req.FirstName, req.LastName)

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("creating user: %w", err)
	}

	s.logger.Info().Str("user_id", user.UserID.String()).Msg("user registered successfully")
	return user, nil
}

func (s *AuthService) Login(ctx context.Context, req *domain.LoginRequest) (*auth.TokenPair, error) {
	s.logger.Info().Str("email", req.Email).Msg("login attempt")

	user, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err != nil {
		return nil, domainerrors.ErrUserNotFound
	}

	if user.Status != domain.UserStatusActive {
		return nil, fmt.Errorf("account is %s", user.Status)
	}

	if !auth.VerifyPassword(req.Password, user.PasswordHash) {
		s.logger.Warn().Str("email", req.Email).Msg("invalid password")
		return nil, domainerrors.ErrUnauthorized
	}

	roles := []string{"user"}
	tokenPair, err := s.tokenMgr.GenerateTokenPair(user.UserID.String(), user.Email, roles, req.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("generating tokens: %w", err)
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
		return nil, fmt.Errorf("no OTP found")
	}

	if !otp.IsValid(code) {
		s.logger.Warn().Str("user_id", userID.String()).Msg("invalid OTP")
		return nil, fmt.Errorf("invalid or expired OTP")
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
	tokenPair, err := s.tokenMgr.GenerateTokenPair(user.UserID.String(), user.Email, roles, deviceID)
	if err != nil {
		return nil, fmt.Errorf("generating tokens: %w", err)
	}

	s.logger.Info().Str("user_id", userID.String()).Msg("OTP verified successfully")
	return tokenPair, nil
}

func (s *AuthService) RefreshToken(ctx context.Context, refreshToken string) (*auth.TokenPair, error) {
	s.logger.Info().Msg("token refresh")

	claims, err := s.tokenMgr.ValidateToken(refreshToken)
	if err != nil {
		return nil, domainerrors.ErrUnauthorized
	}

	userID, err := uuid.Parse(claims.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user ID in token: %w", err)
	}

	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, domainerrors.ErrUserNotFound
	}

	if user.Status != domain.UserStatusActive {
		return nil, fmt.Errorf("account is %s", user.Status)
	}

	roles := []string{"user"}
	tokenPair, err := s.tokenMgr.GenerateTokenPair(user.UserID.String(), user.Email, roles, claims.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("generating tokens: %w", err)
	}

	s.logger.Info().Str("user_id", user.UserID.String()).Msg("token refreshed")
	return tokenPair, nil
}

func (s *AuthService) Logout(ctx context.Context, userID uuid.UUID, deviceID string) error {
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
