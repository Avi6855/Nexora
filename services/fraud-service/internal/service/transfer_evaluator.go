package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/fraud-service/internal/domain"
	"github.com/nexora/nexora/services/fraud-service/internal/repository"
)

// TransferEvaluator produces the outbound bank-transfer scam decision. Card
// presentment has merchant data; outbound transfers have only the user's own
// history, so the signals are behavioural: new payee, unusual amount, payment
// velocity, late-night timing and rapid successions of transfers.
type TransferEvaluator struct {
	repo   repository.FraudRepository
	logger zerolog.Logger
}

// NewTransferEvaluator builds the outbound-transfer evaluator.
func NewTransferEvaluator(repo repository.FraudRepository, logger zerolog.Logger) *TransferEvaluator {
	return &TransferEvaluator{repo: repo, logger: logger}
}

// signalAggregator keeps the per-signal score + weight pairs before the final
// decision is derived.
type signalAggregator struct {
	signals []domain.FraudSignal
	reasons []string
	total   float64
}

func (a *signalAggregator) add(signalType domain.FraudSignalType, score, weight float64, message, reason string) {
	a.signals = append(a.signals, domain.FraudSignal{
		SignalType: signalType,
		Score:      score,
		Weight:     weight,
		Message:    message,
	})
	a.reasons = append(a.reasons, reason)
	a.total += score * weight
}

// Evaluate scores an outbound transfer over the user's stored decision
// history (fraud_analyses rows created by earlier card + transfer checks).
func (e *TransferEvaluator) Evaluate(ctx context.Context, req *domain.TransferRiskRequest) (*domain.TransferRiskResponse, error) {
	agg := &signalAggregator{}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("invalid user_id: %w", err)
	}

	// Real history: every prior analysis for this user (card authorisations
	// plus earlier transfer checks) is the behavioural baseline.
	analyses, err := e.repo.GetAnalysesByUser(ctx, userID, 200)
	if err != nil {
		return nil, fmt.Errorf("reading decision history: %w", err)
	}

	// Normalise the user's outgoing amounts: card debits + prior transfer
	// analyses give the amount pattern.
	amounts := make([]float64, 0, len(analyses))
	for _, a := range analyses {
		if a.Amount > 0 {
			amounts = append(amounts, float64(a.Amount))
		}
	}
	sort.Float64s(amounts)

	// Signal 1: new payee — no prior analysis with this counterparty.
	knownCounterparty := false
	if req.CounterpartyID != "" || req.CounterpartyName != "" {
		for _, a := range analyses {
			if a.Merchant != "" && (strings.EqualFold(a.Merchant, req.CounterpartyName) || a.Merchant == req.CounterpartyID) {
				knownCounterparty = true
				break
			}
			if req.CounterpartyID != "" && a.PaymentID.String() == req.CounterpartyID {
				knownCounterparty = true
				break
			}
		}
	}
	if req.CounterpartyName != "" && !knownCounterparty {
		agg.add(domain.FraudSignalNewRecipient, 0.5, 0.25,
			"First payment to this recipient", "new_recipient")
	}

	// Signal 2: unusual amount vs the user's own history (p95 heuristic).
	if len(amounts) >= 5 {
		p95 := amounts[int(float64(len(amounts))*0.95)]
		if p95 > 0 && float64(req.Amount) > 3*p95 {
			agg.add(domain.FraudSignalAmountOutOfPattern, 0.7, 0.25,
				"Amount much higher than your usual payments", "amount_out_of_pattern")
		} else if p95 > 0 && float64(req.Amount) > 2*p95 {
			agg.add(domain.FraudSignalAmountOutOfPattern, 0.4, 0.2,
				"Amount higher than your usual payments", "amount_above_pattern")
		}
	} else {
		// Thin history: large absolute transfers warrant friction.
		if req.Amount > 200000 { // > £2,000
			agg.add(domain.FraudSignalAmountOutOfPattern, 0.4, 0.2,
				"Large transfer with little payment history", "large_amount_thin_history")
		}
	}

	// Signal 3: velocity — many decisions in the last 24h.
	now := time.Now().UTC()
	recent, err := e.repo.GetRecentDecisions(ctx, userID, now.Add(-24*time.Hour), 50)
	if err != nil {
		e.logger.Warn().Err(err).Msg("velocity check degraded; continuing")
		recent = nil
	}
	if len(recent) >= 10 {
		agg.add(domain.FraudSignalHighFrequency, 0.6, 0.2,
			"Unusually high number of payments today", "high_frequency")
	} else if len(recent) >= 5 {
		agg.add(domain.FraudSignalHighFrequency, 0.3, 0.15,
			"Elevated payment activity today", "elevated_frequency")
	}

	// Signal 4: rapid succession — last decision < 90s ago.
	if len(recent) > 0 {
		last := recent[0].CreatedAt
		if now.Sub(last) < 90*time.Second {
			agg.add(domain.FraudSignalVelocityBreach, 0.5, 0.15,
				"Rapid successive payments", "velocity_breach")
		}
	}

	// Signal 5: late-night timing (02:00–05:00) — classic APP-scam window.
	hour := now.Hour()
	if hour >= 2 && hour < 5 {
		agg.add(domain.FraudSignalUnusualTime, 0.4, 0.15,
			"Payment at an unusual hour", "unusual_time")
	}

	total := normalize(agg.total)

	action := domain.RiskActionAllow
	level := domain.RiskLevelLow
	advice := ""
	switch {
	case total >= 0.75:
		action = domain.RiskActionBlock
		level = domain.RiskLevelCritical
		advice = "This payment looks like it could be a scam. Contact the recipient through a number you trust before sending money."
	case total >= 0.5:
		action = domain.RiskActionStepUp
		level = domain.RiskLevelHigh
		advice = "Take a moment: is this the first time you're paying this person? Scam payments are often urgent requests."
	case total >= 0.3:
		action = domain.RiskActionReview
		level = domain.RiskLevelMedium
		advice = "This payment is unusual compared to your normal activity. Double-check the account details."
	}

	if len(agg.signals) == 0 {
		agg.signals = make([]domain.FraudSignal, 0)
	}
	if len(agg.reasons) == 0 {
		agg.reasons = make([]string, 0)
	}

	requestID := req.RequestID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	resp := &domain.TransferRiskResponse{
		RequestID:  requestID,
		Action:     action,
		RiskScore:  total,
		RiskLevel:  level,
		Reasons:    agg.reasons,
		Signals:    agg.signals,
		Advice:     advice,
		EvaluatedAt: now,
	}

	// Persist the decision over the user's real history so future decisions
	// (and velocity checks) see this transfer attempt too.
	analysis := &domain.FraudAnalysis{
		AnalysisID:  uuid.New(),
		PaymentID:   parseOrNew(req.RequestID),
		UserID:      userID,
		RiskScore:   total,
		RiskAction:  action,
		RiskLevel:   level,
		DeviceID:    req.DeviceID,
		IPAddress:   req.IPAddress,
		Amount:      req.Amount,
		Currency:    req.Currency,
		Merchant:    req.CounterpartyName,
		CreatedAt:   now,
	}
	_ = e.repo.CreateAnalysis(ctx, analysis)

	e.logger.Info().
		Str("request_id", resp.RequestID).
		Str("action", string(action)).
		Float64("risk_score", total).
		Int("signals", len(agg.signals)).
		Msg("outbound transfer risk evaluated")

	return resp, nil
}

func normalize(v float64) float64 {
	if v > 1.0 {
		return 1.0
	}
	if v < 0 {
		return 0
	}
	return v
}

func parseOrNew(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.New()
	}
	return id
}
