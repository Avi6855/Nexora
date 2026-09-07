package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/card-service/internal/clients"
	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/events"
	"github.com/nexora/nexora/services/card-service/internal/repository"
)

// AuthorizationService implements the real-time card presentment lifecycle:
//
//	tap -> card state check -> real-time fraud decision (over stored history)
//	-> funds reservation in the ledger -> durable authorization record
//	-> Kafka event for the push/feed pipeline -> response to the merchant.
//
// The decision path is synchronous (sub-second) exactly like a card scheme
// authorisation; everything after the response (capture/void) is async.
type AuthorizationService struct {
	cardRepo   repository.CardRepository
	authRepo   repository.CardAuthorizationRepository
	fraud      *clients.FraudClient
	ledger     *clients.LedgerClient
	publisher  *events.KafkaEventPublisher
	logger     zerolog.Logger
	// accounts resolves the emergency-lockdown flag at account-service.
	// Nil in tests; lockdown enforcement degrades to card-level checks.
	accounts   *clients.AccountClient
}

// SetAccountClient enables the lockdown lookup on the authorization path.
func (s *AuthorizationService) SetAccountClient(c *clients.AccountClient) {
	s.accounts = c
}

func NewAuthorizationService(
	cardRepo repository.CardRepository,
	authRepo repository.CardAuthorizationRepository,
	fraud *clients.FraudClient,
	ledger *clients.LedgerClient,
	publisher *events.KafkaEventPublisher,
	logger zerolog.Logger,
) *AuthorizationService {
	return &AuthorizationService{
		cardRepo:  cardRepo,
		authRepo:  authRepo,
		fraud:     fraud,
		ledger:    ledger,
		publisher: publisher,
		logger:    logger,
	}
}

