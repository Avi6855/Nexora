package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/card-service/internal/domain"
)

type CardRepository interface {
	Create(ctx context.Context, card *domain.Card) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Card, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Card, error)
	Update(ctx context.Context, card *domain.Card) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type cassandraCardRepository struct {
	session *gocql.Session
}

func NewCassandraCardRepository(session *gocql.Session) CardRepository {
	return &cassandraCardRepository{session: session}
}

func (r *cassandraCardRepository) Create(ctx context.Context, card *domain.Card) error {
	query := `INSERT INTO cards (card_id, user_id, account_id, card_number_last4, card_type, status, spending_limit, daily_limit, monthly_limit, currency, online_enabled, atm_enabled, gambling_block_enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if err := r.session.Query(query,
		gocql.UUID(card.CardID), gocql.UUID(card.UserID), gocql.UUID(card.AccountID), card.CardNumberLast4,
		string(card.CardType), string(card.Status), card.SpendingLimit,
		card.DailyLimit, card.MonthlyLimit, card.Currency,
		card.SpendingControls.OnlineEnabled, card.SpendingControls.ATMEnabled, card.SpendingControls.GamblingBlockEnabled,
		card.CreatedAt, card.UpdatedAt,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("inserting card: %w", err)
	}

	indexQuery := `INSERT INTO cards_by_user (user_id, card_id, status, card_type, created_at)
		VALUES (?, ?, ?, ?, ?)`

	if err := r.session.Query(indexQuery,
		gocql.UUID(card.UserID), gocql.UUID(card.CardID), string(card.Status),
		string(card.CardType), card.CreatedAt,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("inserting card index: %w", err)
	}

	return nil
}

func (r *cassandraCardRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Card, error) {
	var card domain.Card
	var cardID, userID, accountID gocql.UUID
	var cardType, status, currency string
	var online, atm, gamblingBlock *bool

	query := `SELECT card_id, user_id, account_id, card_number_last4, card_type, status, spending_limit, daily_limit, monthly_limit, currency, online_enabled, atm_enabled, gambling_block_enabled, created_at, updated_at
		FROM cards WHERE card_id = ?`

	err := r.session.Query(query, gocql.UUID(id)).WithContext(ctx).Scan(
		&cardID, &userID, &accountID, &card.CardNumberLast4,
		&cardType, &status, &card.SpendingLimit, &card.DailyLimit,
		&card.MonthlyLimit, &currency,
		&online, &atm, &gamblingBlock,
		&card.CreatedAt, &card.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("%w", domain.ErrCardNotFound)
	}
	if err != nil {
		return nil, err
	}

	card.CardID = uuid.UUID(cardID)
	card.UserID = uuid.UUID(userID)
	card.AccountID = uuid.UUID(accountID)
	card.CardType = domain.CardType(cardType)
	card.Status = domain.CardStatus(status)
	card.Currency = currency
	card.SpendingControls = domain.SpendingControls{
		DailyLimit:           card.DailyLimit,
		MonthlyLimit:         card.MonthlyLimit,
		OnlineEnabled:        online,
		ATMEnabled:           atm,
		GamblingBlockEnabled: gamblingBlock,
	}
	return &card, nil
}

func (r *cassandraCardRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Card, error) {
	cards := make([]*domain.Card, 0)

	// Query cards_by_user lookup table to get card_ids, then fetch each card
	iter := r.session.Query(
		`SELECT card_id FROM cards_by_user WHERE user_id = ?`, gocql.UUID(userID),
	).WithContext(ctx).Iter()
	defer iter.Close()

	var cardID gocql.UUID
	for iter.Scan(&cardID) {
		card, err := r.GetByID(ctx, uuid.UUID(cardID))
		if err != nil {
			continue
		}
		cards = append(cards, card)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return cards, nil
}

func (r *cassandraCardRepository) Update(ctx context.Context, card *domain.Card) error {
	query := `UPDATE cards SET status = ?, spending_limit = ?, daily_limit = ?, monthly_limit = ?, online_enabled = ?, atm_enabled = ?, gambling_block_enabled = ?, updated_at = ? WHERE card_id = ?`
	if err := r.session.Query(query,
		string(card.Status), card.SpendingLimit, card.DailyLimit, card.MonthlyLimit,
		card.SpendingControls.OnlineEnabled, card.SpendingControls.ATMEnabled, card.SpendingControls.GamblingBlockEnabled,
		card.UpdatedAt, gocql.UUID(card.CardID),
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("updating card: %w", err)
	}

	indexQuery := `UPDATE cards_by_user SET status = ? WHERE user_id = ? AND card_id = ?`
	return r.session.Query(indexQuery,
		string(card.Status), gocql.UUID(card.UserID), gocql.UUID(card.CardID),
	).WithContext(ctx).Exec()
}

func (r *cassandraCardRepository) Delete(ctx context.Context, id uuid.UUID) error {
	card, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := r.session.Query(`DELETE FROM cards WHERE card_id = ?`, gocql.UUID(id)).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("deleting card: %w", err)
	}

	if err := r.session.Query(`DELETE FROM cards_by_user WHERE user_id = ? AND card_id = ?`,
		gocql.UUID(card.UserID), gocql.UUID(card.CardID)).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("deleting card index: %w", err)
	}

	return nil
}
