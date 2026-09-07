package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/replay-service/internal/clients"
	"github.com/nexora/nexora/services/replay-service/internal/domain"
	"github.com/nexora/nexora/services/replay-service/internal/events"
	"github.com/nexora/nexora/services/replay-service/internal/repository"
)

type ReplayService struct {
	repo     repository.ReplayRepository
	producer *events.KafkaProducer
	ledger   *clients.LedgerClient
	replays  map[uuid.UUID]*domain.ReplayRequest
	events   map[string][]domain.ReplayStep
	logger   zerolog.Logger
}

func NewReplayService(repo repository.ReplayRepository, producer *events.KafkaProducer, ledger *clients.LedgerClient, logger zerolog.Logger) *ReplayService {
	return &ReplayService{
		repo:     repo,
		producer: producer,
		ledger:   ledger,
		replays:  make(map[uuid.UUID]*domain.ReplayRequest),
		events:   make(map[string][]domain.ReplayStep),
		logger:   logger,
	}
}

func (s *ReplayService) CreateReplay(ctx context.Context, req *domain.CreateReplayRequest) (*domain.ReplayRequest, error) {
	s.logger.Info().Msg("creating replay request")

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, fmt.Errorf("invalid start_time: %w", err)
	}

	endTime, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		return nil, fmt.Errorf("invalid end_time: %w", err)
	}

	replay := &domain.ReplayRequest{
		ReplayID:    uuid.New(),
		StartTime:   startTime,
		EndTime:     endTime,
		EventTypes:  req.EventTypes,
		Status:      domain.ReplayStatusPending,
		CreatedAt:   time.Now().UTC(),
		EventsCount: 0,
	}

	s.replays[replay.ReplayID] = replay

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "replay.started", replay)
	}

	return replay, nil
}

func (s *ReplayService) GetReplay(ctx context.Context, id uuid.UUID) (*domain.ReplayRequest, error) {
	replay, ok := s.replays[id]
	if !ok {
		return nil, fmt.Errorf("replay not found")
	}
	return replay, nil
}

func (s *ReplayService) StartReplay(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("replay_id", id.String()).Msg("starting replay")
	replay, ok := s.replays[id]
	if !ok {
		return fmt.Errorf("replay not found")
	}
	replay.Status = domain.ReplayStatusRunning
	return nil
}

func (s *ReplayService) CompleteReplay(ctx context.Context, id uuid.UUID, eventsCount int) error {
	s.logger.Info().Str("replay_id", id.String()).Int("count", eventsCount).Msg("completing replay")
	replay, ok := s.replays[id]
	if !ok {
		return fmt.Errorf("replay not found")
	}
	now := time.Now().UTC()
	replay.Status = domain.ReplayStatusCompleted
	replay.CompletedAt = &now
	replay.EventsCount = eventsCount

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "replay.completed", replay)
	}

	return nil
}

func (s *ReplayService) FailReplay(ctx context.Context, id uuid.UUID) error {
	replay, ok := s.replays[id]
	if !ok {
		return fmt.Errorf("replay not found")
	}
	now := time.Now().UTC()
	replay.Status = domain.ReplayStatusFailed
	replay.CompletedAt = &now

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "replay.failed", replay)
	}

	return nil
}

func (s *ReplayService) ReplayTransaction(ctx context.Context, transactionID string) (*domain.ReplayResult, error) {
	s.logger.Info().Str("transaction_id", transactionID).Msg("replaying transaction")

	events, ok := s.events[transactionID]
	if !ok {
		events = s.generateSampleTransactionEvents(transactionID)
		s.events[transactionID] = events
	}

	state := make(map[string]interface{})
	var steps []domain.ReplayStep

	for _, step := range events {
		state["last_event"] = step.EventType
		state["last_timestamp"] = step.Timestamp
		state["event_count"] = len(steps) + 1

		stateJSON, _ := json.Marshal(state)
		appliedStep := step
		appliedStep.AppliedState = stateJSON
		steps = append(steps, appliedStep)
	}

	finalState, _ := json.Marshal(state)
	originalResult := json.RawMessage(fmt.Sprintf(`{"status":"%s","events":%d}`, state["last_event"], len(steps)))

	result := &domain.ReplayResult{
		ReplayID:       uuid.New(),
		OriginalResult: originalResult,
		ReplayedResult: finalState,
		Differences:    []domain.ReplayDifference{},
		Steps:          steps,
		Deterministic:  true,
		TotalEvents:    len(steps),
		ReplayedAt:     time.Now().UTC(),
	}

	_ = s.repo.StoreReplayResult(ctx, result)

	return result, nil
}

