package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/card-service/internal/domain"
)

type CardAuthorizationRepository interface {
	Create(ctx context.Context, auth *domain.CardAuthorization) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.CardAuthorization, error)
	GetByCardID(ctx context.Context, cardID uuid.UUID, limit int) ([]*domain.CardAuthorization, error)
	UpdateCapture(ctx context.Context, id uuid.UUID) error
	UpdateVoid(ctx context.Context, id uuid.UUID) error
}

type cassandraAuthorizationRepository struct {
	session *gocql.Session
}

func NewCassandraAuthorizationRepository(session *gocql.Session) CardAuthorizationRepository {
	return &cassandraAuthorizationRepository{session: session}
}

func toUUID(id uuid.UUID) gocql.UUID {
	return gocql.UUID(id)
}

func (r *cassandraAuthorizationRepository) Create(ctx context.Context, auth *domain.CardAuthorization) error {
	query := `INSERT INTO card_authorizations (
		authorization_id, card_id, user_id, account_id, amount, currency,
		merchant, merchant_category, merchant_city, merchant_country,
		latitude, longitude, terminal_id,
		decision, decline_reason, risk_score, risk_action, risk_level, risk_reasons,
		reservation_id, status, latency_ms,
		created_at, updated_at, captured_at, voided_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var reservationID interface{}
	if auth.ReservationID != uuid.Nil {
		reservationID = toUUID(auth.ReservationID)
	}

	return r.session.Query(query,
		toUUID(auth.AuthorizationID),
		toUUID(auth.CardID),
		toUUID(auth.UserID),
		toUUID(auth.AccountID),
		auth.Amount,
		auth.Currency,
		auth.Merchant,
		auth.MerchantCategory,
		auth.MerchantCity,
		auth.MerchantCountry,
		auth.Latitude,
		auth.Longitude,
		auth.TerminalID,
		string(auth.Decision),
		auth.DeclineReason,
		auth.RiskScore,
		auth.RiskAction,
		auth.RiskLevel,
		auth.RiskReasons,
		reservationID,
		string(auth.Status),
		auth.LatencyMs,
		auth.CreatedAt,
		auth.UpdatedAt,
		auth.CapturedAt,
		auth.VoidedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraAuthorizationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.CardAuthorization, error) {
	var auth domain.CardAuthorization
	var authorizationID, cardID, userID, accountID gocql.UUID
	var reservationID gocql.UUID
	var decision, status, declineReason, riskAction, riskLevel, riskReasons string
	var capturedAt, voidedAt *time.Time

	query := `SELECT authorization_id, card_id, user_id, account_id, amount, currency,
		merchant, merchant_category, merchant_city, merchant_country,
		latitude, longitude, terminal_id,
		decision, decline_reason, risk_score, risk_action, risk_level, risk_reasons,
		reservation_id, status, latency_ms,
		created_at, updated_at, captured_at, voided_at
		FROM card_authorizations WHERE authorization_id = ?`

	err := r.session.Query(query, toUUID(id)).WithContext(ctx).Scan(
		&authorizationID, &cardID, &userID, &accountID, &auth.Amount, &auth.Currency,
		&auth.Merchant, &auth.MerchantCategory, &auth.MerchantCity, &auth.MerchantCountry,
		&auth.Latitude, &auth.Longitude, &auth.TerminalID,
		&decision, &declineReason, &auth.RiskScore, &riskAction, &riskLevel, &riskReasons,
		&reservationID, &status, &auth.LatencyMs,
		&auth.CreatedAt, &auth.UpdatedAt, &capturedAt, &voidedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, domain.ErrAuthNotFound
	}
	if err != nil {
		return nil, err
	}

	auth.AuthorizationID = uuid.UUID(authorizationID)
	auth.CardID = uuid.UUID(cardID)
	auth.UserID = uuid.UUID(userID)
	auth.AccountID = uuid.UUID(accountID)
	auth.Decision = domain.AuthorizationDecision(decision)
	auth.DeclineReason = declineReason
	auth.RiskAction = riskAction
	auth.RiskLevel = riskLevel
	auth.RiskReasons = riskReasons
	auth.Status = domain.AuthorizationStatus(status)
	auth.CapturedAt = capturedAt
	auth.VoidedAt = voidedAt
	if reservationID != gocql.UUID([16]byte{}) {
		auth.ReservationID = uuid.UUID(reservationID)
	}
	return &auth, nil
}

func (r *cassandraAuthorizationRepository) GetByCardID(ctx context.Context, cardID uuid.UUID, limit int) ([]*domain.CardAuthorization, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT authorization_id, card_id, user_id, account_id, amount, currency,
		merchant, merchant_category, merchant_city, merchant_country,
		latitude, longitude, terminal_id,
		decision, decline_reason, risk_score, risk_action, risk_level, risk_reasons,
		reservation_id, status, latency_ms,
		created_at, updated_at, captured_at, voided_at
		FROM card_authorizations WHERE card_id = ? LIMIT ?`

	iter := r.session.Query(query, toUUID(cardID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	var auths []*domain.CardAuthorization
	for {
		var auth domain.CardAuthorization
		var authorizationID, cardIDCol, userID, accountID gocql.UUID
		var reservationID gocql.UUID
		var decision, status, declineReason, riskAction, riskLevel, riskReasons string
		var capturedAt, voidedAt *time.Time

		if !iter.Scan(
			&authorizationID, &cardIDCol, &userID, &accountID, &auth.Amount, &auth.Currency,
			&auth.Merchant, &auth.MerchantCategory, &auth.MerchantCity, &auth.MerchantCountry,
			&auth.Latitude, &auth.Longitude, &auth.TerminalID,
			&decision, &declineReason, &auth.RiskScore, &riskAction, &riskLevel, &riskReasons,
			&reservationID, &status, &auth.LatencyMs,
			&auth.CreatedAt, &auth.UpdatedAt, &capturedAt, &voidedAt,
		) {
			break
		}
		auth.AuthorizationID = uuid.UUID(authorizationID)
		auth.CardID = uuid.UUID(cardIDCol)
		auth.UserID = uuid.UUID(userID)
		auth.AccountID = uuid.UUID(accountID)
		auth.Decision = domain.AuthorizationDecision(decision)
		auth.DeclineReason = declineReason
		auth.RiskAction = riskAction
		auth.RiskLevel = riskLevel
		auth.RiskReasons = riskReasons
		auth.Status = domain.AuthorizationStatus(status)
		auth.CapturedAt = capturedAt
		auth.VoidedAt = voidedAt
		if reservationID != gocql.UUID([16]byte{}) {
			auth.ReservationID = uuid.UUID(reservationID)
		}
		auths = append(auths, &auth)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return auths, nil
}

func (r *cassandraAuthorizationRepository) UpdateCapture(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE card_authorizations SET status = 'CAPTURED', captured_at = ?, updated_at = ? WHERE authorization_id = ? IF status = 'APPROVED'`
	applied, err := r.session.Query(query, now, now, toUUID(id)).WithContext(ctx).ScanCAS()
	if err != nil {
		return err
	}
	if !applied {
		return domain.ErrAuthNotCapturable
	}
	return nil
}

func (r *cassandraAuthorizationRepository) UpdateVoid(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `UPDATE card_authorizations SET status = 'VOIDED', voided_at = ?, updated_at = ? WHERE authorization_id = ? IF status = 'APPROVED'`
	applied, err := r.session.Query(query, now, now, toUUID(id)).WithContext(ctx).ScanCAS()
	if err != nil {
		return err
	}
	if !applied {
		return domain.ErrAuthNotVoidable
	}
	return nil
}
