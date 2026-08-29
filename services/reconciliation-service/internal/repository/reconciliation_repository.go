package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/reconciliation-service/internal/domain"
)

type ReconciliationRepository interface {
	CreateCase(ctx context.Context, c *domain.ReconciliationCase) error
	GetCaseByID(ctx context.Context, id uuid.UUID) (*domain.ReconciliationCase, error)
	GetCaseByPaymentID(ctx context.Context, paymentID uuid.UUID) (*domain.ReconciliationCase, error)
	GetCasesByStatus(ctx context.Context, status domain.ReconciliationStatus, limit int) ([]*domain.ReconciliationCase, error)
	UpdateCaseStatus(ctx context.Context, id uuid.UUID, status domain.ReconciliationStatus, reason string) error
	UpdateCaseResolution(ctx context.Context, id uuid.UUID, resolution domain.ResolutionType, reason string) error
	IncrementAttemptCount(ctx context.Context, id uuid.UUID) error
	CreateRecord(ctx context.Context, record *domain.ReconciliationRecord) error
	GetRecordByID(ctx context.Context, id uuid.UUID) (*domain.ReconciliationRecord, error)
	GetRecordsByStatus(ctx context.Context, status domain.ReconciliationStatus) ([]*domain.ReconciliationRecord, error)
	UpdateRecordStatus(ctx context.Context, id uuid.UUID, status domain.ReconciliationStatus, reason string) error
}

type cassandraReconciliationRepository struct {
	session *gocql.Session
}

func NewCassandraReconciliationRepository(session *gocql.Session) ReconciliationRepository {
	return &cassandraReconciliationRepository{session: session}
}

func (r *cassandraReconciliationRepository) CreateCase(ctx context.Context, c *domain.ReconciliationCase) error {
	query := `INSERT INTO reconciliation_cases (case_id, payment_id, internal_state, external_state, internal_amount, external_amount, currency, status, resolution, discrepancy_reason, provider_ref, attempt_count, max_attempts, created_at, updated_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		c.CaseID, c.PaymentID, c.InternalState, c.ExternalState,
		c.InternalAmount, c.ExternalAmount, c.Currency,
		string(c.Status), string(c.Resolution), c.DiscrepancyReason,
		c.ProviderRef, c.AttemptCount, c.MaxAttempts,
		c.CreatedAt, c.UpdatedAt, c.ResolvedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraReconciliationRepository) GetCaseByID(ctx context.Context, id uuid.UUID) (*domain.ReconciliationCase, error) {
	var c domain.ReconciliationCase
	var status, resolution string
	query := `SELECT case_id, payment_id, internal_state, external_state, internal_amount, external_amount, currency, status, resolution, discrepancy_reason, provider_ref, attempt_count, max_attempts, created_at, updated_at, resolved_at
		FROM reconciliation_cases WHERE case_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&c.CaseID, &c.PaymentID, &c.InternalState, &c.ExternalState,
		&c.InternalAmount, &c.ExternalAmount, &c.Currency,
		&status, &resolution, &c.DiscrepancyReason,
		&c.ProviderRef, &c.AttemptCount, &c.MaxAttempts,
		&c.CreatedAt, &c.UpdatedAt, &c.ResolvedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("reconciliation case not found")
	}
	if err != nil {
		return nil, err
	}
	c.Status = domain.ReconciliationStatus(status)
	c.Resolution = domain.ResolutionType(resolution)
	c.AuditTrail = make([]domain.ReconciliationAudit, 0)
	return &c, nil
}

func (r *cassandraReconciliationRepository) GetCaseByPaymentID(ctx context.Context, paymentID uuid.UUID) (*domain.ReconciliationCase, error) {
	var c domain.ReconciliationCase
	var status, resolution string
	query := `SELECT case_id, payment_id, internal_state, external_state, internal_amount, external_amount, currency, status, resolution, discrepancy_reason, provider_ref, attempt_count, max_attempts, created_at, updated_at, resolved_at
		FROM reconciliation_cases WHERE payment_id = ? LIMIT 1`
	err := r.session.Query(query, paymentID).WithContext(ctx).Scan(
		&c.CaseID, &c.PaymentID, &c.InternalState, &c.ExternalState,
		&c.InternalAmount, &c.ExternalAmount, &c.Currency,
		&status, &resolution, &c.DiscrepancyReason,
		&c.ProviderRef, &c.AttemptCount, &c.MaxAttempts,
		&c.CreatedAt, &c.UpdatedAt, &c.ResolvedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("reconciliation case not found for payment")
	}
	if err != nil {
		return nil, err
	}
	c.Status = domain.ReconciliationStatus(status)
	c.Resolution = domain.ResolutionType(resolution)
	c.AuditTrail = make([]domain.ReconciliationAudit, 0)
	return &c, nil
}

