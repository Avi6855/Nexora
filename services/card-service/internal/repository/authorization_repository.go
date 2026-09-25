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

	// UpdateChallengeSatisfied completes a step-up: the authorization moves
	// CHALLENGED -> APPROVED and takes the funds hold that was deliberately not
	// placed while the cardholder was still authenticating.
	UpdateChallengeSatisfied(ctx context.Context, id uuid.UUID, reservationID uuid.UUID) error
	// UpdateChallengeDeclined closes a step-up that failed or expired without
	// holding any funds.
	UpdateChallengeDeclined(ctx context.Context, id uuid.UUID, reason string) error
	// SaveRefund writes the refunded total and status back with an optimistic
	// concurrency check on the version the caller read (status + updated_at),
	// so two concurrent partial refunds cannot exceed the captured amount. It is
	// also the compensation path: a claim whose ledger credit never landed is
	// written back through the same method so the refund can be retried.
	SaveRefund(ctx context.Context, auth *domain.CardAuthorization, expectedStatus domain.AuthorizationStatus, expectedUpdatedAt time.Time) (bool, error)
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
		reservation_id, challenge_id, sca_exemption, status, latency_ms,
		created_at, updated_at, captured_at, voided_at, refunded_amount, refunded_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var reservationID interface{}
	if auth.ReservationID != uuid.Nil {
		reservationID = toUUID(auth.ReservationID)
	}
	var challengeID interface{}
	if auth.ChallengeID != uuid.Nil {
		challengeID = toUUID(auth.ChallengeID)
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
		challengeID,
		auth.SCAExemption,
		string(auth.Status),
		auth.LatencyMs,
		auth.CreatedAt,
		auth.UpdatedAt,
		auth.CapturedAt,
		auth.VoidedAt,
		auth.RefundedAmount,
		auth.RefundedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraAuthorizationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.CardAuthorization, error) {
	var auth domain.CardAuthorization
	var authorizationID, cardID, userID, accountID gocql.UUID
	var reservationID, challengeID gocql.UUID
	var decision, status, declineReason, riskAction, riskLevel, riskReasons, scaExemption string
	var capturedAt, voidedAt, refundedAt *time.Time

	query := `SELECT authorization_id, card_id, user_id, account_id, amount, currency,
		merchant, merchant_category, merchant_city, merchant_country,
		latitude, longitude, terminal_id,
		decision, decline_reason, risk_score, risk_action, risk_level, risk_reasons,
		reservation_id, challenge_id, sca_exemption, status, latency_ms,
		created_at, updated_at, captured_at, voided_at, refunded_amount, refunded_at
		FROM card_authorizations WHERE authorization_id = ?`

	err := r.session.Query(query, toUUID(id)).WithContext(ctx).Scan(
		&authorizationID, &cardID, &userID, &accountID, &auth.Amount, &auth.Currency,
		&auth.Merchant, &auth.MerchantCategory, &auth.MerchantCity, &auth.MerchantCountry,
		&auth.Latitude, &auth.Longitude, &auth.TerminalID,
		&decision, &declineReason, &auth.RiskScore, &riskAction, &riskLevel, &riskReasons,
		&reservationID, &challengeID, &scaExemption, &status, &auth.LatencyMs,
		&auth.CreatedAt, &auth.UpdatedAt, &capturedAt, &voidedAt, &auth.RefundedAmount, &refundedAt,
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
	auth.SCAExemption = scaExemption
	auth.CapturedAt = capturedAt
	auth.VoidedAt = voidedAt
	auth.RefundedAt = refundedAt
	if reservationID != gocql.UUID([16]byte{}) {
		auth.ReservationID = uuid.UUID(reservationID)
	}
	if challengeID != gocql.UUID([16]byte{}) {
		auth.ChallengeID = uuid.UUID(challengeID)
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
		reservation_id, challenge_id, sca_exemption, status, latency_ms,
		created_at, updated_at, captured_at, voided_at, refunded_amount, refunded_at
		FROM card_authorizations WHERE card_id = ? LIMIT ?`

	iter := r.session.Query(query, toUUID(cardID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	var auths []*domain.CardAuthorization
	for {
		var auth domain.CardAuthorization
		var authorizationID, cardIDCol, userID, accountID gocql.UUID
		var reservationID, challengeID gocql.UUID
		var decision, status, declineReason, riskAction, riskLevel, riskReasons, scaExemption string
		var capturedAt, voidedAt, refundedAt *time.Time

		if !iter.Scan(
			&authorizationID, &cardIDCol, &userID, &accountID, &auth.Amount, &auth.Currency,
			&auth.Merchant, &auth.MerchantCategory, &auth.MerchantCity, &auth.MerchantCountry,
			&auth.Latitude, &auth.Longitude, &auth.TerminalID,
			&decision, &declineReason, &auth.RiskScore, &riskAction, &riskLevel, &riskReasons,
			&reservationID, &challengeID, &scaExemption, &status, &auth.LatencyMs,
			&auth.CreatedAt, &auth.UpdatedAt, &capturedAt, &voidedAt, &auth.RefundedAmount, &refundedAt,
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
		auth.SCAExemption = scaExemption
		auth.CapturedAt = capturedAt
		auth.VoidedAt = voidedAt
		auth.RefundedAt = refundedAt
		if reservationID != gocql.UUID([16]byte{}) {
			auth.ReservationID = uuid.UUID(reservationID)
		}
		if challengeID != gocql.UUID([16]byte{}) {
			auth.ChallengeID = uuid.UUID(challengeID)
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

func (r *cassandraAuthorizationRepository) UpdateChallengeSatisfied(ctx context.Context, id uuid.UUID, reservationID uuid.UUID) error {
	now := time.Now().UTC()
	// The final decision on the row is APPROVE: the challenge row retains the
	// step-up trail (who was challenged, when, how many attempts).
	query := `UPDATE card_authorizations SET status = 'APPROVED', decision = 'APPROVE', reservation_id = ?, updated_at = ?
		WHERE authorization_id = ? IF status = 'CHALLENGED'`
	applied, err := r.session.Query(query, toUUID(reservationID), now, toUUID(id)).WithContext(ctx).ScanCAS()
	if err != nil {
		return err
	}
	if !applied {
		return domain.ErrAuthNotChallenged
	}
	return nil
}

func (r *cassandraAuthorizationRepository) UpdateChallengeDeclined(ctx context.Context, id uuid.UUID, reason string) error {
	now := time.Now().UTC()
	query := `UPDATE card_authorizations SET status = 'DECLINED', decision = 'DECLINE', decline_reason = ?, updated_at = ?
		WHERE authorization_id = ? IF status = 'CHALLENGED'`
	applied, err := r.session.Query(query, reason, now, toUUID(id)).WithContext(ctx).ScanCAS()
	if err != nil {
		return err
	}
	if !applied {
		return domain.ErrAuthNotChallenged
	}
	return nil
}

func (r *cassandraAuthorizationRepository) SaveRefund(ctx context.Context, auth *domain.CardAuthorization, expectedStatus domain.AuthorizationStatus, expectedUpdatedAt time.Time) (bool, error) {
	query := `UPDATE card_authorizations
		SET refunded_amount = ?, refunded_at = ?, status = ?, updated_at = ?
		WHERE authorization_id = ? IF status = ? AND updated_at = ?`

	applied, err := r.session.Query(query,
		auth.RefundedAmount,
		auth.RefundedAt,
		string(auth.Status),
		auth.UpdatedAt,
		toUUID(auth.AuthorizationID),
		string(expectedStatus),
		expectedUpdatedAt,
	).WithContext(ctx).ScanCAS()
	if err != nil {
		return false, err
	}
	return applied, nil
}
