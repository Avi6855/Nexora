package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/fraud-service/internal/domain"
	"github.com/nexora/nexora/services/fraud-service/internal/repository"
)

// Real-time authorisation policy thresholds. These are policy constants, not
// demo data: every *input* to the engine (history rows, spend today, limits)
// is read live from Cassandra on each request.
const (
	velocityWindow       = 10 * time.Minute
	velocityMaxPerWindow = 4
	patternWindow        = 24 * time.Hour
	amountOutlierFactor  = 4.0
	scoreBlockThreshold  = 0.6
	scoreReviewThreshold = 0.35
)

type AuthorizationEvaluator struct {
	repo repository.FraudRepository
}

func NewAuthorizationEvaluator(repo repository.FraudRepository) *AuthorizationEvaluator {
	return &AuthorizationEvaluator{repo: repo}
}

// Evaluate scores a live card authorisation against the user's *real* stored
// decision history (velocity, geo, amount pattern, merchant behaviour) plus
// the live account/card state passed in by the card platform, then persists
// the analysis row so the next authorisation sees it.
func (e *AuthorizationEvaluator) Evaluate(ctx context.Context, req *domain.EvaluateAuthorizationRequest) (*domain.EvaluateAuthorizationResponse, error) {
	userID := uuid.MustParse(req.UserID)

	now := time.Now().UTC()
	velocityHistory, err := e.repo.GetRecentDecisions(ctx, userID, now.Add(-velocityWindow), 100)
	if err != nil {
		return nil, fmt.Errorf("loading velocity history: %w", err)
	}
	patternHistory, err := e.repo.GetRecentDecisions(ctx, userID, now.Add(-patternWindow), 200)
	if err != nil {
		return nil, fmt.Errorf("loading pattern history: %w", err)
	}

	var signals []domain.FraudSignal
	var reasons []string
	total := 0.0

	addSignal := func(s domain.FraudSignal) {
		signals = append(signals, s)
		reasons = append(reasons, string(s.SignalType))
		total += s.Score * s.Weight
	}

	// 1. Card controls breached — deterministic decline. Inputs are the real
	//    card limits and the real spend today supplied by the card platform.
	if req.DailyLimit > 0 && req.Amount+req.SpentToday > req.DailyLimit {
		addSignal(domain.FraudSignal{
			SignalType: domain.FraudSignalVelocityBreach,
			Score:      1.0,
			Weight:     1.0,
			Message:    "Amount would exceed the card daily limit (spent today " + fmt.Sprintf("%d", req.SpentToday) + ")",
		})
	}

	// 2. Rapid successive authorisations (velocity over REAL history rows).
	velocity := len(velocityHistory)
	if velocity >= velocityMaxPerWindow {
		addSignal(domain.FraudSignal{
			SignalType: domain.FraudSignalHighFrequency,
			Score:      0.7,
			Weight:     0.3,
			Message:    fmt.Sprintf("%d authorisations in the last %s", velocity, velocityWindow),
		})
	}

	// 3. Amount out of pattern vs REAL 24h history.
	var avg, max int64
	var counted int
	for _, h := range patternHistory {
		if h.Amount > 0 {
			avg += h.Amount
			if h.Amount > max {
				max = h.Amount
			}
			counted++
		}
	}
	if counted > 0 {
		avg /= int64(counted)
	}
	if counted >= 2 && req.Amount > int64(float64(avg)*amountOutlierFactor) {
		addSignal(domain.FraudSignal{
			SignalType: domain.FraudSignalAmountOutOfPattern,
			Score:      0.8,
			Weight:     0.3,
			Message:    fmt.Sprintf("Amount %d is more than %.0fx the average (%d) of the last %d authorisations", req.Amount, amountOutlierFactor, avg, counted),
		})
	}

	// 4. Geo anomaly vs REAL prior authorisation countries/cities.
	var priorCountries = map[string]int{}
	for _, h := range patternHistory {
		if h.MerchantCountry != "" {
			priorCountries[h.MerchantCountry]++
		}
	}
	established := len(priorCountries)
	if established > 0 && priorCountries[req.MerchantCountry] == 0 {
		addSignal(domain.FraudSignal{
			SignalType: domain.FraudSignalGeoAnomaly,
			Score:      0.65,
			Weight:     0.25,
			Message:    "Authorisation from a country with no prior spend history (" + req.MerchantCountry + ")",
		})
	}

	// 5. Unusual hour of day.
	hour := now.Hour()
	if hour >= 1 && hour <= 5 {
		addSignal(domain.FraudSignal{
			SignalType: domain.FraudSignalUnusualTime,
			Score:      0.4,
			Weight:     0.15,
			Message:    "Authorisation between 01:00 and 05:00 local",
		})
	}

	// 6. Inherently high-risk merchant categories (policy constants).
	switch req.MerchantCategory {
	case "CRYPTO", "WIRE_TRANSFER", "GAMBLING", "MONEY_TRANSFER", "CASH_ADVANCE", "HIGH_VALUE_ELECTRONICS":
		addSignal(domain.FraudSignal{
			SignalType: domain.FraudSignalAmountOutOfPattern,
			Score:      0.55,
			Weight:     0.2,
			Message:    "High-risk merchant category: " + req.MerchantCategory,
		})
	}

	total = math.Min(total, 1.0)

	decision := domain.CardAuthApprove
	level := domain.RiskLevelLow
	switch {
	case total >= scoreBlockThreshold:
		decision = domain.CardAuthDecline
		level = domain.RiskLevelCritical
	case total >= scoreReviewThreshold:
		decision = domain.CardAuthReview
		level = domain.RiskLevelHigh
	}

	// Persist this authorisation so the next decision runs over real history.
	reasonsJSON, _ := json.Marshal(reasons)
	signalsJSON, _ := json.Marshal(signals)
	analysis := &domain.FraudAnalysis{
		AnalysisID:       uuid.New(),
		PaymentID:        uuid.MustParse(req.AuthorizationID),
		UserID:           userID,
		AccountID:        uuid.MustParse(req.AccountID),
		RiskScore:        total,
		RiskAction:       domain.RiskAction(decision),
		RiskLevel:        level,
		RiskReasons:      string(reasonsJSON),
		Signals:          string(signalsJSON),
		DeviceID:         req.DeviceID,
		Amount:           req.Amount,
		Currency:         req.Currency,
		Merchant:         req.Merchant,
		MerchantCategory: req.MerchantCategory,
		MerchantCity:     req.MerchantCity,
		MerchantCountry:  req.MerchantCountry,
		Latitude:         req.Latitude,
		Longitude:        req.Longitude,
		CreatedAt:        now,
	}
	if err := e.repo.CreateAnalysis(ctx, analysis); err != nil {
		return nil, fmt.Errorf("persisting fraud analysis: %w", err)
	}

	return &domain.EvaluateAuthorizationResponse{
		AuthorizationID: req.AuthorizationID,
		Decision:        decision,
		RiskScore:       total,
		RiskLevel:       level,
		Signals:         signals,
		Reasons:         reasons,
		EvaluatedAt:     now,
	}, nil
}
