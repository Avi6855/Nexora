package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/money"
	"github.com/nexora/nexora/services/account-service/internal/clients"
	"github.com/nexora/nexora/services/account-service/internal/domain"
	"github.com/nexora/nexora/services/account-service/internal/events"
	"github.com/nexora/nexora/services/account-service/internal/repository"
)

type AccountService struct {
	accountRepo repository.AccountRepository
	producer    *events.KafkaProducer
	ledger      *clients.LedgerClient
	logger      zerolog.Logger
}

func NewAccountService(accountRepo repository.AccountRepository, producer *events.KafkaProducer, logger zerolog.Logger) *AccountService {
	return &AccountService{
		accountRepo: accountRepo,
		producer:    producer,
		ledger:      clients.NewLedgerClient(logger),
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

	s.overlayLedgerBalance(ctx, account)
	return account, nil
}

func (s *AccountService) GetAccountsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Account, error) {
	s.logger.Info().Str("user_id", userID.String()).Msg("getting user accounts")

	accounts, err := s.accountRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Balances come from the ledger of record, never the row counters — the
	// app's home totals must reflect a card capture that booked seconds ago.
	for _, account := range accounts {
		s.overlayLedgerBalance(ctx, account)
	}
	return accounts, nil
}

// overlayLedgerBalance overwrites the account's money fields with the live
// ledger breakdown (cleared/reserved/available). Failures keep row values so a
// ledger hiccup never blanks the list.
func (s *AccountService) overlayLedgerBalance(ctx context.Context, account *domain.Account) {
	b, err := s.ledger.Balance(ctx, account.AccountID)
	if err != nil {
		s.logger.Warn().Err(err).Str("account_id", account.AccountID.String()).Msg("ledger balance unavailable, using row values")
		return
	}
	account.AvailableBalance = money.Money{Amount: b.Available, Currency: account.Currency}
	account.CurrentBalance = money.Money{Amount: b.Balance, Currency: account.Currency}
	account.ReservedBalance = money.Money{Amount: b.Pending, Currency: account.Currency}
}

// GetBalance answers from the ledger — the system of record — never from a
// locally cached counter. The ledger computes cleared (balance), reserved
// (pending) and available money from the actual double-entry rows, so a card
// capture or payment that books seconds ago is reflected instantly. The
// account row is only used to resolve existence + currency.
func (s *AccountService) GetBalance(ctx context.Context, id uuid.UUID) (money.Money, money.Money, money.Money, error) {
	s.logger.Info().Str("account_id", id.String()).Msg("getting balance from ledger of record")

	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return money.Money{}, money.Money{}, money.Money{}, err
	}

	b, err := s.ledger.Balance(ctx, id)
	if err != nil {
		return money.Money{}, money.Money{}, money.Money{}, fmt.Errorf("ledger of record unavailable: %w", err)
	}

	toMoney := func(amount int64) money.Money {
		return money.Money{Amount: amount, Currency: account.Currency}
	}
	return toMoney(b.Available), toMoney(b.Balance), toMoney(b.Pending), nil
}

func (s *AccountService) Debit(ctx context.Context, id uuid.UUID, amount money.Money) error {
	s.logger.Info().Str("account_id", id.String()).Str("amount", amount.String()).Msg("debiting account")

	// Emergency lockdown blocks all money-OUT operations at the source of
	// truth. Credits (money in) are never blocked.
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if account.LockdownEnabled {
		return domain.ErrAccountLocked
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

// SetLockdown toggles the emergency "freeze money out" switch on an account
// owned by the caller. Returns the updated lockdown state.
func (s *AccountService) SetLockdown(ctx context.Context, userID, accountID uuid.UUID, enabled bool) (*domain.Account, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.UserID != userID {
		return nil, fmt.Errorf("account does not belong to caller")
	}

	account.LockdownEnabled = enabled
	account.UpdatedAt = time.Now().UTC()
	if err := s.accountRepo.Update(ctx, account); err != nil {
		return nil, fmt.Errorf("updating lockdown: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "account.lockdown_changed", account)
	}

	s.logger.Info().
		Str("account_id", accountID.String()).
		Bool("lockdown", enabled).
		Msg("account lockdown toggled")
	return account, nil
}

// IsLocked reports whether an account is in emergency lockdown. Internal
// callers (ledger/card/transfer enforcement) use this for fast decisions;
// account rows are also denormalised so enforcement works when this call
// fails open-closed policy is set by the caller.
func (s *AccountService) IsLocked(ctx context.Context, accountID uuid.UUID) (bool, error) {
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		return false, err
	}
	return account.LockdownEnabled, nil
}
