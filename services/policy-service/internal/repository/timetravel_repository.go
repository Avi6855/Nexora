package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
)

// PolicyVersionRepository reads append-only policy versions.
type PolicyVersionRepository interface {
	// VersionsFor returns every version of a policy whose validity window
	// intersects [from, to] — the replay engine narrows to the covering one.
	VersionsFor(ctx context.Context, policyID uuid.UUID) ([]domain.PolicyVersion, error)
	// SaveVersion appends a new version (never updates an existing one).
	SaveVersion(ctx context.Context, v domain.PolicyVersion) error
	// CloseSuperseded marks the previous open-ended version closed at t.
	CloseSuperseded(ctx context.Context, policyID uuid.UUID, exceptVersion int, t time.Time) error
}

type cassandraPolicyVersionRepository struct {
	session *gocql.Session
}

func NewCassandraPolicyVersionRepository(session *gocql.Session) PolicyVersionRepository {
	return &cassandraPolicyVersionRepository{session: session}
}

func (r *cassandraPolicyVersionRepository) VersionsFor(ctx context.Context, policyID uuid.UUID) ([]domain.PolicyVersion, error) {
	query := `SELECT policy_id, version, status, rules, name, enabled, valid_from, valid_to
		FROM policy_versions WHERE policy_id = ?`
	iter := r.session.Query(query, policyID).WithContext(ctx).Iter()
	defer iter.Close()
	var out []domain.PolicyVersion
	var v domain.PolicyVersion
	var status, rulesJSON string
	for iter.Scan(&v.PolicyID, &v.Version, &status, &rulesJSON, &v.Name, &v.Enabled, &v.ValidFrom, &v.ValidTo) {
		v.Status = domain.PolicyStatus(status)
		v.Rules = deserializePolicyRules(rulesJSON)
		out = append(out, v)
	}
	return out, iter.Close()
}

func (r *cassandraPolicyVersionRepository) SaveVersion(ctx context.Context, v domain.PolicyVersion) error {
	query := `INSERT INTO policy_versions (policy_id, version, status, rules, name, enabled, valid_from, valid_to)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	rulesJSON := serializePolicyRules(v.Rules)
	return r.session.Query(query,
		v.PolicyID, v.Version, string(v.Status), rulesJSON, v.Name, v.Enabled, v.ValidFrom, v.ValidTo,
	).WithContext(ctx).Exec()
}

func (r *cassandraPolicyVersionRepository) CloseSuperseded(ctx context.Context, policyID uuid.UUID, exceptVersion int, t time.Time) error {
	versions, err := r.VersionsFor(ctx, policyID)
	if err != nil {
		return err
	}
	for _, v := range versions {
		if v.Version == exceptVersion || !v.ValidTo.IsZero() {
			continue
		}
		q := `UPDATE policy_versions SET valid_to = ? WHERE policy_id = ? AND version = ?`
		if err := r.session.Query(q, t, policyID, v.Version).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

// SnapshotRepository reads/writes customer state snapshots.
type SnapshotRepository interface {
	SaveSnapshot(ctx context.Context, s domain.CustomerStateSnapshot) error
	// LatestAtOrBefore returns the snapshot in force at instant t.
	LatestAtOrBefore(ctx context.Context, userID uuid.UUID, t time.Time) (*domain.CustomerStateSnapshot, error)
}

type cassandraSnapshotRepository struct {
	session *gocql.Session
}

func NewCassandraSnapshotRepository(session *gocql.Session) SnapshotRepository {
	return &cassandraSnapshotRepository{session: session}
}

func (r *cassandraSnapshotRepository) SaveSnapshot(ctx context.Context, s domain.CustomerStateSnapshot) error {
	query := `INSERT INTO customer_state_snapshots (user_id, captured_at, snapshot_id, kyc_tier, device_known, account_age_days, flagged_risk)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		s.UserID, s.CapturedAt, s.SnapshotID, s.KYCTier, s.DeviceKnown, s.AccountAgeDays, s.FlaggedRisk,
	).WithContext(ctx).Exec()
}

func (r *cassandraSnapshotRepository) LatestAtOrBefore(ctx context.Context, userID uuid.UUID, t time.Time) (*domain.CustomerStateSnapshot, error) {
	// Clustering order is captured_at DESC, so LIMIT 1 returns the newest
	// snapshot at or before t.
	query := `SELECT snapshot_id, user_id, captured_at, kyc_tier, device_known, account_age_days, flagged_risk
		FROM customer_state_snapshots WHERE user_id = ? AND captured_at <= ? LIMIT 1`
	var s domain.CustomerStateSnapshot
	err := r.session.Query(query, userID, t).WithContext(ctx).Scan(
		&s.SnapshotID, &s.UserID, &s.CapturedAt, &s.KYCTier, &s.DeviceKnown, &s.AccountAgeDays, &s.FlaggedRisk)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("%w", domain.ErrNoStateSnapshot)
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// HistoryRepository stores the decision records that make future
// reconstruction possible, keyed for both access patterns.
type HistoryRepository interface {
	SaveRecord(ctx context.Context, rec domain.HistoricalDecisionRecord) error
	GetByPayment(ctx context.Context, paymentID string) (*domain.HistoricalDecisionRecord, error)
}

type cassandraHistoryRepository struct {
	session *gocql.Session
}

func NewCassandraHistoryRepository(session *gocql.Session) HistoryRepository {
	return &cassandraHistoryRepository{session: session}
}

func (r *cassandraHistoryRepository) SaveRecord(ctx context.Context, rec domain.HistoricalDecisionRecord) error {
	query := `INSERT INTO historical_decisions (record_id, payment_id, decided_at, final_decision, policy_versions, snapshot_id, replay_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	pv := make([]string, 0, len(rec.Request.PolicyVersions))
	for _, id := range rec.Request.PolicyVersions {
		pv = append(pv, id.String())
	}
	return r.session.Query(query,
		rec.RecordID, rec.Request.PaymentID, rec.Request.DecidedAt,
		string(rec.Request.FinalDecision), pv, rec.Request.SnapshotID, rec.ReplayHash, rec.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraHistoryRepository) GetByPayment(ctx context.Context, paymentID string) (*domain.HistoricalDecisionRecord, error) {
	query := `SELECT record_id, payment_id, decided_at, final_decision, policy_versions, snapshot_id, replay_hash, created_at
		FROM historical_decisions WHERE payment_id = ? LIMIT 1`
	var rec domain.HistoricalDecisionRecord
	var decision string
	var pv []string
	err := r.session.Query(query, paymentID).WithContext(ctx).Scan(
		&rec.RecordID, &rec.Request.PaymentID, &rec.Request.DecidedAt, &decision,
		&pv, &rec.Request.SnapshotID, &rec.ReplayHash, &rec.CreatedAt)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("no historical decision record for payment %s", paymentID)
	}
	if err != nil {
		return nil, err
	}
	rec.Request.FinalDecision = domain.PolicyDecisionAction(decision)
	for _, idStr := range pv {
		id, err := uuid.Parse(idStr)
		if err == nil {
			rec.Request.PolicyVersions = append(rec.Request.PolicyVersions, id)
		}
	}
	return &rec, nil
}
