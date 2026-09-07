package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/services/insights-service/internal/domain"
)

// Repository persists the intelligence projections.
type Repository interface {
	UpsertSubscription(ctx context.Context, s *domain.Subscription) error
	GetSubscription(ctx context.Context, accountID uuid.UUID, subID string) (*domain.Subscription, error)
	ListSubscriptions(ctx context.Context, accountID uuid.UUID) ([]*domain.Subscription, error)

	UpsertBaseline(ctx context.Context, b *domain.Baseline) error
	ListBaselines(ctx context.Context, accountID uuid.UUID) ([]*domain.Baseline, error)

	UpsertIncome(ctx context.Context, in *domain.Income) error
	GetIncome(ctx context.Context, accountID uuid.UUID) (*domain.Income, error)

	InsertAlert(ctx context.Context, a *domain.InsightAlert) error
	ListAlerts(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.InsightAlert, error)

	// ListAccountsWithIncome returns every account that has an income
	// projection (used by the periodic salary-late scan).
	ListAccountsWithIncome(ctx context.Context) ([]uuid.UUID, error)
}

type cassandraRepository struct {
	session *gocql.Session
}

// NewCassandraRepository builds the projections repository.
func NewCassandraRepository(session *gocql.Session) Repository {
	return &cassandraRepository{session: session}
}

func (r *cassandraRepository) UpsertSubscription(ctx context.Context, s *domain.Subscription) error {
	q := `INSERT INTO subscriptions
		(account_id, subscription_id, merchant, monthly_amount, last_amount, previous_amount,
		 cadence_days, transaction_count, price_hike_pct, active, notified,
		 first_seen_at, last_seen_at, next_expected_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(s.AccountID), s.SubscriptionID, s.Merchant,
		s.MonthlyAmount, s.LastAmount, s.PreviousAmount,
		s.CadenceDays, s.TxCount, s.PriceHikePct, s.Active, s.Notified,
		s.FirstSeenAt, s.LastSeenAt, s.NextExpectedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) GetSubscription(ctx context.Context, accountID uuid.UUID, subID string) (*domain.Subscription, error) {
	q := `SELECT account_id, subscription_id, merchant, monthly_amount, last_amount, previous_amount,
		cadence_days, transaction_count, price_hike_pct, active, notified,
		first_seen_at, last_seen_at, next_expected_at
		FROM subscriptions WHERE account_id = ? AND subscription_id = ?`
	return scanSubscription(r.session.Query(q, gocql.UUID(accountID), subID).WithContext(ctx))
}

func (r *cassandraRepository) ListSubscriptions(ctx context.Context, accountID uuid.UUID) ([]*domain.Subscription, error) {
	q := `SELECT account_id, subscription_id, merchant, monthly_amount, last_amount, previous_amount,
		cadence_days, transaction_count, price_hike_pct, active, notified,
		first_seen_at, last_seen_at, next_expected_at
		FROM subscriptions WHERE account_id = ?`
	iter := r.session.Query(q, gocql.UUID(accountID)).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.Subscription, 0)
	for {
		s, err := scanSubscriptionIter(iter)
		if err != nil {
			return nil, err
		}
		if s == nil {
			break
		}
		out = append(out, s)
	}
	return out, nil
}

func scanSubscription(query *gocql.Query) (*domain.Subscription, error) {
	var s domain.Subscription
	var accountIDCol gocql.UUID
	var firstSeen, lastSeen, nextExpected time.Time
	err := query.Scan(
		&accountIDCol, &s.SubscriptionID, &s.Merchant,
		&s.MonthlyAmount, &s.LastAmount, &s.PreviousAmount,
		&s.CadenceDays, &s.TxCount, &s.PriceHikePct, &s.Active, &s.Notified,
		&firstSeen, &lastSeen, &nextExpected,
	)
	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.AccountID = uuid.UUID(accountIDCol)
	s.FirstSeenAt, s.LastSeenAt, s.NextExpectedAt = firstSeen, lastSeen, nextExpected
	return &s, nil
}

func scanSubscriptionIter(iter *gocql.Iter) (*domain.Subscription, error) {
	var s domain.Subscription
	var accountIDCol gocql.UUID
	var firstSeen, lastSeen, nextExpected time.Time
	if !iter.Scan(
		&accountIDCol, &s.SubscriptionID, &s.Merchant,
		&s.MonthlyAmount, &s.LastAmount, &s.PreviousAmount,
		&s.CadenceDays, &s.TxCount, &s.PriceHikePct, &s.Active, &s.Notified,
		&firstSeen, &lastSeen, &nextExpected,
	) {
		return nil, nil
	}
	s.AccountID = uuid.UUID(accountIDCol)
	s.FirstSeenAt, s.LastSeenAt, s.NextExpectedAt = firstSeen, lastSeen, nextExpected
	return &s, nil
}

func (r *cassandraRepository) UpsertBaseline(ctx context.Context, b *domain.Baseline) error {
	q := `INSERT INTO account_baselines (account_id, category, daily_avg, total_30d, tx_count, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(b.AccountID), b.Category, b.DailyAvg, b.Total30d, b.TxCount, b.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) ListBaselines(ctx context.Context, accountID uuid.UUID) ([]*domain.Baseline, error) {
	q := `SELECT account_id, category, daily_avg, total_30d, tx_count, updated_at
		FROM account_baselines WHERE account_id = ?`
	iter := r.session.Query(q, gocql.UUID(accountID)).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.Baseline, 0)
	var accountIDCol gocql.UUID
	var updated time.Time
	var category string
	var dailyAvg, total int64
	var count int
	for iter.Scan(&accountIDCol, &category, &dailyAvg, &total, &count, &updated) {
		out = append(out, &domain.Baseline{
			AccountID: uuid.UUID(accountIDCol),
			Category:  category,
			DailyAvg:  dailyAvg,
			Total30d:  total,
			TxCount:   count,
			UpdatedAt: updated,
		})
	}
	return out, iter.Close()
}

