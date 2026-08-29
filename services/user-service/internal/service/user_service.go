package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/user-service/internal/domain"
	"github.com/nexora/nexora/services/user-service/internal/events"
	"github.com/nexora/nexora/services/user-service/internal/repository"
)

type UserService struct {
	userRepo repository.UserRepository
	producer *events.KafkaProducer
	logger   zerolog.Logger
}

func NewUserService(userRepo repository.UserRepository, producer *events.KafkaProducer, logger zerolog.Logger) *UserService {
	return &UserService{
		userRepo: userRepo,
		producer: producer,
		logger:   logger,
	}
}

func (s *UserService) GetUserByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	s.logger.Info().Str("user_id", id.String()).Msg("getting user")

	user, err := s.userRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (s *UserService) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	s.logger.Info().Str("email", email).Msg("getting user by email")

	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (s *UserService) UpdateUser(ctx context.Context, id uuid.UUID, req *domain.UpdateUserRequest) (*domain.User, error) {
	s.logger.Info().Str("user_id", id.String()).Msg("updating user")

	user, err := s.userRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("user not found: %w", err)
	}

	if req.FirstName != nil {
		user.FirstName = *req.FirstName
	}
	if req.LastName != nil {
		user.LastName = *req.LastName
	}
	if req.Phone != nil {
		user.Phone = *req.Phone
	}

	user.UpdatedAt = time.Now().UTC()

	if err := s.userRepo.Update(ctx, user); err != nil {
		return nil, fmt.Errorf("updating user: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "user.updated", user)
	}

	s.logger.Info().Str("user_id", id.String()).Msg("user updated")
	return user, nil
}
