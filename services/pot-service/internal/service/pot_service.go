package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/pot-service/internal/domain"
	"github.com/nexora/nexora/services/pot-service/internal/events"
	"github.com/nexora/nexora/services/pot-service/internal/repository"
)

type LedgerEntryRequest struct {
	AccountID string            `json:"account_id"`
	EntryType string            `json:"entry_type"`
	Amount    int64             `json:"amount"`
	Currency  string            `json:"currency"`
	Reference string            `json:"reference"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type LedgerEntryResponse struct {
	EntryID     string `json:"entry_id"`
	AccountID   string `json:"account_id"`
	EntryType   string `json:"entry_type"`
	Amount      int64  `json:"amount"`
	BalanceAfter int64 `json:"balance_after"`
	Currency    string `json:"currency"`
	Reference   string `json:"reference"`
	CreatedAt   string `json:"created_at"`
}

type PotService struct {
	potRepo      repository.PotRepository
	publisher    events.EventPublisher
	ledgerBaseURL string
	httpClient   *http.Client
	logger       zerolog.Logger
}

func NewPotService(potRepo repository.PotRepository, publisher events.EventPublisher, ledgerBaseURL string, logger zerolog.Logger) *PotService {
	return &PotService{
		potRepo:      potRepo,
		publisher:    publisher,
		ledgerBaseURL: ledgerBaseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
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

func (s *PotService) GetPot(ctx context.Context, id uuid.UUID) (*domain.Pot, error) {
	return s.potRepo.GetByID(ctx, id)
}

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

	if err := pot.Deposit(amount); err != nil {
		return err
	}

	ledgerReq := LedgerEntryRequest{
		AccountID: accountID.String(),
		EntryType: "CREDIT",
		Amount:    amount,
		Currency:  pot.Currency,
		Reference: fmt.Sprintf("pot-deposit:%s", potID.String()),
		Metadata: map[string]string{
			"pot_id": potID.String(),
			"user_id": userID.String(),
		},
	}

	_, err = s.createLedgerEntry(ctx, ledgerReq)
	if err != nil {
		return fmt.Errorf("creating ledger entry for deposit: %w", err)
	}

	if err := s.potRepo.Update(ctx, pot); err != nil {
		return fmt.Errorf("persisting pot deposit: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotDeposit, map[string]interface{}{
		"pot_id":    potID.String(),
		"user_id":   userID.String(),
		"amount":    amount,
		"currency":  pot.Currency,
		"balance":   pot.CurrentAmount,
	}, potID.String(), correlationID)

	return nil
}

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

	if err := pot.Withdraw(amount); err != nil {
		return err
	}

	ledgerReq := LedgerEntryRequest{
		AccountID: accountID.String(),
		EntryType: "DEBIT",
		Amount:    amount,
		Currency:  pot.Currency,
		Reference: fmt.Sprintf("pot-withdraw:%s", potID.String()),
		Metadata: map[string]string{
			"pot_id": potID.String(),
			"user_id": userID.String(),
		},
	}

	_, err = s.createLedgerEntry(ctx, ledgerReq)
	if err != nil {
		return fmt.Errorf("creating ledger entry for withdrawal: %w", err)
	}

	if err := s.potRepo.Update(ctx, pot); err != nil {
		return fmt.Errorf("persisting pot withdrawal: %w", err)
	}

	_ = s.publisher.PublishPotEvent(ctx, events.EventTypePotWithdraw, map[string]interface{}{
		"pot_id":    potID.String(),
		"user_id":   userID.String(),
		"amount":    amount,
		"currency":  pot.Currency,
		"balance":   pot.CurrentAmount,
	}, potID.String(), correlationID)

	return nil
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

func (s *PotService) createLedgerEntry(ctx context.Context, req LedgerEntryRequest) (*LedgerEntryResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling ledger request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/ledger/entries", s.ledgerBaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling ledger service: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading ledger response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ledger service returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var entryResp LedgerEntryResponse
	if err := json.Unmarshal(respBody, &entryResp); err != nil {
		return nil, fmt.Errorf("unmarshaling ledger response: %w", err)
	}

	return &entryResp, nil
}
