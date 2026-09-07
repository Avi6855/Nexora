package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/dispute-service/internal/clients"
	"github.com/nexora/nexora/services/dispute-service/internal/domain"
	"github.com/nexora/nexora/services/dispute-service/internal/repository"
)

// DisputeService runs the dispute workflow: intake → eligibility → evidence →
// merchant response → review → resolution, with timeout-driven progression and
// real ledger adjustments on resolution.
type DisputeService struct {
	repo    repository.Repository
	ledger  *clients.LedgerClient
	logger  zerolog.Logger
	nowFunc func() time.Time
}

// NewDisputeService builds the service.
func NewDisputeService(repo repository.Repository, ledger *clients.LedgerClient, logger zerolog.Logger) *DisputeService {
	return &DisputeService{repo: repo, ledger: ledger, logger: logger, nowFunc: time.Now}
}

// ── Intake ──────────────────────────────────────────────────────────────────

// CreateDispute validates the disputed entry against the ledger, runs the
// eligibility engine and opens the case (or explains why it can't).
func (s *DisputeService) CreateDispute(ctx context.Context, userID uuid.UUID, req *domain.CreateDisputeRequest) (*domain.DisputeCase, error) {
	entryID, err := uuid.Parse(req.EntryID)
	if err != nil {
		return nil, fmt.Errorf("invalid entry_id: %w", err)
	}
	reason := domain.DisputeReason(req.Reason)
	switch reason {
	case domain.ReasonFraud, domain.ReasonNotReceived, domain.ReasonNotAsDescribed,
		domain.ReasonDuplicate, domain.ReasonIncorrectAmount, domain.ReasonCancelled:
	default:
		return nil, fmt.Errorf("invalid reason (use FRAUD, NOT_RECEIVED, NOT_AS_DESCRIBED, DUPLICATE, INCORRECT_AMOUNT, CANCELLED_RECURRING)")
	}

	// Exactly-one-open-case per transaction.
	existing, err := s.repo.GetCaseByEntry(ctx, entryID)
	if err != nil {
		return nil, fmt.Errorf("checking existing disputes: %w", err)
	}
	if existing != nil && existing.Status == domain.CaseStatusOpen {
		return nil, domain.ErrDuplicateDispute
	}

	// The disputed entry must be real. The ledger is the source of truth.
	entry, err := s.ledger.GetEntry(ctx, entryID)
	if err != nil {
		return nil, fmt.Errorf("verifying disputed entry: %w", err)
	}
	if entry.EntryType != "DEBIT" {
		return nil, fmt.Errorf("only outgoing payments (DEBIT) can be disputed")
	}
	entryAccount, err := uuid.Parse(entryAccountID(entry))
	if err != nil {
		return nil, fmt.Errorf("disputed entry has no resolvable account")
	}
	entryAt := entry.CreatedAt
	if entryAt.IsZero() {
		entryAt = s.nowFunc().UTC()
	}

	eligibility := domain.AssessEligibility(entry.Category, entry.Amount, entryAt, s.nowFunc().UTC())
	if !eligibility.Eligible {
		return nil, fmt.Errorf("%s", eligibility.Reason)
	}

	now := s.nowFunc().UTC()
	caseID := uuid.New()
	c := &domain.DisputeCase{
		CaseID:        caseID,
		UserID:        userID,
		AccountID:     entryAccount,
		EntryID:       entryID,
		TransactionID: parseOrNew(entry.Transaction),
		Amount:        entry.Amount,
		Currency:      entry.Currency,
		Merchant:      entry.Description,
		Category:      entry.Category,
		Reason:        reason,
		Description:   req.Description,
		Status:        domain.CaseStatusOpen,
		Stage:         domain.StageSubmitted,
		Eligibility:   eligibility,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Eligibility is immediate in the open path; evidence deadline becomes
	// the first deadline.
	c.Stage = domain.StageAwaitingEvidence
	c.Deadline = &eligibility.EvidenceDeadline

	if err := s.repo.CreateCase(ctx, c); err != nil {
		return nil, fmt.Errorf("storing dispute case: %w", err)
	}
	s.appendEvent(ctx, c.CaseID, "CASE_OPENED", "user:"+userID.String(),
		fmt.Sprintf("dispute opened for %s (£%.2f), reason %s", entry.Description, float64(entry.Amount)/100.0, reason))
	s.appendEvent(ctx, c.CaseID, "ELIGIBILITY_PASSED", "system", "scheme rules checked; evidence requested from customer")

	s.logger.Info().
		Str("case_id", caseID.String()).
		Str("entry_id", entryID.String()).
		Str("reason", string(reason)).
		Msg("dispute case opened")
	return c, nil
}

// ── Evidence ────────────────────────────────────────────────────────────────

// AddEvidence attaches a document to an open case. The first evidence moves
// the case to MERCHANT_RESPONSE (the workflow's long wait) with the merchant
// deadline.
func (s *DisputeService) AddEvidence(ctx context.Context, userID, caseID uuid.UUID, ev *domain.Evidence) (*domain.DisputeCase, error) {
	c, err := s.requireOpenCase(ctx, userID, caseID)
	if err != nil {
		return nil, err
	}
	if ev.EvidenceType == "" || (ev.Filename == "" && ev.Content == "") {
		return nil, domain.ErrInvalidEvidence
	}
	now := s.nowFunc().UTC()
	ev.EvidenceID = uuid.New()
	ev.CaseID = caseID
	ev.UploadedBy = userID
	ev.CreatedAt = now
	if err := s.repo.AddEvidence(ctx, ev); err != nil {
		return nil, fmt.Errorf("storing evidence: %w", err)
	}
	s.appendEvent(ctx, caseID, "EVIDENCE_ADDED", "user:"+userID.String(), ev.EvidenceType+": "+ev.Filename)

	// First evidence submits the case to the merchant/scheme.
	count, err := s.repo.CountEvidence(ctx, caseID)
	if err == nil && count == 1 && c.Stage == domain.StageAwaitingEvidence {
		if err := c.TransitionTo(domain.StageMerchantResponse, now); err == nil {
			deadline := s.nowFunc().UTC().Add(45 * 24 * time.Hour)
			c.Deadline = &deadline
			if err := s.repo.UpdateCase(ctx, c); err != nil {
				return nil, err
			}
			s.appendEvent(ctx, caseID, "SUBMITTED_TO_MERCHANT", "system", "evidence pack sent to merchant; awaiting response")
		}
	}
	return c, nil
}

// ── Resolution ──────────────────────────────────────────────────────────────

// ResolveDispute closes the case. REFUNDED / MERCHANT_REFUND book a real
// ledger credit back to the customer (idempotent by case id so retries and
// ticker races can never double-refund).
func (s *DisputeService) ResolveDispute(ctx context.Context, actor string, caseID uuid.UUID, resolution domain.Resolution, note string) (*domain.DisputeCase, error) {
	c, err := s.repo.GetCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if c.Status != domain.CaseStatusOpen {
		return nil, domain.ErrCaseNotOpen
	}
	switch resolution {
	case domain.ResolutionRefunded, domain.ResolutionRejected, domain.ResolutionMerchantRefund, domain.ResolutionWithdrawn:
	default:
		return nil, fmt.Errorf("invalid resolution")
	}

	now := s.nowFunc().UTC()
	if err := c.TransitionTo(domain.StageResolved, now); err != nil {
		return nil, err
	}
	c.Status = domain.CaseStatusResolved
	c.Resolution = resolution
	c.ResolvedAt = &now

	if resolution == domain.ResolutionRefunded || resolution == domain.ResolutionMerchantRefund {
		refundKey := "dispute-refund:" + c.CaseID.String()
		txnID, err := s.ledger.BookRefund(ctx, c.AccountID, c.Amount, c.Currency, refundKey,
			fmt.Sprintf("Dispute refund %s (%s)", c.CaseID.String()[:8], c.Merchant))
		if err != nil {
			// The case stays open; the ticker retries resolution.
			s.logger.Error().Err(err).Str("case_id", caseID.String()).Msg("refund booking failed; case stays open for retry")
			return nil, fmt.Errorf("booking refund: %w", err)
		}
		s.appendEvent(ctx, caseID, "REFUND_BOOKED", actor, "ledger transaction "+txnID)
	}

	if err := s.repo.UpdateCase(ctx, c); err != nil {
		return nil, err
	}
	s.appendEvent(ctx, caseID, "CASE_RESOLVED", actor, string(resolution)+": "+note)

	s.logger.Info().
		Str("case_id", caseID.String()).
		Str("resolution", string(resolution)).
		Msg("dispute resolved")
	return c, nil
}

// ── Timeout progression (the "long-running" part) ───────────────────────────

// ProcessDeadlines advances every open case whose stage deadline has passed:
// evidence overdue → escalate to review; merchant silent → escalate to review;
// review overdue → resolve for the customer (scheme-style default judgement).
// It is idempotent: transitions are guarded by the state machine.
func (s *DisputeService) ProcessDeadlines(ctx context.Context) (advanced int, err error) {
	open, err := s.repo.ListOpenCases(ctx, 500)
	if err != nil {
		return 0, err
	}
	now := s.nowFunc().UTC()
	for _, c := range open {
		if c.Deadline == nil || now.Before(*c.Deadline) {
			continue
		}
		switch c.Stage {
		case domain.StageAwaitingEvidence:
			// Customer never sent evidence: escalate for human/scheme review
			// rather than killing the case (Monzo keeps disputes open).
			if c.TransitionTo(domain.StageUnderReview, now) == nil {
				deadline := now.Add(30 * 24 * time.Hour)
				c.Deadline = &deadline
				if uerr := s.repo.UpdateCase(ctx, c); uerr == nil {
					s.appendEvent(ctx, c.CaseID, "ESCALATED_TO_REVIEW", "system", "evidence deadline passed; escalated to review")
					advanced++
				}
			}
		case domain.StageMerchantResponse:
			// Merchant didn't respond in the scheme window: chargeback proceeds.
			if c.TransitionTo(domain.StageUnderReview, now) == nil {
				deadline := now.Add(14 * 24 * time.Hour)
				c.Deadline = &deadline
				if uerr := s.repo.UpdateCase(ctx, c); uerr == nil {
					s.appendEvent(ctx, c.CaseID, "MERCHANT_TIMEOUT", "system", "merchant did not respond; proceeding to review")
					advanced++
				}
			}
		case domain.StageUnderReview:
			// Review window elapsed with no counter-evidence: default judgement
			// for the customer, refunding real money through the ledger.
			if _, rerr := s.ResolveDispute(ctx, "system", c.CaseID, domain.ResolutionRefunded, "resolved in customer's favour after review window"); rerr == nil {
				advanced++
			}
		}
	}
	return advanced, nil
}

// ── Reads ───────────────────────────────────────────────────────────────────

func (s *DisputeService) GetCase(ctx context.Context, userID, caseID uuid.UUID) (*domain.DisputeCase, error) {
	c, err := s.repo.GetCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if c.UserID != userID {
		return nil, domain.ErrNotOwner
	}
	return c, nil
}

func (s *DisputeService) ListCases(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.DisputeCase, error) {
	return s.repo.ListCasesByUser(ctx, userID, limit)
}

func (s *DisputeService) ListEvidence(ctx context.Context, userID, caseID uuid.UUID) ([]*domain.Evidence, error) {
	if _, err := s.GetCase(ctx, userID, caseID); err != nil {
		return nil, err
	}
	return s.repo.ListEvidence(ctx, caseID)
}

func (s *DisputeService) ListEvents(ctx context.Context, userID, caseID uuid.UUID) ([]*domain.CaseEvent, error) {
	if _, err := s.GetCase(ctx, userID, caseID); err != nil {
		return nil, err
	}
	return s.repo.ListEvents(ctx, caseID)
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (s *DisputeService) requireOpenCase(ctx context.Context, userID, caseID uuid.UUID) (*domain.DisputeCase, error) {
	c, err := s.repo.GetCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if c.UserID != userID {
		return nil, domain.ErrNotOwner
	}
	if c.Status != domain.CaseStatusOpen {
		return nil, domain.ErrCaseNotOpen
	}
	return c, nil
}

func (s *DisputeService) appendEvent(ctx context.Context, caseID uuid.UUID, eventType, actor, note string) {
	ev := &domain.CaseEvent{
		EventID:   uuid.New(),
		CaseID:    caseID,
		EventType: eventType,
		Actor:     actor,
		Note:      note,
		CreatedAt: s.nowFunc().UTC(),
	}
	if err := s.repo.AppendEvent(ctx, ev); err != nil {
		s.logger.Error().Err(err).Str("case_id", caseID.String()).Msg("failed to append case event")
	}
}

// entryAccountID extracts the account from the raw ledger JSON. The ledger
// entry endpoint returns the account in `account_id`; keep parsing tolerant.
func entryAccountID(e *clients.LedgerEntry) string {
	return e.AccountID
}

func parseOrNew(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.New()
	}
	return id
}
