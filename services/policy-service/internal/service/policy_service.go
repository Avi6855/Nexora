package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
	"github.com/nexora/nexora/services/policy-service/internal/events"
	"github.com/nexora/nexora/services/policy-service/internal/repository"
)

type PolicyService struct {
	policyRepo repository.PolicyRepository
	producer   *events.KafkaProducer
	logger     zerolog.Logger
}

func NewPolicyService(policyRepo repository.PolicyRepository, producer *events.KafkaProducer, logger zerolog.Logger) *PolicyService {
	return &PolicyService{policyRepo: policyRepo, producer: producer, logger: logger}
}

func (s *PolicyService) CreatePolicy(ctx context.Context, req *domain.CreatePolicyRequest) (*domain.Policy, error) {
	s.logger.Info().Str("name", req.Name).Msg("creating policy")
	now := time.Now().UTC()
	policy := &domain.Policy{
		PolicyID:    uuid.New(),
		Name:        req.Name,
		Description: req.Description,
		PolicyType:  req.PolicyType,
		Scope:       req.Scope,
		Status:      domain.PolicyStatusDraft,
		Rules:       req.Rules,
		Enabled:     false,
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	for i := range policy.Rules {
		if policy.Rules[i].RuleID == "" {
			policy.Rules[i].RuleID = uuid.New().String()[:8]
		}
		policy.Rules[i].Enabled = true
	}
	if err := s.policyRepo.Create(ctx, policy); err != nil {
		return nil, fmt.Errorf("storing policy: %w", err)
	}

	if s.producer != nil {
		_ = s.producer.Publish(ctx, "policy.created", policy)
	}

	return policy, nil
}

func (s *PolicyService) GetPolicy(ctx context.Context, id uuid.UUID) (*domain.Policy, error) {
	return s.policyRepo.GetByID(ctx, id)
}

func (s *PolicyService) GetPoliciesByType(ctx context.Context, policyType domain.PolicyType) ([]*domain.Policy, error) {
	return s.policyRepo.GetByType(ctx, policyType)
}

func (s *PolicyService) UpdatePolicy(ctx context.Context, id uuid.UUID, req *domain.CreatePolicyRequest) (*domain.Policy, error) {
	policy, err := s.policyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if policy.Status == domain.PolicyStatusActive {
		return nil, fmt.Errorf("cannot update active policy directly, disable first")
	}
	policy.Name = req.Name
	policy.Description = req.Description
	policy.PolicyType = req.PolicyType
	policy.Scope = req.Scope
	policy.Rules = req.Rules
	policy.Version++
	policy.UpdatedAt = time.Now().UTC()
	if err := s.policyRepo.Update(ctx, policy); err != nil {
		return nil, fmt.Errorf("updating policy: %w", err)
	}
	return policy, nil
}

func (s *PolicyService) DeletePolicy(ctx context.Context, id uuid.UUID) error {
	policy, err := s.policyRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if policy.Status == domain.PolicyStatusActive {
		return fmt.Errorf("cannot delete active policy, disable first")
	}
	return s.policyRepo.Delete(ctx, id)
}

func (s *PolicyService) DraftPolicy(ctx context.Context, id uuid.UUID) error {
	return s.setStatus(ctx, id, domain.PolicyStatusDraft)
}

func (s *PolicyService) ActivatePolicy(ctx context.Context, id uuid.UUID) error {
	policy, err := s.policyRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if policy.Status != domain.PolicyStatusTesting && policy.Status != domain.PolicyStatusShadow && policy.Status != domain.PolicyStatusDraft {
		return fmt.Errorf("policy must be in TESTING, SHADOW, or DRAFT status to activate")
	}
	policy.Enabled = true
	policy.UpdatedAt = time.Now().UTC()
	s.policyRepo.Update(ctx, policy)
	return s.setStatus(ctx, id, domain.PolicyStatusActive)
}

func (s *PolicyService) DisablePolicy(ctx context.Context, id uuid.UUID) error {
	policy, err := s.policyRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	policy.Enabled = false
	policy.UpdatedAt = time.Now().UTC()
	s.policyRepo.Update(ctx, policy)
	return s.setStatus(ctx, id, domain.PolicyStatusDisabled)
}

func (s *PolicyService) SetTesting(ctx context.Context, id uuid.UUID) error {
	return s.setStatus(ctx, id, domain.PolicyStatusTesting)
}

func (s *PolicyService) SetShadow(ctx context.Context, id uuid.UUID) error {
	return s.setStatus(ctx, id, domain.PolicyStatusShadow)
}

func (s *PolicyService) EnablePolicy(ctx context.Context, id uuid.UUID) error {
	return s.ActivatePolicy(ctx, id)
}

func (s *PolicyService) EvaluatePayment(ctx context.Context, req *domain.EvaluatePaymentRequest) (*domain.EvaluatePaymentResponse, error) {
	s.logger.Info().Str("payment_id", req.PaymentID).Msg("evaluating payment against policies")

	paymentCtx := domain.PaymentContext{
		AccountID:   uuid.MustParse(req.AccountID),
		UserID:      uuid.MustParse(req.UserID),
		Amount:      req.Amount,
		Currency:    req.Currency,
		DeviceID:    req.DeviceID,
		IPAddress:   req.IPAddress,
		RecipientID: req.RecipientID,
		Timestamp:   time.Now().UTC(),
	}

	activePolicies, err := s.policyRepo.GetActivePolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching active policies: %w", err)
	}

	var allDecisions []domain.PolicyDecision
	var allMatchedRules []domain.MatchedRule
	var allReasons []string
	finalDecision := domain.PolicyDecisionAllow

	for _, policy := range activePolicies {
		decision := s.evaluatePolicy(paymentCtx, *policy)
		if decision != nil {
			allDecisions = append(allDecisions, *decision)
			allMatchedRules = append(allMatchedRules, decision.MatchedRules...)
			allReasons = append(allReasons, decision.Reasons...)

			if decision.Action == domain.PolicyDecisionBlock {
				finalDecision = domain.PolicyDecisionBlock
			} else if decision.Action == domain.PolicyDecisionStepUp && finalDecision != domain.PolicyDecisionBlock {
				finalDecision = domain.PolicyDecisionStepUp
			}
		}
	}

	return &domain.EvaluatePaymentResponse{
		FinalDecision:  finalDecision,
		PolicyDecisions: allDecisions,
		MatchedRules:   allMatchedRules,
		Reasons:        allReasons,
	}, nil
}