func (s *AuthorizationService) AuthorizeCard(ctx context.Context, cardID uuid.UUID, req *domain.AuthorizeCardRequest) (*domain.CardAuthorization, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	started := time.Now()

	card, err := s.cardRepo.GetByID(ctx, cardID)
	if err != nil {
		return nil, err
	}

	authID := uuid.New()
	now := time.Now().UTC()

	// Local fast-fail checks that need no further service calls.
	decline := func(reason string) (*domain.CardAuthorization, error) {
		latency := time.Since(started).Milliseconds()
		auth := &domain.CardAuthorization{
			AuthorizationID: authID,
			CardID:          card.CardID,
			UserID:          card.UserID,
			AccountID:       card.AccountID,
			Amount:          req.Amount,
			Currency:        req.Currency,
			Merchant:        req.Merchant,
			MerchantCategory: req.MerchantCategory,
			MerchantCity:    req.MerchantCity,
			MerchantCountry: req.MerchantCountry,
			Latitude:        req.Latitude,
			Longitude:       req.Longitude,
			TerminalID:      req.TerminalID,
			Decision:        domain.AuthDecisionDecline,
			DeclineReason:   reason,
			Status:          domain.AuthStatusDeclined,
			LatencyMs:       latency,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		if err := s.authRepo.Create(ctx, auth); err != nil {
			return nil, fmt.Errorf("persisting declined authorization: %w", err)
		}
		s.publish(ctx, events.EventTypeAuthDeclined, auth, nil)
		return auth, nil
	}

	if card.Status != domain.CardStatusActive {
		return decline("card_not_active")
	}
	if req.Currency != card.Currency {
		return decline("currency_mismatch")
	}

	// Emergency lockdown is checked before the user's own channel controls:
	// a locked account refuses every outbound presentment with a specific
	// reason so the app can show the "account locked" state.
	if s.accounts != nil {
		if locked, err := s.accounts.GetLockdown(ctx, card.AccountID); err == nil && locked {
			return decline("account_locked")
		}
	}

	// User-configured spending controls are enforced before anything else —
	// the user's own toggles are the fastest decline path (Monzo-style).
	if reason, blocked := channelBlocked(card, req, authID, now, started); blocked {
		return decline(reason)
	}

	// Real spend today (from actual ledger entries) and real card limits.
	spentToday, err := s.ledger.SpentToday(ctx, card.AccountID)
	if err != nil {
		s.logger.Warn().Err(err).Msg("could not compute spend today; continuing without limit signal")
	}

	decision, err := s.fraud.EvaluateAuthorization(ctx, &clients.EvaluateAuthorizationRequest{
		AuthorizationID:  authID.String(),
		UserID:           card.UserID.String(),
		AccountID:        card.AccountID.String(),
		CardID:           card.CardID.String(),
		Amount:           req.Amount,
		Currency:         req.Currency,
		Merchant:         req.Merchant,
		MerchantCategory: req.MerchantCategory,
		MerchantCity:     req.MerchantCity,
		MerchantCountry:  req.MerchantCountry,
		Latitude:         req.Latitude,
		Longitude:        req.Longitude,
		TerminalID:       req.TerminalID,
		DeviceID:         req.DeviceID,
		DailyLimit:       card.DailyLimit,
		MonthlyLimit:     card.MonthlyLimit,
		SpentToday:       spentToday,
	})
	if err != nil {
		// Fail closed: no risk decision -> no authorisation.
		return decline("risk_service_unavailable")
	}
	_ = decision

	if decision.Decision != "APPROVE" {
		reason := "declined_by_risk_engine"
		if decision.Decision == "REVIEW" {
			reason = "flagged_for_review"
		}
		auth, err := s.declineWithRisk(ctx, card, req, authID, now, started, reason, decision)
		return auth, err
	}

	// Reserve funds in the ledger — a real, balance-checked hold.
	reservation, err := s.ledger.Reserve(ctx, card.AccountID, req.Amount, req.Currency, authID, "2m")
	if err != nil {
		if clients.IsInsufficientFunds(err) {
			return decline("insufficient_funds")
		}
		// Ledger unavailable -> treat like no auth to be safe.
		return decline("ledger_unavailable")
	}

	latency := time.Since(started).Milliseconds()
	auth := &domain.CardAuthorization{
		AuthorizationID: authID,
		CardID:          card.CardID,
		UserID:          card.UserID,
		AccountID:       card.AccountID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Merchant:        req.Merchant,
		MerchantCategory: req.MerchantCategory,
		MerchantCity:    req.MerchantCity,
		MerchantCountry: req.MerchantCountry,
		Latitude:        req.Latitude,
		Longitude:       req.Longitude,
		TerminalID:      req.TerminalID,
		Decision:        domain.AuthDecisionApprove,
		RiskScore:       decision.RiskScore,
		RiskAction:      decision.Decision,
		RiskLevel:       decision.RiskLevel,
		RiskReasons:     flattenReasons(decision.Reasons),
		ReservationID:   reservation.ReservationID,
		Status:          domain.AuthStatusApproved,
		LatencyMs:       latency,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.authRepo.Create(ctx, auth); err != nil {
		return nil, fmt.Errorf("persisting authorization: %w", err)
	}

	s.publish(ctx, events.EventTypeAuthApproved, auth, nil)

	s.logger.Info().
		Str("authorization_id", authID.String()).
		Str("card_id", cardID.String()).
		Str("merchant", req.Merchant).
		Int64("amount", req.Amount).
		Int64("latency_ms", latency).
		Msg("card authorized")
	return auth, nil
}

// declineWithRisk persists a risk-engine decline with the real risk context.
func (s *AuthorizationService) declineWithRisk(ctx context.Context, card *domain.Card, req *domain.AuthorizeCardRequest, authID uuid.UUID, now time.Time, started time.Time, reason string, decision *clients.FraudDecision) (*domain.CardAuthorization, error) {
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
		Decision:         domain.AuthDecisionDecline,
		DeclineReason:    reason,
		RiskScore:        decision.RiskScore,
		RiskAction:       decision.Decision,
		RiskLevel:        decision.RiskLevel,
		RiskReasons:      flattenReasons(decision.Reasons),
		Status:           domain.AuthStatusDeclined,
		LatencyMs:        latency,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.authRepo.Create(ctx, auth); err != nil {
		return nil, fmt.Errorf("persisting declined authorization: %w", err)
	}
	s.publish(ctx, events.EventTypeAuthDeclined, auth, nil)
	return auth, nil
}

// CaptureAuthorization settles the merchant hold: funds really move in the
// ledger and the resulting balance is emitted for the mobile feed.
func (s *AuthorizationService) CaptureAuthorization(ctx context.Context, cardID uuid.UUID, authID uuid.UUID) (*domain.CardAuthorization, error) {
	auth, err := s.authRepo.GetByID(ctx, authID)
	if err != nil {
		return nil, err
	}
	if auth.CardID != cardID {
		return nil, domain.ErrAuthCardMismatch
	}
	if auth.Status != domain.AuthStatusApproved || auth.ReservationID == uuid.Nil {
		return nil, domain.ErrAuthNotCapturable
	}

	entry, err := s.ledger.SettleReservation(ctx, auth.ReservationID)
	if err != nil {
		return nil, fmt.Errorf("settling reservation: %w", err)
	}

	if err := s.authRepo.UpdateCapture(ctx, authID); err != nil {
		return nil, err
	}
	auth.Status = domain.AuthStatusCaptured
	now := time.Now().UTC()
	auth.CapturedAt = &now

	bal := entry.BalanceAfter
	ev := &events.AuthorizationEvent{
		AuthorizationID: auth.AuthorizationID.String(),
		CardID:          auth.CardID.String(),
		UserID:          auth.UserID.String(),
		AccountID:       auth.AccountID.String(),
		Amount:          auth.Amount,
		Currency:        auth.Currency,
		Merchant:        auth.Merchant,
		MerchantCategory: auth.MerchantCategory,
		MerchantCity:    auth.MerchantCity,
		MerchantCountry: auth.MerchantCountry,
		TerminalID:      auth.TerminalID,
		Status:          string(domain.AuthStatusCaptured),
		Decision:        string(auth.Decision),
		ReservationID:   auth.ReservationID.String(),
		BalanceAfter:    &bal,
		CreatedAt:       now,
	}
	s.publishEvent(ctx, events.EventTypeAuthCaptured, ev)

	s.logger.Info().
		Str("authorization_id", authID.String()).
		Int64("balance_after", bal).
		Msg("card authorization captured")
	return auth, nil
}

// VoidAuthorization releases the hold without moving money.
func (s *AuthorizationService) VoidAuthorization(ctx context.Context, cardID uuid.UUID, authID uuid.UUID) (*domain.CardAuthorization, error) {
	auth, err := s.authRepo.GetByID(ctx, authID)
	if err != nil {
		return nil, err
	}
	if auth.CardID != cardID {
		return nil, domain.ErrAuthCardMismatch
	}
	if auth.Status != domain.AuthStatusApproved || auth.ReservationID == uuid.Nil {
		return nil, domain.ErrAuthNotVoidable
	}

	if err := s.ledger.ReleaseReservation(ctx, auth.ReservationID); err != nil {
		return nil, fmt.Errorf("releasing reservation: %w", err)
	}
	if err := s.authRepo.UpdateVoid(ctx, authID); err != nil {
		return nil, err
	}
	auth.Status = domain.AuthStatusVoided
	now := time.Now().UTC()
	auth.VoidedAt = &now

	ev := &events.AuthorizationEvent{
		AuthorizationID: auth.AuthorizationID.String(),
		CardID:          auth.CardID.String(),
		UserID:          auth.UserID.String(),
		AccountID:       auth.AccountID.String(),
		Amount:          auth.Amount,
		Currency:        auth.Currency,
		Merchant:        auth.Merchant,
		MerchantCategory: auth.MerchantCategory,
		Status:          string(domain.AuthStatusVoided),
		Decision:        string(auth.Decision),
		ReservationID:   auth.ReservationID.String(),
		CreatedAt:       now,
	}
	s.publishEvent(ctx, events.EventTypeAuthVoided, ev)
	return auth, nil
}

func (s *AuthorizationService) GetAuthorizations(ctx context.Context, cardID uuid.UUID, limit int) ([]*domain.CardAuthorization, error) {
	return s.authRepo.GetByCardID(ctx, cardID, limit)
}

func (s *AuthorizationService) GetAuthorization(ctx context.Context, authID uuid.UUID) (*domain.CardAuthorization, error) {
	return s.authRepo.GetByID(ctx, authID)
}

func (s *AuthorizationService) publish(ctx context.Context, eventType events.EventType, auth *domain.CardAuthorization, balanceAfter *int64) {
	ev := &events.AuthorizationEvent{
		AuthorizationID: auth.AuthorizationID.String(),
		CardID:          auth.CardID.String(),
		UserID:          auth.UserID.String(),
		AccountID:       auth.AccountID.String(),
		Amount:          auth.Amount,
		Currency:        auth.Currency,
		Merchant:        auth.Merchant,
		MerchantCategory: auth.MerchantCategory,
		MerchantCity:    auth.MerchantCity,
		MerchantCountry: auth.MerchantCountry,
		TerminalID:      auth.TerminalID,
		Status:          string(auth.Status),
		Decision:        string(auth.Decision),
		DeclineReason:   auth.DeclineReason,
		RiskScore:       auth.RiskScore,
		RiskReasons:     auth.RiskReasons,
		ReservationID:   auth.ReservationID.String(),
		BalanceAfter:    balanceAfter,
		LatencyMs:       auth.LatencyMs,
		CreatedAt:       auth.CreatedAt,
	}
	s.publishEvent(ctx, eventType, ev)
}

func (s *AuthorizationService) publishEvent(ctx context.Context, eventType events.EventType, ev *events.AuthorizationEvent) {
	if s.publisher == nil {
		return
	}
	if err := s.publisher.PublishAuthorizationEvent(ctx, eventType, ev); err != nil {
		s.logger.Warn().Err(err).Str("event_type", string(eventType)).Msg("failed to publish authorization event")
	}
}

// channelBlocked reports which user-configured spending control (if any)
// declines this presentment. Runs before the risk engine: the user's own
// settings are the cheapest and most authoritative decline signal.
func channelBlocked(card *domain.Card, req *domain.AuthorizeCardRequest, authID uuid.UUID, now time.Time, started time.Time) (string, bool) {
	merchant := strings.ToLower(req.Merchant)
	ecommerce := strings.EqualFold(req.TerminalID, "ECOM") ||
		strings.Contains(merchant, "amazon") || strings.Contains(merchant, ".com") ||
		strings.Contains(merchant, "online") || strings.Contains(merchant, "app store") ||
		strings.Contains(merchant, "google play") || strings.Contains(merchant, "steam")
	if ecommerce && !card.OnlineEnabled() {
		return "online_payments_disabled", true
	}
	if strings.EqualFold(req.TerminalID, "ATM") && !card.ATMEnabled() {
		return "atm_withdrawals_disabled", true
	}
	if card.GamblingBlocked() {
		for _, kw := range []string{"bet", "casino", "poker", "bingo", "lottery", "gambling", "slots", "william hill", "ladbrokes", "sky bet", "paddy power", "bet365", "flutter"} {
			if strings.Contains(merchant, kw) {
				return "gambling_block_active", true
			}
		}
	}
	return "", false
}

func flattenReasons(reasons []string) string {
	if len(reasons) == 0 {
		return ""
	}
	data, err := json.Marshal(reasons)
	if err != nil {
		return ""
	}
	return string(data)
}
