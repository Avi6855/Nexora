package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/events"
	"github.com/nexora/nexora/services/card-service/internal/repository"
)

type CardService struct {
	cardRepo   repository.CardRepository
	publisher  events.EventPublisher
	logger     zerolog.Logger
}

func NewCardService(cardRepo repository.CardRepository, publisher events.EventPublisher, logger zerolog.Logger) *CardService {
	return &CardService{
		cardRepo:  cardRepo,
		publisher: publisher,
		logger:    logger,
	}
}

func (s *CardService) CreateCard(ctx context.Context, userID, accountID uuid.UUID, cardType domain.CardType, spendingLimit, dailyLimit, monthlyLimit int64, currency string) (*domain.Card, error) {
	s.logger.Info().Str("user_id", userID.String()).Str("account_id", accountID.String()).Msg("creating card")

	last4 := generateLast4()
	card := domain.NewCard(userID, accountID, last4, cardType, spendingLimit, dailyLimit, monthlyLimit, currency)

	if err := s.cardRepo.Create(ctx, card); err != nil {
		return nil, fmt.Errorf("storing card: %w", err)
	}

	_ = s.publisher.PublishCardEvent(ctx, events.EventTypeCardCreated, card, card.CardID.String(), "")

	return card, nil
}

func (s *CardService) GetCardsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Card, error) {
	return s.cardRepo.GetByUserID(ctx, userID)
}

func (s *CardService) GetCard(ctx context.Context, id uuid.UUID) (*domain.Card, error) {
	return s.cardRepo.GetByID(ctx, id)
}

func (s *CardService) FreezeCard(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("card_id", id.String()).Msg("freezing card")

	card, err := s.cardRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := card.Freeze(); err != nil {
		return err
	}

	if err := s.cardRepo.Update(ctx, card); err != nil {
		return fmt.Errorf("persisting card freeze: %w", err)
	}

	_ = s.publisher.PublishCardEvent(ctx, events.EventTypeCardFrozen, card, card.CardID.String(), "")
	return nil
}

func (s *CardService) UnfreezeCard(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("card_id", id.String()).Msg("unfreezing card")

	card, err := s.cardRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := card.Unfreeze(); err != nil {
		return err
	}

	if err := s.cardRepo.Update(ctx, card); err != nil {
		return fmt.Errorf("persisting card unfreeze: %w", err)
	}

	_ = s.publisher.PublishCardEvent(ctx, events.EventTypeCardUnfrozen, card, card.CardID.String(), "")
	return nil
}

func (s *CardService) BlockCard(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("card_id", id.String()).Msg("blocking card")

	card, err := s.cardRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := card.Block(); err != nil {
		return err
	}

	if err := s.cardRepo.Update(ctx, card); err != nil {
		return fmt.Errorf("persisting card block: %w", err)
	}

	_ = s.publisher.PublishCardEvent(ctx, events.EventTypeCardBlocked, card, card.CardID.String(), "")
	return nil
}

func (s *CardService) UpdateSpendingLimits(ctx context.Context, id uuid.UUID, dailyLimit, monthlyLimit int64) error {
	s.logger.Info().Str("card_id", id.String()).Msg("updating card spending limits")

	card, err := s.cardRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if card.Status == domain.CardStatusBlocked {
		return fmt.Errorf("%w: cannot update limits on a blocked card", domain.ErrInvalidCardState)
	}
	if card.Status == domain.CardStatusExpired {
		return fmt.Errorf("%w: cannot update limits on an expired card", domain.ErrInvalidCardState)
	}

	card.UpdateSpendingLimits(dailyLimit, monthlyLimit)

	if err := s.cardRepo.Update(ctx, card); err != nil {
		return fmt.Errorf("persisting card limits update: %w", err)
	}

	_ = s.publisher.PublishCardEvent(ctx, events.EventTypeCardUpdated, card, card.CardID.String(), "")
	return nil
}

func generateLast4() string {
	return fmt.Sprintf("%04d", time.Now().UnixNano()%10000)
}
