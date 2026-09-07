package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/ledger-service/internal/clients"
	"github.com/nexora/nexora/services/ledger-service/internal/domain"
)

// The ledger is append-only double-entry: every entry carries
// balance_before/balance_after, and the recomputed sum of credits minus debits
// must equal the newest entry's balance_after. The invariant monitor verifies
// both properties continuously:
//
//  1. CHAIN — each entry must continue from the previous one
//     (entries[i].balance_before == entries[i-1].balance_after, and
//     balance_after == balance_before ± amount). A mid-chain tamper breaks
//     this even when the final balance still matches.
//  2. RECOMPUTE — Σcredits − Σdebits must equal the latest balance_after
//     (the existing VerifyBalanceIntegrity check).
//
// On violation the monitor freezes the account (no new holds, transfers or
// double-entry bookings), writes a VIOLATION event, and files a SEV1 incident.
// When a later sweep finds the ledger repaired the guard self-heals: the
// account unfreezes and a CLEARED event closes the loop. Repairs are always
// manual (the monitor never mutates the ledger — it only stops it).

// SetIncidentReporter wires the optional ops integration (no-op when nil).
func (s *LedgerService) SetIncidentReporter(r clients.IncidentReporter) {
	s.incidents = r
}

// ensureAccountWritable refuses new money movement (holds, transfers,
// double-entry bookings) against an account that is under emergency lockdown
// OR frozen by the invariant monitor.
func (s *LedgerService) ensureAccountWritable(ctx context.Context, accountID uuid.UUID) error {
	locked, err := s.ledgerRepo.IsAccountLocked(ctx, accountID)
	if err != nil {
		s.logger.Warn().Err(err).Str("account_id", accountID.String()).Msg("lockdown check unavailable, skipping")
	} else if locked {
		return domain.ErrAccountLocked
	}

	guard, err := s.ledgerRepo.GetIntegrityGuard(ctx, accountID)
	if err != nil {
		s.logger.Warn().Err(err).Str("account_id", accountID.String()).Msg("integrity guard check unavailable, allowing booking")
		return nil
	}
	if guard != nil && guard.Frozen {
		return domain.ErrAccountIntegrityViolation
	}
	return nil
}

// checkAccount runs both invariants over the account's full entry set and
// reports the outcome (pure: no side effects).
func (s *LedgerService) checkAccount(ctx context.Context, accountID uuid.UUID) (chainOK, recomputeOK bool, firstBad uuid.UUID, message string, count int, err error) {
	entries, err := s.ledgerRepo.GetAllEntriesByAccount(ctx, accountID)
	if err != nil {
		return false, false, uuid.Nil, "", 0, fmt.Errorf("reading entries: %w", err)
	}
	count = len(entries)
	if count == 0 {
		return true, true, uuid.Nil, "no entries", 0, nil
	}

	// Entries cluster newest-first; walk in booking order.
	sorted := make([]*domain.LedgerEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })

	// 1. Chain continuity.
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if cur.BalanceBefore != prev.BalanceAfter {
			return false, false, cur.EntryID,
				fmt.Sprintf("balance chain broken at entry %s: expected balance_before %d, got %d (previous entry ended at %d)",
					cur.EntryID, prev.BalanceAfter, cur.BalanceBefore, prev.BalanceAfter), count, nil
		}
	}
	// 2. Per-entry arithmetic (balance_after == balance_before ± amount).
	for _, e := range sorted {
		var expected int64
		if e.EntryType == domain.EntryTypeDebit {
			expected = e.BalanceBefore - e.Amount
		} else {
			expected = e.BalanceBefore + e.Amount
		}
		if e.BalanceAfter != expected {
			return false, false, e.EntryID,
				fmt.Sprintf("entry %s arithmetic broken: balance_before %d %s %d should end at %d, recorded %d",
					e.EntryID, e.BalanceBefore, e.EntryType, e.Amount, expected, e.BalanceAfter), count, nil
		}
	}

	// 3. Recompute vs latest recorded balance.
	integrity, err := s.VerifyBalanceIntegrity(ctx, accountID)
	if err != nil {
		return false, false, uuid.Nil, "", count, fmt.Errorf("recompute check: %w", err)
	}
	if !integrity.IsBalanced {
		return false, false, sorted[len(sorted)-1].EntryID,
			fmt.Sprintf("recompute mismatch: Σcredits−Σdebits = %d but latest balance_after = %d",
				integrity.ComputedBalance, integrity.LatestBalance), count, nil
	}

	return true, true, uuid.Nil, "chain continuous and recompute balanced", count, nil
}