// ── Time-travel over the real ledger ────────────────────────────────────────

// TimeTravel reconstructs the account's state as it was at instant T from the
// append-only ledger. The ledger stores balance_after on every entry, so the
// authoritative answer is derived two independent ways and compared:
//  1. backwards: take the newest entry at/before T and read its balance_after
//  2. forwards: replay every entry up to T from zero (signed amounts)
//
// If both agree, the reconstruction is deterministic and the diff is empty —
// that's the time-travel debug story ("customer's balance at 14:37 was X and
// here is every entry that produced it").
func (s *ReplayService) TimeTravel(ctx context.Context, accountID uuid.UUID, at time.Time) (*domain.TimeTravelResult, error) {
	now := time.Now().UTC()
	if at.After(now.Add(1 * time.Minute)) {
		return nil, fmt.Errorf("snapshot time is in the future")
	}
	if s.ledger == nil {
		return nil, fmt.Errorf("ledger client not configured")
	}

	entries, err := s.ledger.GetEntries(ctx, accountID, 500)
	if err != nil {
		return nil, fmt.Errorf("reading ledger for time-travel: %w", err)
	}
	if entries == nil {
		entries = []clients.Entry{}
	}
	// Ledger returns newest-first; walk in booking order.
	sorted := make([]clients.Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt.Before(sorted[j].CreatedAt) })

	// Forwards replay: signed sum of every entry at/before the snapshot.
	var forward int64
	seen := 0
	steps := make([]domain.TimeTravelStep, 0, len(sorted))
	for _, e := range sorted {
		if e.CreatedAt.After(at) {
			continue
		}
		if e.EntryDirect == "DEBIT" {
			forward -= e.Amount
		} else {
			forward += e.Amount
		}
		seen++
		steps = append(steps, domain.TimeTravelStep{
			EntryID:      e.EntryID,
			EntryType:    e.EntryType,
			Description:  e.Description,
			Amount:       e.Amount,
			Direction:    e.EntryDirect,
			BalanceAfter: e.BalanceAfter,
			BookedAt:     e.CreatedAt,
		})
	}

	// Backwards read: balance_after of the newest entry at/before T.
	var backwards int64
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i].CreatedAt.After(at) {
			continue
		}
		backwards = sorted[i].BalanceAfter
		break
	}
	// Zero entries at snapshot → state was zero either way.

	deterministic := forward == backwards
	ledgerIsLive := seen == len(sorted)

	s.logger.Info().
		Str("account_id", accountID.String()).
		Time("snapshot_at", at).
		Int64("balance", backwards).
		Bool("deterministic", deterministic).
		Int("entries_seen", seen).
		Msg("time-travel reconstruction complete")

	result := &domain.TimeTravelResult{
		AccountID:    accountID,
		SnapshotAt:   at,
		Balance:      backwards,
		EntriesSeen:  seen,
		LedgerIsLive: ledgerIsLive,
		Steps:        steps,
		ReplayedAt:   now,
	}
	if !deterministic {
		// Forward replay disagrees with the stored running balance: surface
		// both instead of failing, exactly what a debugging session wants.
		result.Divergence = &domain.ReplayDifference{
			Field:    "balance",
			Original: fmt.Sprintf("%d", backwards),
			Replayed: fmt.Sprintf("%d", forward),
		}
	}

	_ = s.repo.StoreReplayResult(ctx, &domain.ReplayResult{
		ReplayID:       uuid.New(),
		OriginalResult: json.RawMessage(fmt.Sprintf(`{"account_id":%q,"snapshot_at":%q,"balance":%d}`, accountID, at.Format(time.RFC3339), backwards)),
		ReplayedResult: json.RawMessage(fmt.Sprintf(`{"forward_balance":%d,"entries":%d}`, forward, seen)),
		Differences:    func() []domain.ReplayDifference { if !deterministic { return []domain.ReplayDifference{*result.Divergence} }; return []domain.ReplayDifference{} }(),
		Steps:          []domain.ReplayStep{},
		Deterministic:  deterministic,
		TotalEvents:    seen,
		ReplayedAt:     now,
	})

	return result, nil
}