func (r *cassandraReconciliationRepository) GetCasesByStatus(ctx context.Context, status domain.ReconciliationStatus, limit int) ([]*domain.ReconciliationCase, error) {
	if limit <= 0 {
		limit = 100
	}
	var cases []*domain.ReconciliationCase
	query := `SELECT case_id, payment_id, internal_state, external_state, internal_amount, external_amount, currency, status, resolution, discrepancy_reason, provider_ref, attempt_count, max_attempts, created_at, updated_at, resolved_at
		FROM reconciliation_cases WHERE status = ? LIMIT ? ALLOW FILTERING`
	iter := r.session.Query(query, string(status), limit).WithContext(ctx).Iter()
	defer iter.Close()
	var c domain.ReconciliationCase
	var statusStr, resolution string
	for iter.Scan(
		&c.CaseID, &c.PaymentID, &c.InternalState, &c.ExternalState,
		&c.InternalAmount, &c.ExternalAmount, &c.Currency,
		&statusStr, &resolution, &c.DiscrepancyReason,
		&c.ProviderRef, &c.AttemptCount, &c.MaxAttempts,
		&c.CreatedAt, &c.UpdatedAt, &c.ResolvedAt,
	) {
		c.Status = domain.ReconciliationStatus(statusStr)
		c.Resolution = domain.ResolutionType(resolution)
		c.AuditTrail = make([]domain.ReconciliationAudit, 0)
		cc := c
		cases = append(cases, &cc)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return cases, nil
}

func (r *cassandraReconciliationRepository) UpdateCaseStatus(ctx context.Context, id uuid.UUID, status domain.ReconciliationStatus, reason string) error {
	now := time.Now().UTC()
	query := `UPDATE reconciliation_cases SET status = ?, discrepancy_reason = ?, updated_at = ? WHERE case_id = ?`
	return r.session.Query(query, string(status), reason, now, id).WithContext(ctx).Exec()
}

func (r *cassandraReconciliationRepository) UpdateCaseResolution(ctx context.Context, id uuid.UUID, resolution domain.ResolutionType, reason string) error {
	now := time.Now().UTC()
	query := `UPDATE reconciliation_cases SET resolution = ?, discrepancy_reason = ?, status = ?, updated_at = ?, resolved_at = ? WHERE case_id = ?`
	return r.session.Query(query, string(resolution), reason, string(domain.ReconciliationStatusResolved), now, now, id).WithContext(ctx).Exec()
}

func (r *cassandraReconciliationRepository) IncrementAttemptCount(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE reconciliation_cases SET attempt_count = attempt_count + 1, updated_at = ? WHERE case_id = ?`
	return r.session.Query(query, time.Now().UTC(), id).WithContext(ctx).Exec()
}

func (r *cassandraReconciliationRepository) CreateRecord(ctx context.Context, record *domain.ReconciliationRecord) error {
	query := `INSERT INTO reconciliation_records (record_id, transaction_id, source, target, amount, currency, status, discrepancy_reason, created_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query, record.RecordID, record.TransactionID, record.Source, record.Target, record.Amount, record.Currency, string(record.Status), record.DiscrepancyReason, record.CreatedAt, record.ResolvedAt).WithContext(ctx).Exec()
}

func (r *cassandraReconciliationRepository) GetRecordByID(ctx context.Context, id uuid.UUID) (*domain.ReconciliationRecord, error) {
	var record domain.ReconciliationRecord
	var status string
	query := `SELECT record_id, transaction_id, source, target, amount, currency, status, discrepancy_reason, created_at, resolved_at FROM reconciliation_records WHERE record_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(&record.RecordID, &record.TransactionID, &record.Source, &record.Target, &record.Amount, &record.Currency, &status, &record.DiscrepancyReason, &record.CreatedAt, &record.ResolvedAt)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("record not found")
	}
	if err != nil {
		return nil, err
	}
	record.Status = domain.ReconciliationStatus(status)
	return &record, nil
}

func (r *cassandraReconciliationRepository) GetRecordsByStatus(ctx context.Context, status domain.ReconciliationStatus) ([]*domain.ReconciliationRecord, error) {
	var records []*domain.ReconciliationRecord
	query := `SELECT record_id, transaction_id, source, target, amount, currency, status, discrepancy_reason, created_at, resolved_at FROM reconciliation_records WHERE status = ? ALLOW FILTERING`
	iter := r.session.Query(query, string(status)).WithContext(ctx).Iter()
	defer iter.Close()
	var record domain.ReconciliationRecord
	var st string
	for iter.Scan(&record.RecordID, &record.TransactionID, &record.Source, &record.Target, &record.Amount, &record.Currency, &st, &record.DiscrepancyReason, &record.CreatedAt, &record.ResolvedAt) {
		record.Status = domain.ReconciliationStatus(st)
		r := record
		records = append(records, &r)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return records, nil
}

func (r *cassandraReconciliationRepository) UpdateRecordStatus(ctx context.Context, id uuid.UUID, status domain.ReconciliationStatus, reason string) error {
	now := time.Now().UTC()
	query := `UPDATE reconciliation_records SET status = ?, discrepancy_reason = ?, resolved_at = ? WHERE record_id = ?`
	return r.session.Query(query, string(status), reason, now, id).WithContext(ctx).Exec()
}