// ScanAccountIntegrity sweeps one account: freeze on violation, self-heal when
// a previously frozen account verifies clean again.
func (s *LedgerService) ScanAccountIntegrity(ctx context.Context, accountID uuid.UUID) (*domain.IntegrityScanResult, error) {
	chainOK, recomputeOK, firstBad, message, count, err := s.checkAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	result := &domain.IntegrityScanResult{
		AccountID:         accountID,
		EntriesChecked:    count,
		ChainContinuous:   chainOK,
		RecomputeBalanced: recomputeOK,
		Violation:         !chainOK || !recomputeOK,
		FirstBadEntryID:   firstBad,
		Message:           message,
	}

	guard, guardErr := s.ledgerRepo.GetIntegrityGuard(ctx, accountID)
	if guardErr != nil {
		return nil, fmt.Errorf("reading guard: %w", guardErr)
	}

	if result.Violation {
		if guard != nil && guard.Frozen {
			// Already frozen: keep the guard, do not spam incidents.
			result.Message = "still violated — guard remains active: " + message
			return result, nil
		}
		// File the incident first so its id lands on the guard row.
		incidentID := uuid.Nil
		if s.incidents != nil {
			if id, iErr := s.incidents.FileIntegrityIncident(ctx, accountID.String(), firstBad.String(), message); iErr == nil {
				incidentID, _ = uuid.Parse(id)
				result.IncidentCreated = true
			} else {
				s.logger.Error().Err(iErr).Str("account_id", accountID.String()).Msg("failed to file integrity incident")
			}
		}
		now := time.Now().UTC()
		if err := s.ledgerRepo.SetIntegrityGuard(ctx, &domain.IntegrityGuard{
			AccountID:  accountID,
			Frozen:     true,
			Reason:     message,
			IncidentID: incidentID,
			DetectedAt: now,
		}); err != nil {
			return nil, fmt.Errorf("setting integrity guard: %w", err)
		}
		result.GuardApplied = true
		if err := s.ledgerRepo.InsertIntegrityEvent(ctx, &domain.IntegrityEvent{
			AccountID:  accountID,
			EventID:    uuid.New(),
			EventType:  domain.IntegrityEventViolation,
			Message:    "Ledger integrity violation — account frozen",
			Detail:     message,
			DetectedAt: now,
		}); err != nil {
			s.logger.Error().Err(err).Str("account_id", accountID.String()).Msg("failed to record integrity event")
		}
		s.logger.Warn().Str("account_id", accountID.String()).Msg("account frozen by ledger invariant monitor")
		return result, nil
	}

	// Verified clean: self-heal a previously frozen account.
	if guard != nil && guard.Frozen {
		now := time.Now().UTC()
		if err := s.ledgerRepo.ClearIntegrityGuard(ctx, accountID); err != nil {
			return nil, fmt.Errorf("clearing integrity guard: %w", err)
		}
		if err := s.ledgerRepo.InsertIntegrityEvent(ctx, &domain.IntegrityEvent{
			AccountID:  accountID,
			EventID:    uuid.New(),
			EventType:  domain.IntegrityEventCleared,
			Message:    "Integrity restored — guard cleared",
			Detail:     "ledger verified continuous and balanced",
			DetectedAt: now,
			ClearedAt:  &now,
		}); err != nil {
			s.logger.Error().Err(err).Str("account_id", accountID.String()).Msg("failed to record cleared event")
		}
		s.logger.Info().Str("account_id", accountID.String()).Msg("integrity guard cleared after repair")
	}
	return result, nil
}

// ScanAllAccountsIntegrity sweeps every account with ledger entries. It is the
// periodic task the ticker drives (and POST /v1/ledger/integrity/scan).
func (s *LedgerService) ScanAllAccountsIntegrity(ctx context.Context) (*domain.IntegrityScanSummary, error) {
	accounts, err := s.ledgerRepo.ListEntryAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing accounts: %w", err)
	}
	summary := &domain.IntegrityScanSummary{CheckedAt: time.Now().UTC()}
	for _, accountID := range accounts {
		res, err := s.ScanAccountIntegrity(ctx, accountID)
		if err != nil {
			s.logger.Error().Err(err).Str("account_id", accountID.String()).Msg("integrity scan failed for account")
			continue
		}
		summary.AccountsScanned++
		summary.Results = append(summary.Results, res)
		if res.Violation {
			summary.Violations++
		}
		if res.GuardApplied {
			summary.GuardsApplied++
		}
		if res.IncidentCreated {
			summary.IncidentsRaised++
		}
	}
	return summary, nil
}

// ListIntegrityEvents reads an account's integrity trail (newest first).
func (s *LedgerService) ListIntegrityEvents(ctx context.Context, accountID uuid.UUID, limit int) ([]*domain.IntegrityEvent, error) {
	events, err := s.ledgerRepo.ListIntegrityEvents(ctx, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing integrity events: %w", err)
	}
	return events, nil
}

// ClearAccountGuard re-verifies an account and removes the freeze once the
// ledger is clean (used by engineers after repairing entries).
func (s *LedgerService) ClearAccountGuard(ctx context.Context, accountID uuid.UUID) (*domain.IntegrityScanResult, error) {
	res, err := s.ScanAccountIntegrity(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if res.Violation {
		return res, fmt.Errorf("%w: %s", domain.ErrAccountIntegrityViolation, res.Message)
	}
	return res, nil
}
