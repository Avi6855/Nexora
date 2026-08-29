package service

import (
	"context"
	"math"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/simulation-service/internal/domain"
	"github.com/nexora/nexora/services/simulation-service/internal/events"
	"github.com/nexora/nexora/services/simulation-service/internal/repository"
)

type SimulationService struct {
	repo     repository.SimulationRepository
	producer *events.KafkaProducer
	simulations map[uuid.UUID]*domain.Simulation
	logger      zerolog.Logger
}

func NewSimulationService(repo repository.SimulationRepository, producer *events.KafkaProducer, logger zerolog.Logger) *SimulationService {
	return &SimulationService{
		repo:        repo,
		producer:    producer,
		simulations: make(map[uuid.UUID]*domain.Simulation),
		logger:      logger,
	}
}

func (s *SimulationService) CreateSimulation(ctx context.Context, req *domain.CreateSimulationRequest) (*domain.Simulation, error) {
	s.logger.Info().Str("name", req.Name).Msg("creating simulation")

	sim := &domain.Simulation{
		SimulationID: uuid.New(),
		Name:         req.Name,
		Type:         req.Type,
		Status:       domain.SimulationStatusPending,
		Parameters:   req.Parameters,
		CreatedAt:    time.Now().UTC(),
	}

	s.simulations[sim.SimulationID] = sim

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "simulation.created", sim)
	}

	return sim, nil
}

func (s *SimulationService) GetSimulation(ctx context.Context, id uuid.UUID) (*domain.Simulation, error) {
	sim, ok := s.simulations[id]
	if !ok {
		return nil, nil
	}
	return sim, nil
}

func (s *SimulationService) StartSimulation(ctx context.Context, id uuid.UUID) error {
	s.logger.Info().Str("simulation_id", id.String()).Msg("starting simulation")
	sim, ok := s.simulations[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	sim.Status = domain.SimulationStatusRunning
	sim.StartedAt = &now
	return nil
}

func (s *SimulationService) CompleteSimulation(ctx context.Context, id uuid.UUID) error {
	sim, ok := s.simulations[id]
	if !ok {
		return nil
	}
	now := time.Now().UTC()
	sim.Status = domain.SimulationStatusCompleted
	sim.CompletedAt = &now

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "simulation.completed", sim)
	}

	return nil
}

func (s *SimulationService) StopSimulation(ctx context.Context, id uuid.UUID) error {
	return s.CompleteSimulation(ctx, id)
}

func (s *SimulationService) ListSimulations(ctx context.Context) ([]*domain.Simulation, error) {
	var sims []*domain.Simulation
	for _, sim := range s.simulations {
		sims = append(sims, sim)
	}
	return sims, nil
}

func (s *SimulationService) CreateDigitalTwin(ctx context.Context, accountID string) (*domain.DigitalTwin, error) {
	s.logger.Info().Str("account_id", accountID).Msg("creating digital twin")

	id, err := uuid.Parse(accountID)
	if err != nil {
		return nil, err
	}

	twin := &domain.DigitalTwin{
		AccountID:          id,
		UserID:             uuid.New(),
		Balance:            250000,
		Available:          200000,
		Reserved:           50000,
		Currency:           "GBP",
		Status:             "ACTIVE",
		SnapshotTime:       time.Now().UTC(),
		RecentTransactions: 12,
		AverageTxAmount:    4500,
		MaxTxAmount:        50000,
		TxCountToday:       3,
	}

	return twin, nil
}

