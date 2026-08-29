package service

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/money"
	"github.com/nexora/nexora/services/account-service/internal/domain"
	"github.com/nexora/nexora/services/account-service/internal/events"
	"github.com/nexora/nexora/services/account-service/internal/repository"
)

type AccountService struct {
	accountRepo repository.AccountRepository
	producer    *events.KafkaProducer
	logger      zerolog.Logger
}

func NewAccountService(accountRepo repository.AccountRepository, producer *events.KafkaProducer, logger zerolog.Logger) *AccountService {
	return &AccountService{
		accountRepo: accountRepo,
		producer:    producer,
		logger:      logger,
	}
}

func (s *AccountService) CreateAccount(ctx context.Context, userID uuid.UUID, accountType domain.AccountType, currency string) (*domain.Account, error) {
	s.logger.Info().Str("user_id", userID.String()).Str("type", string(accountType)).Msg("creating account")

	account, err := domain.NewAccount(userID, accountType, currency)
	if err != nil {
		return nil, fmt.Errorf("creating account: %w", err)
	}

	if err := s.accountRepo.Create(ctx, account); err != nil {
		return nil, fmt.Errorf("storing account: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "account.created", account)
	}

	s.logger.Info().Str("account_id", account.AccountID.String()).Msg("account created")
	return account, nil
}

func (s *AccountService) GetAccount(ctx context.Context, id uuid.UUID) (*domain.Account, error) {
	s.logger.Info().Str("account_id", id.String()).Msg("getting account")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	return account, nil
}

func (s *AccountService) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error) {
	s.logger.Info().Str("user_id", userID.String()).Msg("getting user accounts")

	accounts, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return accounts, nil
}

func (s *AccountService) GetBalance(ctx context.Context, id uuid.UUID) (money.Money, money.Money, money.Money, error) {
	s.logger.Info().Str("account_id", id.String()).Msg("getting balance")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return money.Money{}, money.Money{}, money.Money{}, err
	}

	return account.AvailableBalance, account.CurrentBalance, account.ReservedBalance, nil
}

func (s *AccountService) Debit(ctx context.Context, id uuid.UUID, amount money.Money) error {
	s.logger.Info().Str("account_id", id.String()).Str("amount", amount.String()).Msg("debiting account")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if account.Currency != amount.Currency {
		return fmt.Errorf("currency mismatch: account has %s, debit requested %s", account.Currency, amount.Currency)
	}

	if lt, _ := account.AvailableBalance.IsLessThan(amount); lt {
		return fmt.Errorf("insufficient funds: available %s, requested %s", account.AvailableBalance.String(), amount.String())
	}

	newAvail, err := account.AvailableBalance.Subtract(amount)
	if err != nil {
		return err
	}
	newCurrent, err := account.CurrentBalance.Subtract(amount)
	if err != nil {
		return err
	}

	account.AvailableBalance = newAvail
	account.CurrentBalance = newCurrent
	account.UpdatedAt = account.UpdatedAt

	return s.accountRepo.Update(ctx, account)
}

func (s *AccountService) Credit(ctx context.Context, id uuid.UUID, amount money.Money) error {
	s.logger.Info().Str("account_id", id.String()).Str("amount", amount.String()).Msg("crediting account")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if account.Currency != amount.Currency {
		return fmt.Errorf("currency mismatch")
	}

	newAvail, err := account.AvailableBalance.Add(amount)
	if err != nil {
		return err
	}
	newCurrent, err := account.CurrentBalance.Add(amount)
	if err != nil {
		return err
	}

	account.AvailableBalance = newAvail
	account.CurrentBalance = newCurrent

	return s.accountRepo.Update(ctx, account)
}

func (s *AccountService) Reserve(ctx context.Context, id uuid.UUID, amount money.Money) error {
	s.logger.Info().Str("account_id", id.String()).Str("amount", amount.String()).Msg("reserving funds")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if lt, _ := account.AvailableBalance.IsLessThan(amount); lt {
		return fmt.Errorf("insufficient available balance")
	}

	newAvail, err := account.AvailableBalance.Subtract(amount)
	if err != nil {
		return err
	}
	newRes, err := account.ReservedBalance.Add(amount)
	if err != nil {
		return err
	}

	account.AvailableBalance = newAvail
	account.ReservedBalance = newRes

	return s.accountRepo.Update(ctx, account)
}

func (s *AccountService) Release(ctx context.Context, id uuid.UUID, amount money.Money) error {
	s.logger.Info().Str("account_id", id.String()).Str("amount", amount.String()).Msg("releasing reserved funds")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}

	newAvail, err := account.AvailableBalance.Add(amount)
	if err != nil {
		return err
	}
	newRes, err := account.ReservedBalance.Subtract(amount)
	if err != nil {
		return err
	}

	account.AvailableBalance = newAvail
	account.ReservedBalance = newRes

	return s.accountRepo.Update(ctx, account)
}
