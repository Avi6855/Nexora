package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/services/dispute-service/internal/domain"
)

// Repository persists dispute aggregates.
type Repository interface {
	CreateCase(ctx context.Context, c *domain.DisputeCase) error
	UpdateCase(ctx context.Context, c *domain.DisputeCase) error
	GetCase(ctx context.Context, caseID uuid.UUID) (*domain.DisputeCase, error)
	GetCaseByEntry(ctx context.Context, entryID uuid.UUID) (*domain.DisputeCase, error)
	ListCasesByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.DisputeCase, error)
	ListOpenCases(ctx context.Context, limit int) ([]*domain.DisputeCase, error)

	AddEvidence(ctx context.Context, ev *domain.Evidence) error
	ListEvidence(ctx context.Context, caseID uuid.UUID) ([]*domain.Evidence, error)
	CountEvidence(ctx context.Context, caseID uuid.UUID) (int, error)

	AppendEvent(ctx context.Context, ev *domain.CaseEvent) error
	ListEvents(ctx context.Context, caseID uuid.UUID) ([]*domain.CaseEvent, error)
}

type cassandraRepository struct {
	session *gocql.Session
}

// NewCassandraRepository builds the dispute repository.
func NewCassandraRepository(session *gocql.Session) Repository {
	return &cassandraRepository{session: session}
}

const caseCols = `case_id, user_id, account_id, entry_id, transaction_id, amount, currency,
	merchant, category, reason, description, status, stage, eligibility, eligible,
	ineligibility_reason, resolution, provisional_credit, deadline, resolved_at, created_at, updated_at`

func scanCase(row func(dest ...interface{}) bool) (*domain.DisputeCase, error) {
	var c domain.DisputeCase
	var caseID, userID, accountID, entryID, txnID gocql.UUID
	var reason, status, stage, eligibility, ineligibility, resolution string
	var provisional bool
	var deadline, resolvedAt, createdAt, updatedAt time.Time
	ok := row(&caseID, &userID, &accountID, &entryID, &txnID, &c.Amount, &c.Currency,
		&c.Merchant, &c.Category, &reason, &c.Description, &status, &stage, &eligibility, &c.Eligibility.Eligible,
		&ineligibility, &resolution, &provisional, &deadline, &resolvedAt, &createdAt, &updatedAt)
	if !ok {
		return nil, nil
	}
	c.CaseID = uuid.UUID(caseID)
	c.UserID = uuid.UUID(userID)
	c.AccountID = uuid.UUID(accountID)
	c.EntryID = uuid.UUID(entryID)
	c.TransactionID = uuid.UUID(txnID)
	c.Reason = domain.DisputeReason(reason)
	c.Status = domain.CaseStatus(status)
	c.Stage = domain.LifecycleStage(stage)
	c.Eligibility = domain.Eligibility{
		Eligible:          c.Eligibility.Eligible,
		Reason:            eligibility,
		ProvisionalCredit: provisional,
	}
	if !deadline.IsZero() {
		c.Deadline = &deadline
	}
	if !resolvedAt.IsZero() {
		c.ResolvedAt = &resolvedAt
	}
	if resolution != "" {
		c.Resolution = domain.Resolution(resolution)
	}
	c.CreatedAt = createdAt
	c.UpdatedAt = updatedAt
	return &c, nil
}

