package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/pot-service/internal/clients"
	"github.com/nexora/nexora/services/pot-service/internal/domain"
	"github.com/nexora/nexora/services/pot-service/internal/events"
	"github.com/nexora/nexora/services/pot-service/internal/repository"
	"github.com/rs/zerolog"
)

var (
	ErrInsufficientFunds = clients.ErrInsufficientFunds
	ErrAccountNotOwned   = errors.New("account does not belong to caller")
)

type PotService struct {
	potRepo       repository.PotRepository
	publisher     events.EventPublisher
	ledgerClient  *clients.LedgerClient
	accountClient *clients.AccountClient
	logger        zerolog.Logger
}

func NewPotService(potRepo repository.PotRepository, publisher events.EventPublisher, ledgerClient *clients.LedgerClient, accountClient *clients.AccountClient, logger zerolog.Logger) *PotService {
	return &PotService{
		potRepo:       potRepo,
		publisher:     publisher,
		ledgerClient:  ledgerClient,
		accountClient: accountClient,
		logger:        logger,
	}
}

func (s *PotService) CreatePot(ctx context.Context, userID uuid.UUID, req *domain.CreatePotRequest) (*domain.Pot, error) {
	s.logger.Info().Str("user_id", userID.String()).Str("name", req.Name).Msg("creating pot")

	if req.TargetAmount <= 0 {
		return nil, fmt.Errorf("target_amount must be positive")
	}
	if req.Currency == "" {
		req.Currency = "GBP"
	}

	pot := domain.NewPot(userID, req.Name, req.TargetAmount, req.Currency, req.RoundUp)

	if err := s.potRepo.Create(ctx, pot); err != nil {
		return nil, fmt.Errorf("storing pot: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotCreated, pot, pot.PotID.String(), "")

	return pot, nil
}

func (s *PotService) GetPotsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Pot, error) {
	return s.potRepo.GetByUserID(ctx, userID)
}

// GetPotForUser returns a pot only when the caller owns it.
func (s *PotService) GetPotForUser(ctx context.Context, userID, potID uuid.UUID) (*domain.Pot, error) {
	pot, err := s.potRepo.GetByID(ctx, potID)
	if err != nil {
		return nil, err
	}
	if pot.UserID != userID {
		return nil, domain.ErrPotNotFound
	}
	return pot, nil
}

// Deposit moves real money from the caller's funding account into the pot:
// the ledger debits the account (checking its available balance atomically)
// and credits the pot's own ledger account (pot_id). Pot balances therefore
// always represent booked money — no balance is ever minted or double counted.
func (s *PotService) Deposit(ctx context.Context, potID, userID, accountID uuid.UUID, amount int64, correlationID string) error {
	s.logger.Info().Str("pot_id", potID.String()).Int64("amount", amount).Msg("depositing to pot")

	if amount <= 0 {
		return domain.ErrInvalidAmount
	}

	pot, err := s.potRepo.GetByID(ctx, potID)
	if err != nil {
		return err
	}
	if pot.UserID != userID {
		return domain.ErrPotNotFound
	}
	if pot.Status != domain.PotStatusActive {
		return domain.ErrPotClosed
	}

	// The funding account must belong to the pot owner.
	if err := s.requireOwnedAccount(ctx, userID, accountID); err != nil {
		return err
	}

	key := correlationID
	if key == "" {
		key = uuid.New().String()
	}
	if err := s.ledgerClient.BookTransfer(ctx, accountID, potID, amount, pot.Currency, "pot-deposit:"+potID.String()+":"+key, "Deposit to pot "+pot.Name); err != nil {
		if errors.Is(err, clients.ErrInsufficientFunds) {
			return ErrInsufficientFunds
		}
		return fmt.Errorf("booking pot deposit: %w", err)
	}

	if err := pot.Deposit(amount); err != nil {
		return err
	}
	if err := s.potRepo.Update(ctx, pot); err != nil {
		return fmt.Errorf("persisting pot deposit: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotDeposit, map[string]interface{}{
		"pot_id":   potID.String(),
		"user_id":  userID.String(),
		"amount":   amount,
		"currency": pot.Currency,
		"balance":  pot.CurrentAmount,
	}, potID.String(), correlationID)

	return nil
}

// Withdraw moves money from the pot back to the caller's account by reversing
// the double-entry: DEBIT the pot's ledger account, CREDIT the account.
func (s *PotService) Withdraw(ctx context.Context, potID, userID, accountID uuid.UUID, amount int64, correlationID string) error {
	s.logger.Info().Str("pot_id", potID.String()).Int64("amount", amount).Msg("withdrawing from pot")

	if amount <= 0 {
		return domain.ErrInvalidAmount
	}

	pot, err := s.potRepo.GetByID(ctx, potID)
	if err != nil {
		return err
	}
	if pot.UserID != userID {
		return domain.ErrPotNotFound
	}
	if pot.Status != domain.PotStatusActive {
		return domain.ErrPotClosed
	}
	if pot.CurrentAmount < amount {
		return domain.ErrInsufficientFunds
	}

	if err := s.requireOwnedAccount(ctx, userID, accountID); err != nil {
		return err
	}

	key := correlationID
	if key == "" {
		key = uuid.New().String()
	}
	// Source is the pot's ledger account; the ledger re-checks the pot's real
	// booked balance atomically.
	if err := s.ledgerClient.BookTransfer(ctx, potID, accountID, amount, pot.Currency, "pot-withdraw:"+potID.String()+":"+key, "Withdraw from pot "+pot.Name); err != nil {
		if errors.Is(err, clients.ErrInsufficientFunds) {
			return ErrInsufficientFunds
		}
		return fmt.Errorf("booking pot withdrawal: %w", err)
	}

	if err := pot.Withdraw(amount); err != nil {
		return err
	}
	if err := s.potRepo.Update(ctx, pot); err != nil {
		return fmt.Errorf("persisting pot withdrawal: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotWithdraw, map[string]interface{}{
		"pot_id":   potID.String(),
		"user_id":  userID.String(),
		"amount":   amount,
		"currency": pot.Currency,
		"balance":  pot.CurrentAmount,
	}, potID.String(), correlationID)

	return nil
}

// requireOwnedAccount verifies an account belongs to the given user via the
// account service (internal call).
func (s *PotService) requireOwnedAccount(ctx context.Context, userID, accountID uuid.UUID) error {
	info, err := s.accountClient.GetAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if info.UserID != userID {
		return ErrAccountNotOwned
	}
	return nil
}

// SetRoundUp toggles automatic round-ups into this pot. When enabled, every
// captured card payment sweeps its spare change (rounded to the next pound)
// into the pot via the roundups consumer.
func (s *PotService) SetRoundUp(ctx context.Context, potID, userID uuid.UUID, enabled bool) (*domain.Pot, error) {
	s.logger.Info().Str("pot_id", potID.String()).Bool("enabled", enabled).Msg("setting round-up")

	pot, err := s.potRepo.GetByID(ctx, potID)
	if err != nil {
		return nil, err
	}
	if pot.UserID != userID {
		return nil, domain.ErrPotNotFound
	}
	if pot.Status != domain.PotStatusActive {
		return nil, domain.ErrPotClosed
	}

	pot.RoundUpEnabled = enabled
	pot.UpdatedAt = time.Now().UTC()
	if err := s.potRepo.Update(ctx, pot); err != nil {
		return nil, fmt.Errorf("persisting round-up toggle: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotUpdated, map[string]interface{}{
		"pot_id":           potID.String(),
		"user_id":          userID.String(),
		"round_up_enabled": enabled,
	}, potID.String(), "")
	return pot, nil
}

func (s *PotService) RenamePot(ctx context.Context, potID, userID uuid.UUID, newName string) error {
	s.logger.Info().Str("pot_id", potID.String()).Str("new_name", newName).Msg("renaming pot")

	if newName == "" {
		return fmt.Errorf("pot name cannot be empty")
	}

	pot, err := s.potRepo.GetByID(ctx, potID)
	if err != nil {
		return err
	}
	if pot.UserID != userID {
		return domain.ErrPotNotFound
	}
	if pot.Status == domain.PotStatusClosed {
		return fmt.Errorf("%w: cannot rename a closed pot", domain.ErrPotClosed)
	}

	oldName := pot.Name
	pot.Name = newName
	pot.UpdatedAt = time.Now().UTC()

	if err := s.potRepo.Update(ctx, pot); err != nil {
		return fmt.Errorf("persisting pot rename: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotRenamed, map[string]interface{}{
		"pot_id":   potID.String(),
		"user_id":  userID.String(),
		"old_name": oldName,
		"new_name": newName,
	}, potID.String(), "")

	return nil
}

func (s *PotService) DeletePot(ctx context.Context, potID, userID uuid.UUID) error {
	s.logger.Info().Str("pot_id", potID.String()).Msg("deleting pot")

	pot, err := s.potRepo.GetByID(ctx, potID)
	if err != nil {
		return err
	}
	if pot.UserID != userID {
		return domain.ErrPotNotFound
	}

	if err := pot.Close(); err != nil {
		return err
	}
	if pot.CurrentAmount != 0 {
		return domain.ErrBalanceNonZero
	}

	if err := s.potRepo.Delete(ctx, potID); err != nil {
		return fmt.Errorf("deleting pot: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotDeleted, map[string]interface{}{
		"pot_id":  potID.String(),
		"user_id": userID.String(),
		"name":    pot.Name,
	}, potID.String(), "")

	return nil
}