func (s *SimulationService) SimulatePayment(ctx context.Context, req *domain.SimulatePaymentRequest) (*domain.SimulationResult, error) {
	s.logger.Info().Str("source_account", req.SourceAccountID).Int64("amount", req.Amount).Msg("simulating payment")

	_, err := uuid.Parse(req.SourceAccountID)
	if err != nil {
		return nil, err
	}

	balance := int64(250000)
	available := int64(200000)

	wouldSucceed := true
	var reasons []string
	var policyDecisions []domain.PolicyDecision

	if req.Amount > available {
		wouldSucceed = false
		reasons = append(reasons, "insufficient_available_balance")
	}

	if req.Amount > 100000 {
		wouldSucceed = false
		reasons = append(reasons, "exceeds_single_payment_limit")
		policyDecisions = append(policyDecisions, domain.PolicyDecision{
			PolicyID: "policy-spending-limit",
			RuleID:   "rule-single-payment-max",
			Action:   "BLOCK",
			Reason:   "Amount exceeds maximum single payment limit of £1,000",
		})
	}

	if req.Amount > 10000 {
		policyDecisions = append(policyDecisions, domain.PolicyDecision{
			PolicyID: "policy-high-value",
			RuleID:   "rule-high-value-check",
			Action:   "STEP_UP",
			Reason:   "High value payment requires additional verification",
		})
		reasons = append(reasons, "high_value_step_up_required")
	}

	riskScore := 0.1
	if req.Amount > 50000 {
		riskScore = 0.7
		reasons = append(reasons, "high_risk_amount")
	} else if req.Amount > 10000 {
		riskScore = 0.3
	}

	predictedBalance := balance
	predictedAvailable := available
	if wouldSucceed {
		predictedBalance = balance - req.Amount
		predictedAvailable = available - req.Amount
	}

	result := &domain.SimulationResult{
		WouldSucceed:       wouldSucceed,
		PredictedBalance:   predictedBalance,
		PredictedAvailable: predictedAvailable,
		RiskScore:          riskScore,
		PolicyDecisions:    policyDecisions,
		Reasons:            reasons,
		SimulatedAt:        time.Now().UTC(),
	}

	_ = s.repo.StoreSimulationResult(ctx, result)

	return result, nil
}

func (s *SimulationService) SimulateTransfer(ctx context.Context, req *domain.SimulateTransferRequest) (*domain.SimulationResult, error) {
	s.logger.Info().Str("from", req.FromAccountID).Str("to", req.ToAccountID).Int64("amount", req.Amount).Msg("simulating transfer")

	_, err := uuid.Parse(req.FromAccountID)
	if err != nil {
		return nil, err
	}
	_, err = uuid.Parse(req.ToAccountID)
	if err != nil {
		return nil, err
	}

	sourceBalance := int64(250000)
	sourceAvailable := int64(200000)

	wouldSucceed := true
	var reasons []string
	var policyDecisions []domain.PolicyDecision

	if req.Amount > sourceAvailable {
		wouldSucceed = false
		reasons = append(reasons, "insufficient_available_balance")
	}

	if req.Amount > 50000 {
		wouldSucceed = false
		reasons = append(reasons, "exceeds_transfer_limit")
		policyDecisions = append(policyDecisions, domain.PolicyDecision{
			PolicyID: "policy-transfer-limit",
			RuleID:   "rule-transfer-max",
			Action:   "BLOCK",
			Reason:   "Transfer amount exceeds maximum limit",
		})
	}

	riskScore := 0.05
	if req.Amount > 10000 {
		riskScore = 0.4
		policyDecisions = append(policyDecisions, domain.PolicyDecision{
			PolicyID: "policy-transfer-review",
			RuleID:   "rule-large-transfer",
			Action:   "STEP_UP",
			Reason:   "Large transfer requires verification",
		})
		reasons = append(reasons, "large_transfer_step_up")
	}

	predictedFromBalance := sourceBalance
	predictedFromAvailable := sourceAvailable
	if wouldSucceed {
		predictedFromBalance = sourceBalance - req.Amount
		predictedFromAvailable = sourceAvailable - req.Amount
	}

	result := &domain.SimulationResult{
		WouldSucceed:       wouldSucceed,
		PredictedBalance:   predictedFromBalance,
		PredictedAvailable: predictedFromAvailable,
		RiskScore:          math.Min(riskScore, 1.0),
		PolicyDecisions:    policyDecisions,
		Reasons:            reasons,
		SimulatedAt:        time.Now().UTC(),
	}

	_ = s.repo.StoreSimulationResult(ctx, result)

	return result, nil
}
