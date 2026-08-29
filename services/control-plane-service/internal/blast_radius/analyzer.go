package blast_radius

import (
	"fmt"
	"sync"
	"time"
)

type ChangeType string

const (
	ChangeTypeConfig      ChangeType = "CONFIG"
	ChangeTypeCode        ChangeType = "CODE"
	ChangeTypeInfrastructure ChangeType = "INFRASTRUCTURE"
	ChangeTypeSchema      ChangeType = "SCHEMA"
	ChangeTypeDependency  ChangeType = "DEPENDENCY"
)

type RiskLevel string

const (
	RiskLevelLow      RiskLevel = "LOW"
	RiskLevelMedium   RiskLevel = "MEDIUM"
	RiskLevelHigh     RiskLevel = "HIGH"
	RiskLevelCritical RiskLevel = "CRITICAL"
)

type ServiceInfo struct {
	Name          string   `json:"name"`
	DependsOn     []string `json:"depends_on"`
	DependedBy    []string `json:"depended_by"`
	APIsExposed   []string `json:"apis_exposed"`
	TopicsUsed    []string `json:"topics_used"`
	PaymentPaths  []string `json:"payment_paths"`
}

type ChangeRequest struct {
	ChangeType       ChangeType `json:"change_type"`
	AffectedServices []string   `json:"affected_services"`
	Description      string     `json:"description"`
	Config           map[string]string `json:"config,omitempty"`
}

type BlastRadiusResult struct {
	ChangeType       ChangeType          `json:"change_type"`
	AffectedAPIs     []string            `json:"affected_apis"`
	AffectedTopics   []string            `json:"affected_topics"`
	AffectedPayments []string            `json:"affected_payments"`
	AffectedServices []string            `json:"affected_services"`
	RiskLevel        RiskLevel           `json:"risk_level"`
	RiskScore        float64             `json:"risk_score"`
	EstimatedUsers   int                 `json:"estimated_users_affected"`
	RevenueAtRisk    float64             `json:"revenue_at_risk"`
	Recommendations  []string            `json:"recommendations"`
	Timestamp        time.Time           `json:"timestamp"`
}

type BlastRadiusAnalyzer struct {
	services map[string]*ServiceInfo
	mu       sync.RWMutex
}

func NewBlastRadiusAnalyzer() *BlastRadiusAnalyzer {
	analyzer := &BlastRadiusAnalyzer{
		services: make(map[string]*ServiceInfo),
	}
	analyzer.loadDefaultServices()
	return analyzer
}

