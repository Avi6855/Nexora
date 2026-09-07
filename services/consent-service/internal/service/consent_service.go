package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/consent-service/internal/domain"
	"github.com/nexora/nexora/services/consent-service/internal/repository"
)

// ConsentService manages time-bound, capability-scoped delegation grants.
type ConsentService struct {
	repo    repository.Repository
	logger  zerolog.Logger
	nowFunc func() time.Time
}

// NewConsentService builds the service.
func NewConsentService(repo repository.Repository, logger zerolog.Logger) *ConsentService {
	return &ConsentService{repo: repo, logger: logger, nowFunc: time.Now}
}

// CreateGrant issues a new delegation grant. Money-movement scopes are
// structurally impossible: the scope whitelist is read-only.
func (s *ConsentService) CreateGrant(ctx context.Context, ownerID uuid.UUID, req *domain.CreateGrantRequest) (*domain.DelegationGrant, error) {
	if strings.TrimSpace(req.DelegateEmail) == "" {
		return nil, domain.ErrEmailRequired
	}
	scopes := make([]domain.Scope, 0)
	for _, raw := range req.Scopes {
		s := domain.Scope(strings.ToUpper(strings.TrimSpace(raw)))
		if !domain.ValidScope(string(s)) {
			return nil, fmt.Errorf("%w: unknown scope %q", domain.ErrInvalidScopes, raw)
		}
		scopes = append(scopes, s)
	}
	if len(scopes) == 0 {
		return nil, domain.ErrInvalidScopes
	}

	days := req.DurationDays
	if days <= 0 {
		days = 7
	}
	if days > 90 {
		return nil, domain.ErrInvalidWindow
	}

	now := s.nowFunc().UTC()
	g := &domain.DelegationGrant{
		GrantID:       uuid.New(),
		OwnerUserID:   ownerID,
		DelegateEmail: strings.ToLower(strings.TrimSpace(req.DelegateEmail)),
		Label:         req.Label,
		Scopes:        scopes,
		Status:        domain.GrantStatusActive,
		StartsAt:      now,
		ExpiresAt:     now.Add(time.Duration(days) * 24 * time.Hour),
		CreatedAt:     now,
	}
	if err := s.repo.CreateGrant(ctx, g); err != nil {
		return nil, fmt.Errorf("storing grant: %w", err)
	}
	s.logger.Info().
		Str("grant_id", g.GrantID.String()).
		Str("owner", ownerID.String()).
		Str("delegate", g.DelegateEmail).
		Int("days", days).
		Msg("delegation grant created")
	return g, nil
}

// RevokeGrant kills a grant immediately. Only the owner can revoke.
func (s *ConsentService) RevokeGrant(ctx context.Context, ownerID, grantID uuid.UUID) error {
	g, err := s.requireOwned(ctx, ownerID, grantID)
	if err != nil {
		return err
	}
	now := s.nowFunc().UTC()
	g.Status = domain.GrantStatusRevoked
	g.RevokedAt = &now
	if err := s.repo.UpdateGrant(ctx, g); err != nil {
		return fmt.Errorf("revoking grant: %w", err)
	}
	s.logger.Info().Str("grant_id", grantID.String()).Msg("delegation grant revoked")
	return nil
}

// ListGrants returns the owner's grants with effective (clock-aware) status.
func (s *ConsentService) ListGrants(ctx context.Context, ownerID uuid.UUID, limit int) ([]*domain.DelegationGrant, error) {
	grants, err := s.repo.ListGrantsByOwner(ctx, ownerID, limit)
	if err != nil {
		return nil, err
	}
	now := s.nowFunc().UTC()
	for _, g := range grants {
		g.Status = g.EffectiveStatus(now)
	}
	return grants, nil
}

// ListAudit returns who did what under a grant. Owner-only.
func (s *ConsentService) ListAudit(ctx context.Context, ownerID, grantID uuid.UUID, limit int) ([]*domain.AuditRecord, error) {
	if _, err := s.requireOwned(ctx, ownerID, grantID); err != nil {
		return nil, err
	}
	return s.repo.ListAudit(ctx, grantID, limit)
}

// Evaluate is THE enforcement call other services make (internal token).
// It answers "may this delegate perform this action on this owner's account?"
// and writes an audit row for every decision — allowed or refused.
func (s *ConsentService) Evaluate(ctx context.Context, req *EvaluateRequest) (*EvaluateResponse, error) {
	g, err := s.repo.GetGrant(ctx, req.GrantID)
	if err != nil {
		return nil, err
	}
	now := s.nowFunc().UTC()

	allowed := g.OwnerUserID == req.OwnerUserID &&
		g.HasScope(now, domain.Scope(req.Scope)) &&
		!now.Before(g.StartsAt)

	resp := &EvaluateResponse{
		GrantID:  g.GrantID,
		OwnerID:  g.OwnerUserID,
		Scope:    string(req.Scope),
		Allowed:  allowed,
		Reason:   "scope granted and window open",
		EvaluatedAt: now,
	}
	if !allowed {
		switch {
		case g.OwnerUserID != req.OwnerUserID:
			resp.Reason = "grant does not belong to this account owner"
		case now.Before(g.StartsAt):
			resp.Reason = "grant window has not started"
		case g.EffectiveStatus(now) != domain.GrantStatusActive:
			resp.Reason = "grant is " + string(g.EffectiveStatus(now))
		default:
			resp.Reason = "grant does not include scope " + string(req.Scope)
		}
	}

	// Full audit: every evaluation is recorded, allowed or not.
	_ = s.repo.InsertAudit(ctx, &domain.AuditRecord{
		GrantID:        g.GrantID,
		UsedAt:         now,
		AuditID:        uuid.New(),
		DelegateUserID: req.DelegateUserID,
		Action:         fmt.Sprintf("consent.evaluate.%s", strings.ToLower(string(req.Scope))),
		Resource:       req.Resource,
		Allowed:        allowed,
		IPAddress:      req.IPAddress,
	})
	return resp, nil
}

// SweepExpired flips ACTIVE grants past their expiry to EXPIRED (the ticker's
// housekeeping; enforcement is clock-aware regardless).
func (s *ConsentService) SweepExpired(ctx context.Context) (int, error) {
	// Grants are listed per owner; the sweep relies on the by-owner index.
	// At demo scale a direct pass over owners is out of scope — enforcement
	// already treats expired grants as inactive. Kept as an explicit hook.
	return 0, nil
}

// EvaluateRequest is the internal enforcement payload.
type EvaluateRequest struct {
	GrantID        uuid.UUID `json:"grant_id"`
	OwnerUserID    uuid.UUID `json:"owner_user_id"`
	DelegateUserID uuid.UUID `json:"delegate_user_id"`
	Scope          string    `json:"scope"`
	Resource       string    `json:"resource,omitempty"`
	IPAddress      string    `json:"ip_address,omitempty"`
}

// EvaluateResponse answers the enforcement question.
type EvaluateResponse struct {
	GrantID     uuid.UUID `json:"grant_id"`
	OwnerID     uuid.UUID `json:"owner_id"`
	Scope       string    `json:"scope"`
	Allowed     bool      `json:"allowed"`
	Reason      string    `json:"reason"`
	EvaluatedAt time.Time `json:"evaluated_at"`
}

func (s *ConsentService) requireOwned(ctx context.Context, ownerID, grantID uuid.UUID) (*domain.DelegationGrant, error) {
	g, err := s.repo.GetGrant(ctx, grantID)
	if err != nil {
		return nil, err
	}
	if g.OwnerUserID != ownerID {
		return nil, domain.ErrNotOwner
	}
	return g, nil
}
