package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/dispute-service/internal/domain"
)

// OrchestrationService runs per-kind orchestrated disputes: TRANSFER /
// DIRECT_DEBIT / CASH alongside CARD, with per-kind scheme rules,
// eligibility, status tracking and ledger-adjustment proposals that are
// returned for ledger-service (never auto-posted here).
type OrchestrationService struct {
	mu      sync.Mutex
	cases   map[string]*domain.OrchestratedDispute
	logger  zerolog.Logger
	nowFunc func() time.Time
}

// NewOrchestrationService builds the service.
func NewOrchestrationService(logger zerolog.Logger) *OrchestrationService {
	return &OrchestrationService{
		cases:   make(map[string]*domain.OrchestratedDispute),
		logger:  logger,
		nowFunc: time.Now,
	}
}

// Create opens an orchestrated dispute. Direct-debit indemnity claims that
// are eligible return immediately: status RETURNED with a balanced
// adjustment proposal. All other kinds open in AWAITING_EVIDENCE.
func (s *OrchestrationService) Create(ctx context.Context, kind domain.OrchestratedKind, userID, accountID string, amountMinor int64, currency string, txnAt time.Time) (*domain.OrchestratedDispute, error) {
	switch kind {
	case domain.OrchestratedKindCard, domain.OrchestratedKindTransfer, domain.OrchestratedKindDirectDebit, domain.OrchestratedKindCash:
	default:
		return nil, domain.ErrOrchestratedInvalidKind
	}
	if userID == "" || accountID == "" {
		return nil, fmt.Errorf("user_id and account_id are required")
	}
	if currency == "" {
		return nil, fmt.Errorf("currency is required")
	}
	now := s.nowFunc().UTC()
	if txnAt.IsZero() {
		txnAt = now
	}
	elig := domain.AssessOrchestratedEligibility(kind, amountMinor, txnAt, now)
	if !elig.Eligible {
		return nil, fmt.Errorf("%w: %s", domain.ErrOrchestratedIneligible, elig.Reason)
	}
	d := &domain.OrchestratedDispute{
		DisputeID: uuid.NewString(),
		Kind:      kind,
		UserID:    userID,
		AccountID: accountID,
		Amount:    amountMinor,
		Currency:  currency,
		TxnAt:     txnAt,
		Status:    domain.OrchestratedStatusAwaitingEvidence,
		Evidence:  []domain.OrchestratedEvidence{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if kind == domain.OrchestratedKindCard {
		d.Status = domain.OrchestratedStatusAwaitingEvidence
	}
	if elig.ImmediateReturn {
		// Direct-debit indemnity-style immediate return: no evidence, no
		// review — return the money via a proposed adjustment.
		d.Status = domain.OrchestratedStatusReturned
		d.Resolution = "RETURNED"
		d.Adjustment = domain.ProposeAdjustment(
			"adj-"+d.DisputeID, d.DisputeID, accountID, amountMinor, currency,
			fmt.Sprintf("direct-debit indemnity immediate return (%s)", elig.Reason), now,
		)
	}
	s.mu.Lock()
	s.cases[d.DisputeID] = d
	s.mu.Unlock()

	s.logger.Info().
		Str("dispute_id", d.DisputeID).
		Str("kind", string(kind)).
		Str("status", string(d.Status)).
		Bool("immediate_return", elig.ImmediateReturn).
		Msg("orchestrated dispute opened")
	return copyOrchestrated(d), nil
}

// AddEvidence attaches evidence after per-kind validation.
func (s *OrchestrationService) AddEvidence(ctx context.Context, disputeID, evType, filename string) (*domain.OrchestratedDispute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.cases[disputeID]
	if !ok {
		return nil, domain.ErrOrchestratedNotFound
	}
	if d.Status == domain.OrchestratedStatusResolved || d.Status == domain.OrchestratedStatusReturned {
		return nil, domain.ErrOrchestratedInvalidStatus
	}
	if !domain.ValidateOrchestratedEvidence(d.Kind, evType) {
		return nil, fmt.Errorf("%w: %q for kind %s", domain.ErrOrchestratedEvidence, evType, d.Kind)
	}
	now := s.nowFunc().UTC()
	d.Evidence = append(d.Evidence, domain.OrchestratedEvidence{
		EvidenceID: uuid.NewString(),
		Type:       evType,
		Filename:   filename,
		AddedAt:    now,
	})
	d.UpdatedAt = now
	s.logger.Info().
		Str("dispute_id", disputeID).
		Str("evidence_type", evType).
		Msg("orchestrated evidence added")
	return copyOrchestrated(d), nil
}

// Advance moves the case: actions are "submit" (evidence -> review),
// "resolve-won" (review -> resolved with adjustment proposal),
// "resolve-lost" (review/evidence -> resolved, no adjustment) and "return"
// (indemnity return with adjustment proposal).
func (s *OrchestrationService) Advance(ctx context.Context, disputeID, action string) (*domain.OrchestratedDispute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.cases[disputeID]
	if !ok {
		return nil, domain.ErrOrchestratedNotFound
	}
	now := s.nowFunc().UTC()
	var next domain.OrchestratedStatus
	switch action {
	case "submit":
		next = domain.OrchestratedStatusUnderReview
		if d.Status != domain.OrchestratedStatusAwaitingEvidence && d.Status != domain.OrchestratedStatusOpen {
			return nil, fmt.Errorf("%w: submit from %s", domain.ErrOrchestratedInvalidStatus, d.Status)
		}
		if !domain.HasRequiredEvidence(d.Kind, d.EvidenceTypes()) {
			return nil, fmt.Errorf("%w: cash disputes require ATM_RECEIPT before review", domain.ErrOrchestratedMissingEvidence)
		}
	case "resolve-won":
		next = domain.OrchestratedStatusResolved
		if d.Status != domain.OrchestratedStatusUnderReview && d.Status != domain.OrchestratedStatusAwaitingEvidence {
			return nil, fmt.Errorf("%w: resolve-won from %s", domain.ErrOrchestratedInvalidStatus, d.Status)
		}
		d.Resolution = "REFUNDED"
		d.Adjustment = domain.ProposeAdjustment(
			"adj-"+d.DisputeID+"-won", d.DisputeID, d.AccountID, d.Amount, d.Currency,
			fmt.Sprintf("%s dispute won; proposed ledger adjustment", string(d.Kind)), now,
		)
	case "resolve-lost":
		next = domain.OrchestratedStatusResolved
		if d.Status != domain.OrchestratedStatusUnderReview && d.Status != domain.OrchestratedStatusAwaitingEvidence {
			return nil, fmt.Errorf("%w: resolve-lost from %s", domain.ErrOrchestratedInvalidStatus, d.Status)
		}
		d.Resolution = "REJECTED"
	case "return":
		next = domain.OrchestratedStatusReturned
		if d.Kind != domain.OrchestratedKindDirectDebit {
			return nil, fmt.Errorf("%w: only DIRECT_DEBIT supports indemnity return", domain.ErrOrchestratedInvalidStatus)
		}
		if d.Status == domain.OrchestratedStatusResolved || d.Status == domain.OrchestratedStatusReturned {
			return nil, fmt.Errorf("%w: already terminal", domain.ErrOrchestratedInvalidStatus)
		}
		d.Resolution = "RETURNED"
		if d.Adjustment == nil {
			d.Adjustment = domain.ProposeAdjustment(
				"adj-"+d.DisputeID, d.DisputeID, d.AccountID, d.Amount, d.Currency,
				"direct-debit indemnity immediate return", now,
			)
		}
	default:
		return nil, fmt.Errorf("unknown action %q (use submit, resolve-won, resolve-lost, return)", action)
	}
	if !domain.CanAdvanceOrchestrated(d.Status, next) {
		return nil, fmt.Errorf("%w: %s -> %s", domain.ErrOrchestratedInvalidStatus, d.Status, next)
	}
	d.Status = next
	d.UpdatedAt = now
	s.logger.Info().
		Str("dispute_id", disputeID).
		Str("action", action).
		Str("status", string(next)).
		Msg("orchestrated dispute advanced")
	return copyOrchestrated(d), nil
}

// Get returns one orchestrated dispute.
func (s *OrchestrationService) Get(ctx context.Context, disputeID string) (*domain.OrchestratedDispute, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.cases[disputeID]
	if !ok {
		return nil, domain.ErrOrchestratedNotFound
	}
	return copyOrchestrated(d), nil
}

// Adjustment returns the ledger-adjustment proposal (never auto-posted).
func (s *OrchestrationService) Adjustment(ctx context.Context, disputeID string) (*domain.AdjustmentProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.cases[disputeID]
	if !ok {
		return nil, domain.ErrOrchestratedNotFound
	}
	if d.Adjustment == nil {
		return nil, domain.ErrOrchestratedNoAdjustment
	}
	cp := *d.Adjustment
	cp.Legs = append([]domain.AdjustmentLeg(nil), d.Adjustment.Legs...)
	return &cp, nil
}

func copyOrchestrated(d *domain.OrchestratedDispute) *domain.OrchestratedDispute {
	cp := *d
	cp.Evidence = append([]domain.OrchestratedEvidence(nil), d.Evidence...)
	if d.Adjustment != nil {
		adj := *d.Adjustment
		adj.Legs = append([]domain.AdjustmentLeg(nil), d.Adjustment.Legs...)
		cp.Adjustment = &adj
	}
	return &cp
}