func (a *BlastRadiusAnalyzer) loadDefaultServices() {
	a.services["identity-service"] = &ServiceInfo{
		Name:          "identity-service",
		DependsOn:     []string{},
		DependedBy:    []string{"user-service", "payment-service"},
		APIsExposed:   []string{"/api/v1/auth/login", "/api/v1/auth/refresh"},
		TopicsUsed:    []string{"nexora.user.registered"},
		PaymentPaths:  []string{},
	}
	a.services["user-service"] = &ServiceInfo{
		Name:          "user-service",
		DependsOn:     []string{"identity-service"},
		DependedBy:    []string{"account-service", "payment-service"},
		APIsExposed:   []string{"/api/v1/users"},
		TopicsUsed:    []string{"nexora.user.created"},
		PaymentPaths:  []string{},
	}
	a.services["account-service"] = &ServiceInfo{
		Name:          "account-service",
		DependsOn:     []string{"user-service", "ledger-service"},
		DependedBy:    []string{"payment-service", "transfer-service"},
		APIsExposed:   []string{"/api/v1/accounts"},
		TopicsUsed:    []string{"nexora.account.created"},
		PaymentPaths:  []string{},
	}
	a.services["ledger-service"] = &ServiceInfo{
		Name:          "ledger-service",
		DependsOn:     []string{},
		DependedBy:    []string{"payment-service", "transfer-service", "reconciliation-service"},
		APIsExposed:   []string{"/api/v1/ledger"},
		TopicsUsed:    []string{"nexora.ledger.entry"},
		PaymentPaths:  []string{"payment", "transfer"},
	}
	a.services["payment-service"] = &ServiceInfo{
		Name:          "payment-service",
		DependsOn:     []string{"identity-service", "user-service", "account-service", "ledger-service", "fraud-service", "policy-service"},
		DependedBy:    []string{"notification-service", "reconciliation-service"},
		APIsExposed:   []string{"/api/v1/payments"},
		TopicsUsed:    []string{"nexora.payment.created", "nexora.payment.confirmed", "nexora.payment.failed", "nexora.payment.settled"},
		PaymentPaths:  []string{"card_payment", "bank_transfer", "internal_transfer"},
	}
	a.services["transfer-service"] = &ServiceInfo{
		Name:          "transfer-service",
		DependsOn:     []string{"account-service", "ledger-service"},
		DependedBy:    []string{"notification-service"},
		APIsExposed:   []string{"/api/v1/transfers"},
		TopicsUsed:    []string{"nexora.transfer.created"},
		PaymentPaths:  []string{"internal_transfer"},
	}
	a.services["fraud-service"] = &ServiceInfo{
		Name:          "fraud-service",
		DependsOn:     []string{},
		DependedBy:    []string{"payment-service"},
		APIsExposed:   []string{"/api/v1/fraud/check"},
		TopicsUsed:    []string{"nexora.fraud.alert"},
		PaymentPaths:  []string{"payment"},
	}
	a.services["policy-service"] = &ServiceInfo{
		Name:          "policy-service",
		DependsOn:     []string{},
		DependedBy:    []string{"payment-service", "transfer-service"},
		APIsExposed:   []string{"/api/v1/policy/evaluate"},
		TopicsUsed:    []string{"nexora.policy.violation"},
		PaymentPaths:  []string{"payment", "transfer"},
	}
	a.services["notification-service"] = &ServiceInfo{
		Name:          "notification-service",
		DependsOn:     []string{"payment-service", "transfer-service", "user-service"},
		DependedBy:    []string{},
		APIsExposed:   []string{"/api/v1/notifications"},
		TopicsUsed:    []string{"nexora.notification.send"},
		PaymentPaths:  []string{},
	}
	a.services["reconciliation-service"] = &ServiceInfo{
		Name:          "reconciliation-service",
		DependsOn:     []string{"ledger-service", "payment-service"},
		DependedBy:    []string{},
		APIsExposed:   []string{"/api/v1/reconciliation"},
		TopicsUsed:    []string{"nexora.reconciliation.check"},
		PaymentPaths:  []string{"reconciliation"},
	}
	a.services["simulation-service"] = &ServiceInfo{
		Name:          "simulation-service",
		DependsOn:     []string{"payment-service"},
		DependedBy:    []string{"control-plane-service"},
		APIsExposed:   []string{"/api/v1/simulations"},
		TopicsUsed:    []string{},
		PaymentPaths:  []string{},
	}
	a.services["control-plane-service"] = &ServiceInfo{
		Name:          "control-plane-service",
		DependsOn:     []string{"simulation-service"},
		DependedBy:    []string{},
		APIsExposed:   []string{"/api/v1/health", "/api/v1/system"},
		TopicsUsed:    []string{},
		PaymentPaths:  []string{},
	}
	a.services["audit-service"] = &ServiceInfo{
		Name:          "audit-service",
		DependsOn:     []string{},
		DependedBy:    []string{},
		APIsExposed:   []string{"/api/v1/audit"},
		TopicsUsed:    []string{"nexora.audit.event"},
		PaymentPaths:  []string{},
	}
	a.services["incident-service"] = &ServiceInfo{
		Name:          "incident-service",
		DependsOn:     []string{},
		DependedBy:    []string{"control-plane-service"},
		APIsExposed:   []string{"/api/v1/incidents"},
		TopicsUsed:    []string{"nexora.incident.created"},
		PaymentPaths:  []string{},
	}
	a.services["card-service"] = &ServiceInfo{
		Name:          "card-service",
		DependsOn:     []string{"account-service", "identity-service"},
		DependedBy:    []string{"payment-service"},
		APIsExposed:   []string{"/api/v1/cards"},
		TopicsUsed:    []string{"nexora.card.issued"},
		PaymentPaths:  []string{"card_payment"},
	}
	a.services["pot-service"] = &ServiceInfo{
		Name:          "pot-service",
		DependsOn:     []string{"account-service"},
		DependedBy:    []string{},
		APIsExposed:   []string{"/api/v1/pots"},
		TopicsUsed:    []string{"nexora.pot.created"},
		PaymentPaths:  []string{},
	}
	a.services["replay-service"] = &ServiceInfo{
		Name:          "replay-service",
		DependsOn:     []string{"payment-service", "ledger-service"},
		DependedBy:    []string{"control-plane-service"},
		APIsExposed:   []string{"/api/v1/replay"},
		TopicsUsed:    []string{},
		PaymentPaths:  []string{},
	}
}

func (a *BlastRadiusAnalyzer) RegisterService(info *ServiceInfo) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.services[info.Name] = info
}

func (a *BlastRadiusAnalyzer) AnalyzeChange(req ChangeRequest) *BlastRadiusResult {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := &BlastRadiusResult{
		ChangeType:       req.ChangeType,
		AffectedServices: req.AffectedServices,
		Timestamp:        time.Now().UTC(),
		Recommendations:  make([]string, 0),
	}

	visited := make(map[string]bool)
	for _, svc := range req.AffectedServices {
		a.collectAffected(svc, visited)
	}

	for svc := range visited {
		info, ok := a.services[svc]
		if !ok {
			continue
		}
		result.AffectedAPIs = append(result.AffectedAPIs, info.APIsExposed...)
		result.AffectedTopics = append(result.AffectedTopics, info.TopicsUsed...)
		result.AffectedPayments = append(result.AffectedPayments, info.PaymentPaths...)
	}

	result.RiskScore = a.calculateRiskScore(req, result)
	result.RiskLevel = a.determineRiskLevel(result.RiskScore)
	result.EstimatedUsers = a.estimateUsersAffected(result)
	result.RevenueAtRisk = a.estimateRevenueAtRisk(result)
	result.Recommendations = a.generateRecommendations(req, result)

	return result
}