func (s *PolicyService) RunShadowPolicy(ctx context.Context, req *domain.ShadowCompareRequest) (*domain.ShadowCompareResponse, error) {
	s.logger.Info().Str("payment_id", req.PaymentID).Str("shadow_policy_id", req.PolicyID).Msg("running shadow policy comparison")

	shadowPolicyID, err := uuid.Parse(req.PolicyID)
	if err != nil {
		return nil, fmt.Errorf("invalid shadow policy ID: %w", err)
	}

	currentReq := &domain.EvaluatePaymentRequest{
		PaymentID:   req.PaymentID,
		AccountID:   req.AccountID,
		UserID:      req.UserID,
		Amount:      req.Amount,
		Currency:    req.Currency,
		DeviceID:    req.DeviceID,
		IPAddress:   req.IPAddress,
		RecipientID: req.RecipientID,
	}
	currentResp, err := s.EvaluatePayment(ctx, currentReq)
	if err != nil {
		return nil, err
	}

	shadowPolicy, err := s.policyRepo.GetByID(ctx, shadowPolicyID)
	if err != nil {
		return nil, err
	}

	paymentCtx := domain.PaymentContext{
		AccountID:   uuid.MustParse(req.AccountID),
		UserID:      uuid.MustParse(req.UserID),
		Amount:      req.Amount,
		Currency:    req.Currency,
		DeviceID:    req.DeviceID,
		IPAddress:   req.IPAddress,
		RecipientID: req.RecipientID,
		Timestamp:   time.Now().UTC(),
	}

	shadowDecision := s.evaluatePolicy(paymentCtx, *shadowPolicy)
	shadowResult := domain.PolicyDecision{
		PolicyID:    shadowPolicyID,
		PolicyName:  shadowPolicy.Name,
		Action:      domain.PolicyDecisionAllow,
		MatchedRules: make([]domain.MatchedRule, 0),
		Reasons:     make([]string, 0),
		EvaluatedAt: time.Now().UTC(),
	}
	if shadowDecision != nil {
		shadowResult = *shadowDecision
	}

	currentResult := domain.PolicyDecision{
		PolicyID:     uuid.Nil,
		PolicyName:   "current-active",
		Action:       currentResp.FinalDecision,
		MatchedRules: currentResp.MatchedRules,
		Reasons:      currentResp.Reasons,
		EvaluatedAt:  time.Now().UTC(),
	}

	match := currentResult.Action == shadowResult.Action
	var differences []string
	if !match {
		differences = append(differences, fmt.Sprintf("current=%s vs shadow=%s", currentResult.Action, shadowResult.Action))
	}
	if len(currentResult.MatchedRules) != len(shadowResult.MatchedRules) {
		differences = append(differences, fmt.Sprintf("rule_count: current=%d shadow=%d", len(currentResult.MatchedRules), len(shadowResult.MatchedRules)))
	}

	return &domain.ShadowCompareResponse{
		CurrentDecision: currentResult,
		ShadowDecision:  shadowResult,
		Match:           match,
		Differences:     differences,
	}, nil
}

