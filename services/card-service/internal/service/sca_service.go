package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/services/card-service/internal/clients"
	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/events"
	"github.com/nexora/nexora/shared/schemes"
)

// ── PSD2 strong customer authentication (3-D Secure step-up) ────────────────
//
// The authorization path gains one state between the risk decision and the
// funds hold:
//
//	tap/online -> risk decision APPROVE -> SCA policy
//	              ├─ exemption applies ──> hold funds -> APPROVED
//	              └─ no exemption ──────> CHALLENGED (no hold)
//	                                        cardholder answers the code
//	                                        ├─ correct ──> hold funds -> APPROVED
//	                                        └─ wrong/expired ──> DECLINED (no hold)
//
// Two properties are deliberate:
//
//  1. No funds are reserved while the cardholder is authenticating, so an
//     abandoned challenge can never freeze the customer's money.
//  2. The challenge is consumed (single use, atomically) *before* the hold is
//     taken, so a replayed correct code cannot take a second hold.

// evaluateSCA decides whether this presentment must step up, returning the
// exemption an approval may rely on when it does not.
func (s *AuthorizationService) evaluateSCA(ctx context.Context, card *domain.Card, req *domain.AuthorizeCardRequest, decision *clients.FraudDecision) (schemes.SCAExemption, bool) {
	if !s.scaPolicy.Enabled || s.challenges == nil {
		return schemes.SCAExemptionIssuerDisabled, false
	}
	sc := schemes.SCAContext{
		AmountMinor:      req.Amount,
		Currency:         req.Currency,
		MerchantCategory: req.MerchantCategory,
		MerchantCountry:  req.MerchantCountry,
		CardPresent:      !req.IsEcommerce(),
		RiskScore:        decision.RiskScore,
	}
	// A merchant-initiated claim is verified against the merchant's own
	// history before the exemption is granted.
	if req.MerchantInitiated && s.hasPriorCaptured(ctx, card.CardID, req.Merchant) {
		sc.MerchantInitiated = true
	}
	exemption := s.scaPolicy.Evaluate(sc)
	return exemption, exemption == schemes.SCAExemptionNone
}

// hasPriorCaptured reports whether this card already has a captured presentment
// at the merchant. It is the evidence behind the merchant-initiated exemption:
// a follow-on bill is only exempt when the initial payment really happened.
func (s *AuthorizationService) hasPriorCaptured(ctx context.Context, cardID uuid.UUID, merchant string) bool {
	if strings.TrimSpace(merchant) == "" {
		return false
	}
	prior, err := s.authRepo.GetByCardID(ctx, cardID, 50)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not read presentment history; withholding the merchant-initiated exemption")
		return false
	}
	for _, a := range prior {
		if a.Status == domain.AuthStatusCaptured && strings.EqualFold(strings.TrimSpace(a.Merchant), strings.TrimSpace(merchant)) {
			return true
		}
	}
	return false
}