func (r *cassandraRepository) UpsertIncome(ctx context.Context, in *domain.Income) error {
	q := `INSERT INTO account_income (account_id, user_id, source, last_amount, monthly_avg, last_income_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(in.AccountID), gocql.UUID(in.UserID), in.Source,
		in.LastAmount, in.MonthlyAvg, in.LastIncomeAt, in.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) GetIncome(ctx context.Context, accountID uuid.UUID) (*domain.Income, error) {
	q := `SELECT account_id, user_id, source, last_amount, monthly_avg, last_income_at, updated_at
		FROM account_income WHERE account_id = ?`
	var in domain.Income
	var accountIDCol, userIDCol gocql.UUID
	var lastAt, updated time.Time
	err := r.session.Query(q, gocql.UUID(accountID)).WithContext(ctx).Scan(
		&accountIDCol, &userIDCol, &in.Source, &in.LastAmount, &in.MonthlyAvg, &lastAt, &updated,
	)
	if err == gocql.ErrNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	in.AccountID = uuid.UUID(accountIDCol)
	in.UserID = uuid.UUID(userIDCol)
	in.LastIncomeAt = lastAt
	in.UpdatedAt = updated
	return &in, nil
}

func (r *cassandraRepository) InsertAlert(ctx context.Context, a *domain.InsightAlert) error {
	q := `INSERT INTO insights_alerts (account_id, alert_id, alert_type, title, body, payload, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(a.AccountID), gocql.UUID(a.AlertID), a.AlertType,
		a.Title, a.Body, a.Payload, a.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) ListAlerts(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.InsightAlert, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT account_id, alert_id, alert_type, title, body, payload, created_at
		FROM insights_alerts WHERE account_id = ? LIMIT ?`
	iter := r.session.Query(q, gocql.UUID(accountID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.InsightAlert, 0)
	var accountIDCol, alertIDCol gocql.UUID
	var alertType, title, body, payload string
	var createdAt time.Time
	for iter.Scan(&accountIDCol, &alertIDCol, &alertType, &title, &body, &payload, &createdAt) {
		out = append(out, &domain.InsightAlert{
			AccountID: uuid.UUID(accountIDCol),
			AlertID:   uuid.UUID(alertIDCol),
			AlertType: alertType,
			Title:     title,
			Body:      body,
			Payload:   payload,
			CreatedAt: createdAt,
		})
	}
	return out, iter.Close()
}

// ListAccountsWithIncome scans the small account_income table (one row per
// account) — appropriate at demo scale; a production deployment would page
// with a token.
func (r *cassandraRepository) ListAccountsWithIncome(ctx context.Context) ([]uuid.UUID, error) {
	iter := r.session.Query(`SELECT account_id FROM account_income`).WithContext(ctx).Iter()
	defer iter.Close()
	out := make([]uuid.UUID, 0)
	var accountIDCol gocql.UUID
	for iter.Scan(&accountIDCol) {
		out = append(out, uuid.UUID(accountIDCol))
	}
	return out, iter.Close()
}
