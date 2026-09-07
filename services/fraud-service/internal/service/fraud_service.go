package service

import (
	"context"
	"math"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/fraud-service/internal/domain"
	"github.com/nexora/nexora/services/fraud-service/internal/events"
	"github.com/nexora/nexora/services/fraud-service/internal/repository"
)

type FraudService struct {
	repo   repository.FraudRepository
	producer *events.KafkaProducer
	logger zerolog.Logger
}

func NewFraudService(repo repository.FraudRepository, producer *events.KafkaProducer, logger zerolog.Logger) *FraudService {
	return &FraudService{repo: repo, producer: producer, logger: logger}
}

func (s *FraudService) AnalyzePaymentRisk(ctx context.Context, req *domain.AnalyzeRequest) (*domain.AnalyzeResponse, error) {
	s.logger.Info().Str("payment_id", req.PaymentID).Msg("analyzing payment risk")

	riskScore := 0.0
	var reasons []string

	if req.Amount > 1000000 {
		riskScore += 0.3
		reasons = append(reasons, "high_amount")
	}

	if req.Amount > 500000 {
		riskScore += 0.1
		reasons = append(reasons, "medium_high_amount")
	}

	if req.DeviceID == "" {
		riskScore += 0.2
		reasons = append(reasons, "missing_device_id")
	}

	if req.IPAddress == "" {
		riskScore += 0.15
		reasons = append(reasons, "missing_ip_address")
	}

	riskScore = math.Min(riskScore, 1.0)

	action := domain.RiskActionAllow
	if riskScore >= 0.8 {
		action = domain.RiskActionBlock
	} else if riskScore >= 0.6 {
		action = domain.RiskActionReview
	} else if riskScore >= 0.4 {
		action = domain.RiskActionStepUp
	}

	analysis := &domain.FraudAnalysis{
		AnalysisID:  uuid.New(),
		PaymentID:   uuid.MustParse(req.PaymentID),
		UserID:      uuid.MustParse(req.UserID),
		RiskScore:   riskScore,
		RiskAction:  action,
		RiskLevel:   domain.RiskLevelLow,
		RiskReasons: "",
		DeviceID:    req.DeviceID,
		IPAddress:   req.IPAddress,
		CreatedAt:   time.Now().UTC(),
	}

	_ = s.repo.CreateAnalysis(ctx, analysis)

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "fraud.analysis.completed", analysis)
	}

	s.logger.Info().Str("payment_id", req.PaymentID).Float64("risk_score", riskScore).Str("action", string(action)).Msg("risk analysis complete")

	return &domain.AnalyzeResponse{
		RiskScore:  riskScore,
		RiskAction: action,
		Reasons:    reasons,
	}, nil
}

func (s *FraudService) AnalyzePayment(ctx context.Context, req *domain.AnalyzePaymentFullRequest) (*domain.AnalyzePaymentFullResponse, error) {
	s.logger.Info().Str("payment_id", req.PaymentID).Msg("full payment risk analysis")

	var signals []domain.FraudSignal
	var reasons []string
	totalScore := 0.0

	if req.Amount > 100000 {
		signals = append(signals, domain.FraudSignal{
			SignalType: domain.FraudSignalAmountOutOfPattern,
			Score:      0.8,
			Weight:     0.25,
			Message:    "Payment amount significantly exceeds typical transaction size",
		})
		reasons = append(reasons, "amount_out_of_pattern")
		totalScore += 0.8 * 0.25
	} else if req.Amount > 50000 {
		signals = append(signals, domain.FraudSignal{
			SignalType: domain.FraudSignalAmountOutOfPattern,
			Score:      0.5,
			Weight:     0.25,
			Message:    "Payment amount exceeds average transaction size",
		})
		reasons = append(reasons, "amount_above_average")
		totalScore += 0.5 * 0.25
	}

	if req.RecipientID != "" && req.PaymentHistory != nil {
		isNew := true
		if req.PaymentHistory.UniqueRecipients > 0 {
			isNew = false
		}
		if isNew {
			signals = append(signals, domain.FraudSignal{
				SignalType: domain.FraudSignalNewRecipient,
				Score:      0.6,
				Weight:     0.2,
				Message:    "Payment to previously unseen recipient",
			})
			reasons = append(reasons, "new_recipient")
			totalScore += 0.6 * 0.2
		}
	}

	hour := time.Now().UTC().Hour()
	if hour >= 1 && hour <= 5 {
		signals = append(signals, domain.FraudSignal{
			SignalType: domain.FraudSignalUnusualTime,
			Score:      0.4,
			Weight:     0.15,
			Message:    "Transaction at unusual hour",
		})
		reasons = append(reasons, "unusual_time")
		totalScore += 0.4 * 0.15
	}

	if req.AccountState != nil {
		if req.AccountState.TxCountToday > 20 {
			signals = append(signals, domain.FraudSignal{
				SignalType: domain.FraudSignalHighFrequency,
				Score:      0.7,
				Weight:     0.2,
				Message:    "Unusually high transaction frequency today",
			})
			reasons = append(reasons, "high_frequency")
			totalScore += 0.7 * 0.2
		}

		if req.AccountState.LastTxTime.After(time.Now().Add(-5 * time.Minute)) {
			signals = append(signals, domain.FraudSignal{
				SignalType: domain.FraudSignalVelocityBreach,
				Score:      0.6,
				Weight:     0.1,
				Message:    "Rapid successive transactions detected",
			})
			reasons = append(reasons, "velocity_breach")
			totalScore += 0.6 * 0.1
		}
	}

	if req.DeviceInfo != nil {
		if !req.DeviceInfo.TrustedDevice {
			signals = append(signals, domain.FraudSignal{
				SignalType: domain.FraudSignalDeviceMismatch,
				Score:      0.5,
				Weight:     0.15,
				Message:    "Transaction from untrusted device",
			})
			reasons = append(reasons, "untrusted_device")
			totalScore += 0.5 * 0.15
		}
		if req.DeviceInfo.IsRooted || req.DeviceInfo.IsEmulator {
			signals = append(signals, domain.FraudSignal{
				SignalType: domain.FraudSignalDeviceMismatch,
				Score:      0.9,
				Weight:     0.15,
				Message:    "Transaction from compromised/emulator device",
			})
			reasons = append(reasons, "compromised_device")
			totalScore += 0.9 * 0.15
		}
	}

	totalScore = math.Min(totalScore, 1.0)

	action := domain.RiskActionAllow
	riskLevel := domain.RiskLevelLow
	if totalScore >= 0.7 {
		action = domain.RiskActionBlock
		riskLevel = domain.RiskLevelCritical
	} else if totalScore >= 0.5 {
		action = domain.RiskActionReview
		riskLevel = domain.RiskLevelHigh
	} else if totalScore >= 0.3 {
		action = domain.RiskActionStepUp
		riskLevel = domain.RiskLevelMedium
	}

	if len(signals) == 0 {
		signals = make([]domain.FraudSignal, 0)
	}
	if len(reasons) == 0 {
		reasons = make([]string, 0)
	}

	paymentID := uuid.MustParse(req.PaymentID)
	userID := uuid.MustParse(req.UserID)
	accountID := uuid.MustParse(req.AccountID)

	analysis := &domain.FraudAnalysis{
		AnalysisID:  uuid.New(),
		PaymentID:   paymentID,
		UserID:      userID,
		AccountID:   accountID,
		RiskScore:   totalScore,
		RiskAction:  action,
		RiskLevel:   riskLevel,
		DeviceID:    req.DeviceID,
		IPAddress:   req.IPAddress,
		CreatedAt:   time.Now().UTC(),
	}

	_ = s.repo.CreateAnalysis(ctx, analysis)

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "fraud.analysis.completed", analysis)
	}

	s.logger.Info().
		Str("payment_id", req.PaymentID).
		Float64("risk_score", totalScore).
		Str("action", string(action)).
		Int("signals", len(signals)).
		Msg("full risk analysis complete")

	return &domain.AnalyzePaymentFullResponse{
		Action:     action,
		RiskScore:  totalScore,
		RiskLevel:  riskLevel,
		Signals:    signals,
		Reasons:    reasons,
		PaymentID:  req.PaymentID,
		AnalyzedAt: time.Now().UTC(),
	}, nil
}