func (r *cassandraRepository) caseInsert(ctx context.Context, c *domain.DisputeCase) error {
	q := `INSERT INTO dispute_cases (` + caseCols + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(c.CaseID), gocql.UUID(c.UserID), gocql.UUID(c.AccountID), gocql.UUID(c.EntryID), gocql.UUID(c.TransactionID),
		c.Amount, c.Currency, c.Merchant, c.Category, string(c.Reason), c.Description,
		string(c.Status), string(c.Stage), c.Eligibility.Reason, c.Eligibility.Eligible,
		c.Eligibility.Reason, string(c.Resolution), c.ProvisionalCredit, orZero(c.Deadline), orZero(c.ResolvedAt),
		c.CreatedAt, c.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) CreateCase(ctx context.Context, c *domain.DisputeCase) error {
	if err := r.caseInsert(ctx, c); err != nil {
		return err
	}
	// Read-model row for the app list.
	q2 := `INSERT INTO dispute_cases_by_user
		(user_id, created_at, case_id, status, lifecycle_stage, amount, currency, merchant, category, reason, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q2,
		gocql.UUID(c.UserID), c.CreatedAt, gocql.UUID(c.CaseID),
		string(c.Status), string(c.Stage), c.Amount, c.Currency, c.Merchant, c.Category, string(c.Reason), c.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) UpdateCase(ctx context.Context, c *domain.DisputeCase) error {
	return r.caseInsert(ctx, c)
}

func orZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (r *cassandraRepository) GetCase(ctx context.Context, caseID uuid.UUID) (*domain.DisputeCase, error) {
	q := `SELECT ` + caseCols + ` FROM dispute_cases WHERE case_id = ?`
	iter := r.session.Query(q, gocql.UUID(caseID)).WithContext(ctx).Iter()
	defer iter.Close()
	c, err := scanCase(iter.Scan)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, domain.ErrCaseNotFound
	}
	return c, nil
}

func (r *cassandraRepository) GetCaseByEntry(ctx context.Context, entryID uuid.UUID) (*domain.DisputeCase, error) {
	q := `SELECT ` + caseCols + ` FROM dispute_cases WHERE entry_id = ? ALLOW FILTERING`
	iter := r.session.Query(q, gocql.UUID(entryID)).WithContext(ctx).Iter()
	defer iter.Close()
	return scanCase(iter.Scan)
}

func (r *cassandraRepository) ListCasesByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.DisputeCase, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT case_id, status, lifecycle_stage, amount, currency, merchant, category, reason, created_at, updated_at
		FROM dispute_cases_by_user WHERE user_id = ? LIMIT ?`
	iter := r.session.Query(q, gocql.UUID(userID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.DisputeCase, 0)
	var cid gocql.UUID
	var status, stage, merchant, category, reason string
	var amount int64
	var currency string
	var createdAt, updatedAt time.Time
	for iter.Scan(&cid, &status, &stage, &amount, &currency, &merchant, &category, &reason, &createdAt, &updatedAt) {
		out = append(out, &domain.DisputeCase{
			CaseID:    uuid.UUID(cid),
			Status:    domain.CaseStatus(status),
			Stage:     domain.LifecycleStage(stage),
			Amount:    amount,
			Currency:  currency,
			Merchant:  merchant,
			Category:  category,
			Reason:    domain.DisputeReason(reason),
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		})
	}
	return out, iter.Close()
}

func (r *cassandraRepository) ListOpenCases(ctx context.Context, limit int) ([]*domain.DisputeCase, error) {
	if limit <= 0 {
		limit = 200
	}
	q := `SELECT ` + caseCols + ` FROM dispute_cases WHERE status = ? ALLOW FILTERING LIMIT ?`
	iter := r.session.Query(q, string(domain.CaseStatusOpen), limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.DisputeCase, 0)
	for {
		c, err := scanCase(iter.Scan)
		if err != nil {
			return nil, err
		}
		if c == nil {
			break
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *cassandraRepository) AddEvidence(ctx context.Context, ev *domain.Evidence) error {
	q := `INSERT INTO dispute_evidence (case_id, evidence_id, evidence_type, filename, content, uploaded_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(ev.CaseID), gocql.UUID(ev.EvidenceID), ev.EvidenceType, ev.Filename, ev.Content, gocql.UUID(ev.UploadedBy), ev.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) ListEvidence(ctx context.Context, caseID uuid.UUID) ([]*domain.Evidence, error) {
	q := `SELECT case_id, evidence_id, evidence_type, filename, content, uploaded_by, created_at
		FROM dispute_evidence WHERE case_id = ?`
	iter := r.session.Query(q, gocql.UUID(caseID)).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.Evidence, 0)
	var cid, eid, by gocql.UUID
	var etype, filename, content string
	var createdAt time.Time
	for iter.Scan(&cid, &eid, &etype, &filename, &content, &by, &createdAt) {
		out = append(out, &domain.Evidence{
			CaseID:       uuid.UUID(cid),
			EvidenceID:   uuid.UUID(eid),
			EvidenceType: etype,
			Filename:     filename,
			Content:      content,
			UploadedBy:   uuid.UUID(by),
			CreatedAt:    createdAt,
		})
	}
	return out, iter.Close()
}

func (r *cassandraRepository) CountEvidence(ctx context.Context, caseID uuid.UUID) (int, error) {
	evs, err := r.ListEvidence(ctx, caseID)
	if err != nil {
		return 0, err
	}
	return len(evs), nil
}

func (r *cassandraRepository) AppendEvent(ctx context.Context, ev *domain.CaseEvent) error {
	q := `INSERT INTO dispute_events (case_id, event_id, event_type, actor, note, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(ev.CaseID), gocql.UUID(ev.EventID), ev.EventType, ev.Actor, ev.Note, ev.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) ListEvents(ctx context.Context, caseID uuid.UUID) ([]*domain.CaseEvent, error) {
	q := `SELECT case_id, event_id, event_type, actor, note, created_at
		FROM dispute_events WHERE case_id = ?`
	iter := r.session.Query(q, gocql.UUID(caseID)).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.CaseEvent, 0)
	var cid, eid gocql.UUID
	var etype, actor, note string
	var createdAt time.Time
	for iter.Scan(&cid, &eid, &etype, &actor, &note, &createdAt) {
		out = append(out, &domain.CaseEvent{
			CaseID:    uuid.UUID(cid),
			EventID:   uuid.UUID(eid),
			EventType: etype,
			Actor:     actor,
			Note:      note,
			CreatedAt: createdAt,
		})
	}
	return out, iter.Close()
}
