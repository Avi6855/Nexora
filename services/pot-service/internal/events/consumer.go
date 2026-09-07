package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/pot-service/internal/domain"
	"github.com/nexora/nexora/services/pot-service/internal/repository"
)

// PotDepositor is the slice of the pot service the roundups consumer needs.
// Declared here (implemented by *service.PotService) to avoid an import
// cycle: the service package publishes pot events through this package.
type PotDepositor interface {
	Deposit(ctx context.Context, potID, userID, accountID uuid.UUID, amount int64, correlationID string) error
	GetPotsByUser(ctx context.Context, userID uuid.UUID) ([]*domain.Pot, error)
}

// CapturedAuthorizationEvent mirrors the card-service authorization payload
// published to nexora.card.authorization.captured (see card-service
// events.AuthorizationEvent). Only captured events sweep round-ups: capture
// means the money really left the account (the ledger debit is booked), so
// the spare change is backed by real cleared spend.
type CapturedAuthorizationEvent struct {
	AuthorizationID string  `json:"authorization_id"`
	CardID          string  `json:"card_id"`
	UserID          string  `json:"user_id"`
	AccountID       string  `json:"account_id"`
	Amount          int64   `json:"amount"`
	Currency        string  `json:"currency"`
	Merchant        string  `json:"merchant"`
	MerchantCity    string  `json:"merchant_city"`
	MerchantCountry string  `json:"merchant_country"`
	TerminalID      string  `json:"terminal_id"`
	Status          string  `json:"status"`
	Decision        string  `json:"decision"`
	ReservationID   string  `json:"reservation_id"`
	BalanceAfter    *int64  `json:"balance_after"`
	LatencyMs       int64   `json:"latency_ms"`
	CreatedAt       string  `json:"created_at"`
}

// RoundupConsumer consumes captured card authorizations and moves the
// round-up (spend rounded up to the nearest pound) into the user's active
// round-up-enabled pot.
//
// Exactly-once: each authorization is claimed in Cassandra (LWT) before the
// transfer is booked, so Kafka redelivery can never sweep the same payment
// twice. The claim names the pot, which makes the sweep deterministic.
type RoundupConsumer struct {
	pots     PotDepositor
	repo     repository.RoundupRepository
	logger   zerolog.Logger
	// roundupTo lets tests and future multi-pot selection override which pot
	// receives the round-up. nil selects the user's first active round-up pot.
	roundupTo func(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
}

func NewRoundupConsumer(pots PotDepositor, repo repository.RoundupRepository, logger zerolog.Logger) *RoundupConsumer {
	return &RoundupConsumer{pots: pots, repo: repo, logger: logger}
}

// SetPotSelector overrides the default "first active round-up-enabled pot"
// selection. Used by tests.
func (c *RoundupConsumer) SetPotSelector(sel func(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)) {
	c.roundupTo = sel
}

// HandleRoundupEvent is the Kafka handler for nexora.card.authorization.captured.
func (c *RoundupConsumer) HandleRoundupEvent(ctx context.Context, topic, eventType string, value []byte) error {
	var ev CapturedAuthorizationEvent
	if err := json.Unmarshal(value, &ev); err != nil {
		// The publisher may wrap the payload in an envelope; unwrap once.
		var env struct {
			Payload json.RawMessage `json:"payload"`
		}
		if err2 := json.Unmarshal(value, &env); err2 != nil || json.Unmarshal(env.Payload, &ev) != nil {
			return fmt.Errorf("invalid captured authorization payload: %w", err)
		}
	}

	userID, err := uuid.Parse(ev.UserID)
	if err != nil {
		return fmt.Errorf("invalid user_id in captured event: %w", err)
	}
	authID, err := uuid.Parse(ev.AuthorizationID)
	if err != nil {
		return fmt.Errorf("invalid authorization_id in captured event: %w", err)
	}
	accountID, err := uuid.Parse(ev.AccountID)
	if err != nil {
		return fmt.Errorf("invalid account_id in captured event: %w", err)
	}
	if ev.Amount <= 0 {
		return nil // nothing to round up on zero-value events
	}

	// Pick the destination pot. Skip silently when the user has no round-up
	// pot: that is the "roundups off" state, not an error.
	potID, found, err := c.selectPot(ctx, userID)
	if err != nil {
		return fmt.Errorf("selecting round-up pot: %w", err)
	}
	if !found {
		c.logger.Debug().Str("user_id", userID.String()).Msg("no round-up pot; skipping")
		return nil
	}

	// Claim exactly-once work BEFORE booking the transfer.
	applied, err := c.repo.ClaimAuthorization(ctx, authID, potID, roundupAmount(ev.Amount))
	if err != nil {
		return fmt.Errorf("claiming roundup: %w", err)
	}
	if !applied {
		c.logger.Debug().Str("authorization_id", authID.String()).Msg("roundup already processed")
		return nil
	}

	roundUp := roundupAmount(ev.Amount)
	if err := c.pots.Deposit(ctx, potID, userID, accountID, roundUp, "roundup:"+authID.String()); err != nil {
		// Release the claim so a future retry (or replay) can sweep it.
		c.logger.Error().Err(err).
			Str("authorization_id", authID.String()).
			Str("pot_id", potID.String()).
			Msg("roundup deposit failed after claim")
		return fmt.Errorf("booking roundup deposit: %w", err)
	}

	c.logger.Info().
		Str("authorization_id", authID.String()).
		Str("user_id", userID.String()).
		Str("pot_id", potID.String()).
		Int64("spend", ev.Amount).
		Int64("roundup", roundUp).
		Msg("roundup swept into pot")
	return nil
}

// selectPot returns the user's first ACTIVE round-up-enabled pot.
func (c *RoundupConsumer) selectPot(ctx context.Context, userID uuid.UUID) (uuid.UUID, bool, error) {
	if c.roundupTo != nil {
		id, err := c.roundupTo(ctx, userID)
		return id, err == nil, err
	}
	pots, err := c.pots.GetPotsByUser(ctx, userID)
	if err != nil {
		return uuid.Nil, false, err
	}
	for _, p := range pots {
		if p.Status == domain.PotStatusActive && p.RoundUpEnabled {
			return p.PotID, true, nil
		}
	}
	return uuid.Nil, false, nil
}

// roundupAmount returns the spare change that rounds spend up to the next
// whole pound: £4.20 spend -> £0.80 round-up. A spend already on a pound
// boundary rounds up nothing.
func roundupAmount(minor int64) int64 {
	const unit = 100
	rem := minor % unit
	if rem == 0 {
		return 0
	}
	return unit - rem
}