func (s *PolicyService) evaluatePolicy(ctx domain.PaymentContext, policy domain.Policy) *domain.PolicyDecision {
	if !policy.Enabled {
		return nil
	}

	matchedRules := make([]domain.MatchedRule, 0)
	reasons := make([]string, 0)
	highestAction := domain.PolicyDecisionAllow

	for _, rule := range policy.Rules {
		if !rule.Enabled {
			continue
		}
		if s.evaluateRule(ctx, rule) {
			matched := domain.MatchedRule{
				RuleID:   rule.RuleID,
				RuleName: rule.Name,
				Action:   rule.Action,
				Reason:   fmt.Sprintf("rule '%s' matched", rule.Name),
			}
			matchedRules = append(matchedRules, matched)
			reasons = append(reasons, matched.Reason)

			if rule.Action == domain.PolicyDecisionBlock {
				highestAction = domain.PolicyDecisionBlock
			} else if rule.Action == domain.PolicyDecisionStepUp && highestAction != domain.PolicyDecisionBlock {
				highestAction = domain.PolicyDecisionStepUp
			}
		}
	}

	if len(matchedRules) == 0 {
		return nil
	}

	return &domain.PolicyDecision{
		PolicyID:     policy.PolicyID,
		PolicyName:   policy.Name,
		Action:       highestAction,
		MatchedRules: matchedRules,
		Reasons:      reasons,
		EvaluatedAt:  time.Now().UTC(),
	}
}

func (s *PolicyService) evaluateRule(ctx domain.PaymentContext, rule domain.PolicyRule) bool {
	condition := strings.ToLower(rule.Condition)

	switch {
	case strings.Contains(condition, "amount_gt"):
		threshold := parseThreshold(condition)
		return ctx.Amount > threshold
	case strings.Contains(condition, "amount_lt"):
		threshold := parseThreshold(condition)
		return ctx.Amount < threshold
	case strings.Contains(condition, "amount_eq"):
		threshold := parseThreshold(condition)
		return ctx.Amount == threshold
	case strings.Contains(condition, "time_between"):
		return true
	case strings.Contains(condition, "new_recipient"):
		return true
	case strings.Contains(condition, "device_known"):
		return ctx.DeviceID != ""
	case strings.Contains(condition, "always"):
		return true
	default:
		return false
	}
}

func parseThreshold(condition string) int64 {
	parts := strings.Split(condition, ":")
	if len(parts) > 1 {
		var threshold int64
		fmt.Sscanf(parts[1], "%d", &threshold)
		return threshold
	}
	return 0
}

func (s *PolicyService) setStatus(ctx context.Context, id uuid.UUID, status domain.PolicyStatus) error {
	policy, err := s.policyRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	policy.Status = status
	policy.UpdatedAt = time.Now().UTC()
	if status == domain.PolicyStatusActive {
		policy.Enabled = true
	}
	if status == domain.PolicyStatusDisabled {
		policy.Enabled = false
	}
	return s.policyRepo.Update(ctx, policy)
}