// EvaluateCardAuthorization is the real-time (sub-second) card presentment
// decision. The full evaluation runs inside the service package over the real
// stored history; see authorization_evaluator.go.
func (s *FraudService) EvaluateCardAuthorization(ctx context.Context, req *domain.EvaluateAuthorizationRequest) (*domain.EvaluateAuthorizationResponse, error) {
	evaluator := NewAuthorizationEvaluator(s.repo)
	resp, err := evaluator.Evaluate(ctx, req)
	if err != nil {
		s.logger.Error().Err(err).Str("authorization_id", req.AuthorizationID).Msg("authorisation evaluation failed")
		return nil, err
	}
	s.logger.Info().
		Str("authorization_id", req.AuthorizationID).
		Str("decision", string(resp.Decision)).
		Float64("risk_score", resp.RiskScore).
		Msg("authorisation evaluated")
	return resp, nil
}

// EvaluateTransferRisk is the outbound bank-transfer scam-intelligence
// decision (the card-equivalent check for payments leaving the account).
// The evaluation runs over the user's stored decision history; see
// transfer_evaluator.go.
func (s *FraudService) EvaluateTransferRisk(ctx context.Context, req *domain.TransferRiskRequest) (*domain.TransferRiskResponse, error) {
	evaluator := NewTransferEvaluator(s.repo, s.logger)
	resp, err := evaluator.Evaluate(ctx, req)
	if err != nil {
		s.logger.Error().Err(err).Str("request_id", req.RequestID).Msg("transfer risk evaluation failed")
		return nil, err
	}
	return resp, nil
}

func (s *FraudService) RecordFraudEvent(ctx context.Context, event *domain.FraudEvent) error {
	s.logger.Info().Str("event_id", event.EventID.String()).Msg("recording fraud event")

	analysis := &domain.FraudAnalysis{
		AnalysisID:  uuid.New(),
		PaymentID:   event.PaymentID,
		UserID:      event.UserID,
		RiskScore:   event.RiskScore,
		RiskAction:  event.RiskAction,
		RiskLevel:   event.RiskLevel,
		DeviceID:    event.DeviceID,
		IPAddress:   event.IPAddress,
		GeoLocation: event.GeoLocation,
		CreatedAt:   time.Now().UTC(),
	}

	return s.repo.CreateAnalysis(ctx, analysis)
}

func (s *FraudService) GetUserFraudEvents(ctx context.Context, userID uuid.UUID) ([]*domain.FraudEvent, error) {
	analyses, err := s.repo.GetAnalysesByUser(ctx, userID, 100)
	if err != nil {
		return nil, err
	}

	events := make([]*domain.FraudEvent, len(analyses))
	for i, a := range analyses {
		events[i] = &domain.FraudEvent{
			EventID:     a.AnalysisID,
			PaymentID:   a.PaymentID,
			UserID:      a.UserID,
			RiskScore:   a.RiskScore,
			RiskAction:  a.RiskAction,
			RiskLevel:   a.RiskLevel,
			DeviceID:    a.DeviceID,
			IPAddress:   a.IPAddress,
			GeoLocation: a.GeoLocation,
			CreatedAt:   a.CreatedAt,
		}
	}

	return events, nil
}