func (s *ReplayService) ReplayAccount(ctx context.Context, accountID, fromTime, toTime string) (*domain.ReplayResult, error) {
	s.logger.Info().Str("account_id", accountID).Msg("replaying account state")

	from, err := time.Parse(time.RFC3339, fromTime)
	if err != nil {
		return nil, fmt.Errorf("invalid from_time: %w", err)
	}
	to, err := time.Parse(time.RFC3339, toTime)
	if err != nil {
		return nil, fmt.Errorf("invalid to_time: %w", err)
	}

	events := s.generateAccountEvents(accountID, from, to)

	balance := int64(0)
	available := int64(0)
	var steps []domain.ReplayStep

	for _, step := range events {
		var payload map[string]interface{}
		json.Unmarshal(step.Payload, &payload)

		if amount, ok := payload["amount"].(float64); ok {
			if step.EventType == "ACCOUNT_FUNDED" || step.EventType == "TRANSFER_IN" {
				balance += int64(amount)
				available += int64(amount)
			} else if step.EventType == "PAYMENT_DEBITED" || step.EventType == "TRANSFER_OUT" {
				balance -= int64(amount)
				available -= int64(amount)
			}
		}

		state := map[string]interface{}{
			"balance":   balance,
			"available": available,
		}
		stateJSON, _ := json.Marshal(state)
		appliedStep := step
		appliedStep.AppliedState = stateJSON
		steps = append(steps, appliedStep)
	}

	finalState, _ := json.Marshal(map[string]interface{}{
		"balance":   balance,
		"available": available,
	})

	result := &domain.ReplayResult{
		ReplayID:       uuid.New(),
		OriginalResult: finalState,
		ReplayedResult: finalState,
		Differences:    []domain.ReplayDifference{},
		Steps:          steps,
		Deterministic:  true,
		TotalEvents:    len(steps),
		ReplayedAt:     time.Now().UTC(),
	}

	_ = s.repo.StoreReplayResult(ctx, result)

	return result, nil
}

func (s *ReplayService) ReplayPayment(ctx context.Context, paymentID string) (*domain.ReplayResult, error) {
	s.logger.Info().Str("payment_id", paymentID).Msg("replaying payment lifecycle")

	events := s.generatePaymentEvents(paymentID)

	state := make(map[string]interface{})
	state["payment_id"] = paymentID
	var steps []domain.ReplayStep

	for _, step := range events {
		var payload map[string]interface{}
		json.Unmarshal(step.Payload, &payload)

		if status, ok := payload["status"].(string); ok {
			state["current_status"] = status
		}
		state["last_updated"] = step.Timestamp
		state["step_count"] = len(steps) + 1

		stateJSON, _ := json.Marshal(state)
		appliedStep := step
		appliedStep.AppliedState = stateJSON
		steps = append(steps, appliedStep)
	}

	originalResult, _ := json.Marshal(map[string]interface{}{
		"payment_id":   paymentID,
		"final_status": state["current_status"],
		"total_steps":  len(steps),
	})

	replayedResult, _ := json.Marshal(state)

	result := &domain.ReplayResult{
		ReplayID:       uuid.New(),
		OriginalResult: originalResult,
		ReplayedResult: replayedResult,
		Differences:    []domain.ReplayDifference{},
		Steps:          steps,
		Deterministic:  true,
		TotalEvents:    len(steps),
		ReplayedAt:     time.Now().UTC(),
	}

	_ = s.repo.StoreReplayResult(ctx, result)

	return result, nil
}