// raiseChallenge opens a step-up for a presentment the issuer cannot exempt.
func (s *AuthorizationService) raiseChallenge(ctx context.Context, card *domain.Card, req *domain.AuthorizeCardRequest, authID uuid.UUID, now time.Time, started time.Time, decision *clients.FraudDecision) (*domain.CardAuthorization, error) {
	otp, err := s.otpGenerator()
	if err != nil {
		// No code means no way to authenticate: fail closed without holding
		// anything, exactly like an unavailable risk engine.
		s.logger.Error().Err(err).Msg("could not mint a step-up one-time code")
		return s.failPresentment(ctx, card, req, authID, now, started, "sca_unavailable")
	}

	challenge := &domain.SCAChallenge{
		ChallengeID:     uuid.New(),
		AuthorizationID: authID,
		CardID:          card.CardID,
		UserID:          card.UserID,
		AccountID:       card.AccountID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Merchant:        req.Merchant,
		OTPHash:         schemes.HashOTP(otp),
		Status:          schemes.ChallengePending,
		MaxAttempts:     schemes.MaxOTPAttempts,
		ExpiresAt:       now.Add(schemes.OTPTTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	// The challenge row is written first: it is the record the cardholder will
	// answer, and an orphaned challenge (auth insert fails afterwards) is
	// inert — it simply expires.
	if err := s.challenges.Create(ctx, challenge); err != nil {
		return nil, fmt.Errorf("persisting sca challenge: %w", err)
	}

	latency := time.Since(started).Milliseconds()
	auth := &domain.CardAuthorization{
		AuthorizationID:  authID,
		CardID:           card.CardID,
		UserID:           card.UserID,
		AccountID:        card.AccountID,
		Amount:           req.Amount,
		Currency:         req.Currency,
		Merchant:         req.Merchant,
		MerchantCategory: req.MerchantCategory,
		MerchantCity:     req.MerchantCity,
		MerchantCountry:  req.MerchantCountry,
		Latitude:         req.Latitude,
		Longitude:        req.Longitude,
		TerminalID:       req.TerminalID,
		Decision:         domain.AuthDecisionChallenge,
		RiskScore:        decision.RiskScore,
		RiskAction:       decision.Decision,
		RiskLevel:        decision.RiskLevel,
		RiskReasons:      flattenReasons(decision.Reasons),
		ChallengeID:      challenge.ChallengeID,
		Status:           domain.AuthStatusChallenged,
		LatencyMs:        latency,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.authRepo.Create(ctx, auth); err != nil {
		return nil, fmt.Errorf("persisting challenged authorization: %w", err)
	}

	// The code travels on the internal event to the notification pipeline; it
	// is never logged and never returned to the merchant.
	s.publishChallenge(ctx, events.EventTypeAuthChallenged, auth, challenge, otp)

	s.logger.Info().
		Str("authorization_id", authID.String()).
		Str("challenge_id", challenge.ChallengeID.String()).
		Str("card_id", card.CardID.String()).
		Str("merchant", req.Merchant).
		Int64("amount", req.Amount).
		Time("expires_at", challenge.ExpiresAt).
		Msg("card presentment requires strong customer authentication")
	return auth, nil
}

// CompleteSCAChallenge answers an outstanding step-up with the one-time code
// the cardholder received. Outcomes:
//
//	correct code      -> APPROVED with the funds hold taken now
//	wrong code        -> ErrChallengeOTP (attempt consumed, still answerable)
//	attempts used up  -> DECLINED (sca_failed), no funds held
//	expired           -> DECLINED (sca_challenge_expired), no funds held
//	already answered  -> ErrChallengeStale
func (s *AuthorizationService) CompleteSCAChallenge(ctx context.Context, cardID uuid.UUID, authID uuid.UUID, req *domain.CompleteSCAChallengeRequest) (*domain.CardAuthorization, error) {
	if s.challenges == nil {
		return nil, domain.ErrSCANotConfigured
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}

	auth, err := s.authRepo.GetByID(ctx, authID)
	if err != nil {
		return nil, err
	}
	if auth.CardID != cardID {
		return nil, domain.ErrAuthCardMismatch
	}
	if auth.Status != domain.AuthStatusChallenged {
		return nil, domain.ErrAuthNotChallenged
	}

	challenge, err := s.challenges.GetByID(ctx, req.ChallengeID)
	if err != nil {
		return nil, err
	}
	if challenge.AuthorizationID != authID {
		// A challenge belongs to exactly one presentment: answering a
		// different one would authenticate an amount/payee the cardholder
		// never saw.
		return nil, domain.ErrChallengeNotFound
	}

	now := time.Now().UTC()
	expectedAttempts := challenge.Attempts
	verifyErr := challenge.Verify(strings.TrimSpace(req.OTP), now)
	if verifyErr != nil {
		// Persist the attempt outcome first: the counter is what bounds a
		// brute-force search of the code space, so it must land even if the
		// decline below fails.
		if err := s.persistChallenge(ctx, challenge, expectedAttempts); err != nil {
			return nil, err
		}
		switch {
		case errors.Is(verifyErr, schemes.ErrChallengeNotPending):
			return nil, domain.ErrChallengeStale
		case errors.Is(verifyErr, schemes.ErrChallengeExpired):
			return s.declineChallenged(ctx, auth, "sca_challenge_expired")
		case errors.Is(verifyErr, schemes.ErrOTPAttemptsExhausted):
			return s.declineChallenged(ctx, auth, "sca_failed")
		default:
			return nil, fmt.Errorf("%w: %v", domain.ErrChallengeOTP, verifyErr)
		}
	}

	// Correct code: consume the challenge before touching money, so a replayed
	// successful answer can never take a second hold.
	if err := s.persistChallenge(ctx, challenge, expectedAttempts); err != nil {
		return nil, err
	}

	// Hold the funds now that the cardholder has authenticated.
	reservation, err := s.ledger.Reserve(ctx, auth.AccountID, auth.Amount, auth.Currency, auth.AuthorizationID, "2m")
	if err != nil {
		reason := "ledger_unavailable"
		if clients.IsInsufficientFunds(err) {
			reason = "insufficient_funds"
		}
		// The step-up was answered but the money cannot be held: the
		// presentment declines, and the cardholder must present again (which
		// mints a fresh challenge — the code is deliberately single use).
		s.logger.Warn().Err(err).Str("authorization_id", authID.String()).Msg("step-up answered but the ledger would not hold funds")
		return s.declineChallenged(ctx, auth, reason)
	}

	if err := s.authRepo.UpdateChallengeSatisfied(ctx, authID, reservation.ReservationID); err != nil {
		// Lost a race (concurrent decline/void): hand the hold straight back
		// rather than stranding the customer's money.
		if relErr := s.ledger.ReleaseReservation(ctx, reservation.ReservationID); relErr != nil {
			s.logger.Error().Err(relErr).
				Str("authorization_id", authID.String()).
				Str("reservation_id", reservation.ReservationID.String()).
				Msg("failed to release an orphaned step-up hold")
		}
		return nil, err
	}

	auth.Status = domain.AuthStatusApproved
	auth.Decision = domain.AuthDecisionApprove
	auth.ReservationID = reservation.ReservationID
	auth.UpdatedAt = now
	s.publish(ctx, events.EventTypeAuthApproved, auth, nil)

	s.logger.Info().
		Str("authorization_id", authID.String()).
		Str("challenge_id", challenge.ChallengeID.String()).
		Str("card_id", cardID.String()).
		Int64("amount", auth.Amount).
		Msg("step-up complete; card authorized")
	return auth, nil
}

// ── Expiry sweep ────────────────────────────────────────────────────────────
//
// A step-up that is never answered must still reach an end: otherwise the
// presentment sits in CHALLENGED (holding no money) and the merchant waits on an
// authentication that can never arrive. The sweep is driven by a time-bucketed
// index (see domain.ExpiryBucket) because Cassandra cannot range-read a table by
// a non-key column, and it is safe to run concurrently and repeatedly: the
// challenge transition is single use, so whichever sweep or cardholder gets
// there first decides the outcome and the rest are no-ops.

// sweepLookback is how far back the sweep looks; it must comfortably cover the
// challenge TTL plus a missed tick.
const sweepLookback = schemes.OTPTTL + 30*time.Minute

// expiryBuckets lists the hourly buckets that can hold a deadline due at now.
func expiryBuckets(now time.Time) []string {
	seen := map[string]bool{}
	var buckets []string
	add := func(t time.Time) {
		b := domain.ExpiryBucket(t)
		if !seen[b] {
			seen[b] = true
			buckets = append(buckets, b)
		}
	}
	for t := now.Add(-sweepLookback); !t.After(now); t = t.Add(time.Hour) {
		add(t)
	}
	add(now)
	return buckets
}

// SweepExpiredChallenges declines presentments whose step-up expired unanswered
// and returns how many it closed.
func (s *AuthorizationService) SweepExpiredChallenges(ctx context.Context, now time.Time) (int, error) {
	if s.challenges == nil {
		return 0, domain.ErrSCANotConfigured
	}
	due, err := s.challenges.ListDueExpiring(ctx, expiryBuckets(now), now)
	if err != nil {
		return 0, fmt.Errorf("listing due step-ups: %w", err)
	}

	closed := 0
	for _, entry := range due {
		challenge, err := s.challenges.GetByID(ctx, entry.ChallengeID)
		if err != nil {
			if errors.Is(err, domain.ErrChallengeNotFound) {
				_ = s.challenges.ClearExpiry(ctx, entry)
				continue
			}
			return closed, fmt.Errorf("loading step-up %s: %w", entry.ChallengeID, err)
		}
		if !challenge.Pending() {
			// Answered, failed or already swept: the index entry is stale.
			_ = s.challenges.ClearExpiry(ctx, entry)
			continue
		}

		expectedAttempts := challenge.Attempts
		if err := challenge.Expire(now); err != nil {
			if errors.Is(err, schemes.ErrChallengeNotExpired) {
				continue // clock skew: the next sweep will pick it up
			}
			_ = s.challenges.ClearExpiry(ctx, entry)
			continue
		}

		applied, err := s.challenges.Transition(ctx, challenge, expectedAttempts)
		if err != nil {
			return closed, fmt.Errorf("closing step-up %s: %w", entry.ChallengeID, err)
		}
		if !applied {
			// The cardholder answered while we were looking; their decision stands.
			_ = s.challenges.ClearExpiry(ctx, entry)
			continue
		}

		auth, err := s.authRepo.GetByID(ctx, challenge.AuthorizationID)
		if err == nil && auth.Status == domain.AuthStatusChallenged {
			if _, err := s.declineChallenged(ctx, auth, "sca_challenge_expired"); err != nil {
				s.logger.Warn().Err(err).
					Str("authorization_id", challenge.AuthorizationID.String()).
					Msg("could not decline an expired step-up")
			}
		}
		_ = s.challenges.ClearExpiry(ctx, entry)
		closed++
	}

	if closed > 0 {
		s.logger.Info().Int("closed", closed).Msg("expiry sweep declined unanswered step-ups")
	}
	return closed, nil
}

// GetSCAChallenge returns the step-up attached to a presentment so the app can
// render the challenge state. The one-time code digest is never serialised.
func (s *AuthorizationService) GetSCAChallenge(ctx context.Context, authID uuid.UUID) (*domain.SCAChallenge, error) {
	if s.challenges == nil {
		return nil, domain.ErrSCANotConfigured
	}
	auth, err := s.authRepo.GetByID(ctx, authID)
	if err != nil {
		return nil, err
	}
	if auth.ChallengeID == uuid.Nil {
		return nil, domain.ErrChallengeNotFound
	}
	return s.challenges.GetByID(ctx, auth.ChallengeID)
}

// persistChallenge writes a verification outcome with the single-use guard.
func (s *AuthorizationService) persistChallenge(ctx context.Context, ch *domain.SCAChallenge, expectedAttempts int) error {
	applied, err := s.challenges.Transition(ctx, ch, expectedAttempts)
	if err != nil {
		return fmt.Errorf("persisting sca challenge state: %w", err)
	}
	if !applied {
		// Another answer (or a sweep) won the lightweight transaction.
		return domain.ErrChallengeStale
	}
	return nil
}

// declineChallenged closes a presentment whose step-up failed.
func (s *AuthorizationService) declineChallenged(ctx context.Context, auth *domain.CardAuthorization, reason string) (*domain.CardAuthorization, error) {
	if err := s.authRepo.UpdateChallengeDeclined(ctx, auth.AuthorizationID, reason); err != nil {
		return nil, err
	}
	auth.Status = domain.AuthStatusDeclined
	auth.Decision = domain.AuthDecisionDecline
	auth.DeclineReason = reason
	auth.UpdatedAt = time.Now().UTC()
	s.publish(ctx, events.EventTypeAuthDeclined, auth, nil)

	s.logger.Info().
		Str("authorization_id", auth.AuthorizationID.String()).
		Str("reason", reason).
		Msg("card presentment declined after step-up")
	return auth, nil
}

// publishChallenge emits the step-up event, carrying the one-time code for the
// notification pipeline (in-app push / SMS). The merchant response never
// contains the code.
func (s *AuthorizationService) publishChallenge(ctx context.Context, eventType events.EventType, auth *domain.CardAuthorization, ch *domain.SCAChallenge, otp string) {
	ev := &events.AuthorizationEvent{
		AuthorizationID:  auth.AuthorizationID.String(),
		CardID:           auth.CardID.String(),
		UserID:           auth.UserID.String(),
		AccountID:        auth.AccountID.String(),
		Amount:           auth.Amount,
		Currency:         auth.Currency,
		Merchant:         auth.Merchant,
		MerchantCategory: auth.MerchantCategory,
		MerchantCity:     auth.MerchantCity,
		MerchantCountry:  auth.MerchantCountry,
		TerminalID:       auth.TerminalID,
		Status:           string(auth.Status),
		Decision:         string(auth.Decision),
		RiskScore:        auth.RiskScore,
		RiskReasons:      auth.RiskReasons,
		ChallengeID:      ch.ChallengeID.String(),
		ChallengeExpiry:  &ch.ExpiresAt,
		ChallengeOTP:     otp,
		LatencyMs:        auth.LatencyMs,
		CreatedAt:        auth.CreatedAt,
	}
	s.publishEvent(ctx, eventType, ev)
}
