package repository

import (
	"context"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"

	"github.com/nexora/nexora/services/consent-service/internal/domain"
)

// Repository persists delegation grants + audit.
type Repository interface {
	CreateGrant(ctx context.Context, g *domain.DelegationGrant) error
	UpdateGrant(ctx context.Context, g *domain.DelegationGrant) error
	GetGrant(ctx context.Context, grantID uuid.UUID) (*domain.DelegationGrant, error)
	ListGrantsByOwner(ctx context.Context, ownerID uuid.UUID, limit int) ([]*domain.DelegationGrant, error)

	InsertAudit(ctx context.Context, a *domain.AuditRecord) error
	ListAudit(ctx context.Context, grantID uuid.UUID, limit int) ([]*domain.AuditRecord, error)
}

type cassandraRepository struct {
	session *gocql.Session
}

// NewCassandraRepository builds the consent repository.
func NewCassandraRepository(session *gocql.Session) Repository {
	return &cassandraRepository{session: session}
}

func (r *cassandraRepository) CreateGrant(ctx context.Context, g *domain.DelegationGrant) error {
	q := `INSERT INTO delegation_grants
		(grant_id, owner_user_id, delegate_email, delegate_user_id, label, scopes, status, starts_at, expires_at, created_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if err := r.session.Query(q,
		gocql.UUID(g.GrantID), gocql.UUID(g.OwnerUserID), g.DelegateEmail, nullUUID(g.DelegateUserID),
		g.Label, domain.ScopesToString(g.Scopes), string(g.Status),
		g.StartsAt, g.ExpiresAt, g.CreatedAt, orZero(g.RevokedAt),
	).WithContext(ctx).Exec(); err != nil {
		return err
	}
	q2 := `INSERT INTO delegation_grants_by_owner
		(owner_user_id, created_at, grant_id, delegate_email, label, scopes, status, starts_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q2,
		gocql.UUID(g.OwnerUserID), g.CreatedAt, gocql.UUID(g.GrantID), g.DelegateEmail,
		g.Label, domain.ScopesToString(g.Scopes), string(g.Status), g.StartsAt, g.ExpiresAt,
	).WithContext(ctx).Exec()
}

func nullUUID(id uuid.UUID) interface{} {
	if id == uuid.Nil {
		return nil
	}
	return gocql.UUID(id)
}

func orZero(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (r *cassandraRepository) UpdateGrant(ctx context.Context, g *domain.DelegationGrant) error {
	q := `UPDATE delegation_grants SET status = ?, revoked_at = ? WHERE grant_id = ?`
	return r.session.Query(q, string(g.Status), orZero(g.RevokedAt), gocql.UUID(g.GrantID)).WithContext(ctx).Exec()
}

func (r *cassandraRepository) GetGrant(ctx context.Context, grantID uuid.UUID) (*domain.DelegationGrant, error) {
	q := `SELECT grant_id, owner_user_id, delegate_email, delegate_user_id, label, scopes, status, starts_at, expires_at, created_at, revoked_at
		FROM delegation_grants WHERE grant_id = ?`
	var g domain.DelegationGrant
	var gid, oid, did gocql.UUID
	var email, label, scopes, status string
	var startsAt, expiresAt, createdAt, revokedAt time.Time
	err := r.session.Query(q, gocql.UUID(grantID)).WithContext(ctx).Scan(
		&gid, &oid, &email, &did, &label, &scopes, &status, &startsAt, &expiresAt, &createdAt, &revokedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, domain.ErrGrantNotFound
	}
	if err != nil {
		return nil, err
	}
	g.GrantID = uuid.UUID(gid)
	g.OwnerUserID = uuid.UUID(oid)
	g.DelegateEmail = email
	g.DelegateUserID = uuid.UUID(did)
	g.Label = label
	g.Scopes = domain.StringToScopes(scopes)
	g.Status = domain.GrantStatus(status)
	g.StartsAt = startsAt
	g.ExpiresAt = expiresAt
	g.CreatedAt = createdAt
	if !revokedAt.IsZero() {
		g.RevokedAt = &revokedAt
	}
	return &g, nil
}

func (r *cassandraRepository) ListGrantsByOwner(ctx context.Context, ownerID uuid.UUID, limit int) ([]*domain.DelegationGrant, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT grant_id, delegate_email, label, scopes, status, starts_at, expires_at, created_at
		FROM delegation_grants_by_owner WHERE owner_user_id = ? LIMIT ?`
	iter := r.session.Query(q, gocql.UUID(ownerID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.DelegationGrant, 0)
	var gid gocql.UUID
	var email, label, scopes, status string
	var startsAt, expiresAt, createdAt time.Time
	for iter.Scan(&gid, &email, &label, &scopes, &status, &startsAt, &expiresAt, &createdAt) {
		out = append(out, &domain.DelegationGrant{
			GrantID:       uuid.UUID(gid),
			OwnerUserID:   ownerID,
			DelegateEmail: email,
			Label:         label,
			Scopes:        domain.StringToScopes(scopes),
			Status:        domain.GrantStatus(status),
			StartsAt:      startsAt,
			ExpiresAt:     expiresAt,
			CreatedAt:     createdAt,
		})
	}
	return out, iter.Close()
}

func (r *cassandraRepository) InsertAudit(ctx context.Context, a *domain.AuditRecord) error {
	q := `INSERT INTO delegation_audit (grant_id, used_at, audit_id, delegate_user_id, action, resource, allowed, ip_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(q,
		gocql.UUID(a.GrantID), a.UsedAt, gocql.UUID(a.AuditID), gocql.UUID(a.DelegateUserID),
		a.Action, a.Resource, a.Allowed, a.IPAddress,
	).WithContext(ctx).Exec()
}

func (r *cassandraRepository) ListAudit(ctx context.Context, grantID uuid.UUID, limit int) ([]*domain.AuditRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT grant_id, used_at, audit_id, delegate_user_id, action, resource, allowed, ip_address
		FROM delegation_audit WHERE grant_id = ? LIMIT ?`
	iter := r.session.Query(q, gocql.UUID(grantID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	out := make([]*domain.AuditRecord, 0)
	var gid, aid, did gocql.UUID
	var action, resource, ip string
	var usedAt time.Time
	var allowed bool
	for iter.Scan(&gid, &usedAt, &aid, &did, &action, &resource, &allowed, &ip) {
		out = append(out, &domain.AuditRecord{
			GrantID:        uuid.UUID(gid),
			UsedAt:         usedAt,
			AuditID:        uuid.UUID(aid),
			DelegateUserID: uuid.UUID(did),
			Action:         action,
			Resource:       resource,
			Allowed:        allowed,
			IPAddress:      ip,
		})
	}
	return out, iter.Close()
}