func (s *ReplayService) generateSampleTransactionEvents(transactionID string) []domain.ReplayStep {
	now := time.Now().UTC()
	return []domain.ReplayStep{
		{StepID: 1, EventType: "PAYMENT_CREATED", Timestamp: now.Add(-5 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"CREATED","amount":5000,"currency":"GBP"}`, transactionID))},
		{StepID: 2, EventType: "FRAUD_CHECK_PASSED", Timestamp: now.Add(-4 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","risk_score":0.1,"action":"ALLOW"}`, transactionID))},
		{StepID: 3, EventType: "PAYMENT_AUTHORIZED", Timestamp: now.Add(-3 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"AUTHORIZED","authorization_code":"AUTH123"}`, transactionID))},
		{StepID: 4, EventType: "PAYMENT_PROCESSING", Timestamp: now.Add(-2 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"PROCESSING"}`, transactionID))},
		{StepID: 5, EventType: "PAYMENT_CONFIRMED", Timestamp: now.Add(-1 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"CONFIRMED","provider_ref":"PRV-001"}`, transactionID))},
	}
}

func (s *ReplayService) generateAccountEvents(accountID string, from, to time.Time) []domain.ReplayStep {
	events := []domain.ReplayStep{
		{StepID: 1, EventType: "ACCOUNT_FUNDED", Timestamp: from.Add(1 * time.Hour), Payload: json.RawMessage(`{"account_id":"` + accountID + `","amount":10000,"currency":"GBP"}`)},
		{StepID: 2, EventType: "PAYMENT_DEBITED", Timestamp: from.Add(2 * time.Hour), Payload: json.RawMessage(`{"account_id":"` + accountID + `","amount":2500,"currency":"GBP","payment_id":"pay-001"}`)},
		{StepID: 3, EventType: "TRANSFER_IN", Timestamp: from.Add(3 * time.Hour), Payload: json.RawMessage(`{"account_id":"` + accountID + `","amount":5000,"currency":"GBP"}`)},
		{StepID: 4, EventType: "PAYMENT_DEBITED", Timestamp: from.Add(4 * time.Hour), Payload: json.RawMessage(`{"account_id":"` + accountID + `","amount":1500,"currency":"GBP","payment_id":"pay-002"}`)},
	}

	var filtered []domain.ReplayStep
	for _, e := range events {
		if (e.Timestamp.Equal(from) || e.Timestamp.After(from)) && (e.Timestamp.Equal(to) || e.Timestamp.Before(to)) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}

func (s *ReplayService) generatePaymentEvents(paymentID string) []domain.ReplayStep {
	now := time.Now().UTC()
	return []domain.ReplayStep{
		{StepID: 1, EventType: "PAYMENT_CREATED", Timestamp: now.Add(-10 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"CREATED","amount":5000,"currency":"GBP"}`, paymentID))},
		{StepID: 2, EventType: "FRAUD_CHECKED", Timestamp: now.Add(-9 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"FRAUD_CHECKED","risk_score":0.05}`, paymentID))},
		{StepID: 3, EventType: "PAYMENT_AUTHORIZED", Timestamp: now.Add(-8 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"AUTHORIZED"}`, paymentID))},
		{StepID: 4, EventType: "LEDGER_ENTRY_CREATED", Timestamp: now.Add(-7 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"LEDGERED","ledger_id":"LED-001"}`, paymentID))},
		{StepID: 5, EventType: "PAYMENT_PROCESSING", Timestamp: now.Add(-5 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"PROCESSING"}`, paymentID))},
		{StepID: 6, EventType: "PAYMENT_SETTLED", Timestamp: now.Add(-1 * time.Minute), Payload: json.RawMessage(fmt.Sprintf(`{"payment_id":"%s","status":"SETTLED","settled_at":"%s"}`, paymentID, now.Format(time.RFC3339)))},
	}
}