func (a *BlastRadiusAnalyzer) collectAffected(service string, visited map[string]bool) {
	if visited[service] {
		return
	}
	visited[service] = true

	info, ok := a.services[service]
	if !ok {
		return
	}

	for _, dep := range info.DependedBy {
		a.collectAffected(dep, visited)
	}
}

func (a *BlastRadiusAnalyzer) calculateRiskScore(req ChangeRequest, result *BlastRadiusResult) float64 {
	score := 0.0

	switch req.ChangeType {
	case ChangeTypeConfig:
		score += 20.0
	case ChangeTypeCode:
		score += 40.0
	case ChangeTypeInfrastructure:
		score += 60.0
	case ChangeTypeSchema:
		score += 70.0
	case ChangeTypeDependency:
		score += 50.0
	}

	affectedCount := len(result.AffectedServices)
	if affectedCount > 10 {
		score += 30.0
	} else if affectedCount > 5 {
		score += 20.0
	} else if affectedCount > 2 {
		score += 10.0
	}

	apiCount := len(result.AffectedAPIs)
	if apiCount > 20 {
		score += 20.0
	} else if apiCount > 10 {
		score += 10.0
	}

	topicCount := len(result.AffectedTopics)
	if topicCount > 10 {
		score += 15.0
	} else if topicCount > 5 {
		score += 7.0
	}

	paymentCount := len(result.AffectedPayments)
	if paymentCount > 0 {
		score += float64(paymentCount) * 10.0
	}

	if score > 100 {
		score = 100.0
	}

	return score
}

func (a *BlastRadiusAnalyzer) determineRiskLevel(score float64) RiskLevel {
	switch {
	case score >= 80:
		return RiskLevelCritical
	case score >= 60:
		return RiskLevelHigh
	case score >= 40:
		return RiskLevelMedium
	default:
		return RiskLevelLow
	}
}

func (a *BlastRadiusAnalyzer) estimateUsersAffected(result *BlastRadiusResult) int {
	estimated := 0

	for _, path := range result.AffectedPayments {
		switch path {
		case "card_payment", "bank_transfer":
			estimated += 50000
		case "internal_transfer":
			estimated += 20000
		case "payment":
			estimated += 30000
		}
	}

	for _, api := range result.AffectedAPIs {
		if api == "/api/v1/auth/login" {
			estimated += 100000
		} else if api == "/api/v1/users" {
			estimated += 80000
		}
	}

	return estimated
}

func (a *BlastRadiusAnalyzer) estimateRevenueAtRisk(result *BlastRadiusResult) float64 {
	revenue := 0.0

	for _, path := range result.AffectedPayments {
		switch path {
		case "card_payment":
			revenue += 25000.0
		case "bank_transfer":
			revenue += 15000.0
		case "internal_transfer":
			revenue += 5000.0
		case "reconciliation":
			revenue += 10000.0
		}
	}

	if result.RiskLevel == RiskLevelCritical {
		revenue *= 2.0
	} else if result.RiskLevel == RiskLevelHigh {
		revenue *= 1.5
	}

	return revenue
}

func (a *BlastRadiusAnalyzer) generateRecommendations(req ChangeRequest, result *BlastRadiusResult) []string {
	recs := make([]string, 0)

	if result.RiskLevel == RiskLevelCritical || result.RiskLevel == RiskLevelHigh {
		recs = append(recs, "Perform canary deployment before full rollout")
		recs = append(recs, "Ensure rollback plan is tested")
		recs = append(recs, "Monitor error rates during deployment")
	}

	if len(result.AffectedPayments) > 0 {
		recs = append(recs, "Verify payment flow integrity after change")
		recs = append(recs, "Run financial invariant checks")
	}

	if len(result.AffectedTopics) > 5 {
		recs = append(recs, "Verify Kafka consumer group offsets after deployment")
	}

	if req.ChangeType == ChangeTypeSchema {
		recs = append(recs, "Verify backward compatibility of schema changes")
		recs = append(recs, "Run migration scripts before deployment")
	}

	if req.ChangeType == ChangeTypeInfrastructure {
		recs = append(recs, "Verify infrastructure capacity before change")
		recs = append(recs, "Enable enhanced monitoring during change window")
	}

	if len(result.AffectedServices) > 5 {
		recs = append(recs, "Consider staged rollout to reduce blast radius")
		recs = append(recs, "Coordinate with dependent teams")
	}

	return recs
}
