package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
)

type PolicyRepository interface {
	Create(ctx context.Context, policy *domain.Policy) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Policy, error)
	GetByType(ctx context.Context, policyType domain.PolicyType) ([]*domain.Policy, error)
	GetByStatus(ctx context.Context, status domain.PolicyStatus) ([]*domain.Policy, error)
	GetActivePolicies(ctx context.Context) ([]*domain.Policy, error)
	GetShadowPolicies(ctx context.Context) ([]*domain.Policy, error)
	Update(ctx context.Context, policy *domain.Policy) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PolicyStatus) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type cassandraPolicyRepository struct {
	session *gocql.Session
}

func NewCassandraPolicyRepository(session *gocql.Session) PolicyRepository {
	return &cassandraPolicyRepository{session: session}
}

func (r *cassandraPolicyRepository) Create(ctx context.Context, policy *domain.Policy) error {
	query := `INSERT INTO policies (policy_id, name, description, policy_type, scope, status, rules, enabled, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rulesJSON := serializePolicyRules(policy.Rules)
	return r.session.Query(query,
		policy.PolicyID, policy.Name, policy.Description,
		string(policy.PolicyType), string(policy.Scope), string(policy.Status),
		rulesJSON, policy.Enabled, policy.Version,
		policy.CreatedAt, policy.UpdatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraPolicyRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Policy, error) {
	var p domain.Policy
	var policyType, scope, status string
	var rulesJSON string
	query := `SELECT policy_id, name, description, policy_type, scope, status, rules, enabled, version, created_at, updated_at
		FROM policies WHERE policy_id = ?`
	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&p.PolicyID, &p.Name, &p.Description,
		&policyType, &scope, &status,
		&rulesJSON, &p.Enabled, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("policy not found")
	}
	if err != nil {
		return nil, err
	}
	p.PolicyType = domain.PolicyType(policyType)
	p.Scope = domain.PolicyScope(scope)
	p.Status = domain.PolicyStatus(status)
	p.Rules = deserializePolicyRules(rulesJSON)
	return &p, nil
}

func (r *cassandraPolicyRepository) GetByType(ctx context.Context, policyType domain.PolicyType) ([]*domain.Policy, error) {
	var policies []*domain.Policy
	query := `SELECT policy_id, name, description, policy_type, scope, status, rules, enabled, version, created_at, updated_at
		FROM policies WHERE policy_type = ? ALLOW FILTERING`
	iter := r.session.Query(query, string(policyType)).WithContext(ctx).Iter()
	defer iter.Close()
	policies = scanPolicies(iter)
	return policies, nil
}

func (r *cassandraPolicyRepository) GetByStatus(ctx context.Context, status domain.PolicyStatus) ([]*domain.Policy, error) {
	query := `SELECT policy_id, name, description, policy_type, scope, status, rules, enabled, version, created_at, updated_at
		FROM policies WHERE status = ? ALLOW FILTERING`
	iter := r.session.Query(query, string(status)).WithContext(ctx).Iter()
	defer iter.Close()
	return scanPolicies(iter), nil
}

func (r *cassandraPolicyRepository) GetActivePolicies(ctx context.Context) ([]*domain.Policy, error) {
	return r.GetByStatus(ctx, domain.PolicyStatusActive)
}

func (r *cassandraPolicyRepository) GetShadowPolicies(ctx context.Context) ([]*domain.Policy, error) {
	return r.GetByStatus(ctx, domain.PolicyStatusShadow)
}

func (r *cassandraPolicyRepository) Update(ctx context.Context, policy *domain.Policy) error {
	now := time.Now().UTC()
	rulesJSON := serializePolicyRules(policy.Rules)
	query := `UPDATE policies SET name = ?, description = ?, policy_type = ?, scope = ?, rules = ?, enabled = ?, version = ?, updated_at = ? WHERE policy_id = ?`
	return r.session.Query(query,
		policy.Name, policy.Description,
		string(policy.PolicyType), string(policy.Scope),
		rulesJSON, policy.Enabled, policy.Version, now,
		policy.PolicyID,
	).WithContext(ctx).Exec()
}

func (r *cassandraPolicyRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PolicyStatus) error {
	now := time.Now().UTC()
	query := `UPDATE policies SET status = ?, updated_at = ? WHERE policy_id = ?`
	return r.session.Query(query, string(status), now, id).WithContext(ctx).Exec()
}

func (r *cassandraPolicyRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM policies WHERE policy_id = ?`
	return r.session.Query(query, id).WithContext(ctx).Exec()
}

func scanPolicies(iter *gocql.Iter) []*domain.Policy {
	var policies []*domain.Policy
	var p domain.Policy
	var policyType, scope, status string
	var rulesJSON string
	for iter.Scan(
		&p.PolicyID, &p.Name, &p.Description,
		&policyType, &scope, &status,
		&rulesJSON, &p.Enabled, &p.Version,
		&p.CreatedAt, &p.UpdatedAt,
	) {
		p.PolicyType = domain.PolicyType(policyType)
		p.Scope = domain.PolicyScope(scope)
		p.Status = domain.PolicyStatus(status)
		p.Rules = deserializePolicyRules(rulesJSON)
		pp := p
		policies = append(policies, &pp)
	}
	return policies
}

func serializePolicyRules(rules []domain.PolicyRule) string {
	if len(rules) == 0 {
		return "[]"
	}
	result := "["
	for i, rule := range rules {
		if i > 0 {
			result += ","
		}
		result += fmt.Sprintf(`{"rule_id":"%s","name":"%s","condition":"%s","action":"%s","priority":%d,"enabled":%t}`,
			rule.RuleID, rule.Name, rule.Condition, string(rule.Action), rule.Priority, rule.Enabled)
	}
	result += "]"
	return result
}

func deserializePolicyRules(jsonStr string) []domain.PolicyRule {
	if jsonStr == "" || jsonStr == "[]" {
		return make([]domain.PolicyRule, 0)
	}
	rules := make([]domain.PolicyRule, 0)
	jsonStr = jsonStr[1 : len(jsonStr)-1]
	if len(jsonStr) == 0 {
		return rules
	}
	parts := splitJSONObjects(jsonStr)
	for _, part := range parts {
		rule := domain.PolicyRule{Enabled: true}
		rule.RuleID = extractJSONString(part, "rule_id")
		rule.Name = extractJSONString(part, "name")
		rule.Condition = extractJSONString(part, "condition")
		action := extractJSONString(part, "action")
		rule.Action = domain.PolicyDecisionAction(action)
		rule.Enabled = true
		rules = append(rules, rule)
	}
	return rules
}

func splitJSONObjects(s string) []string {
	var objects []string
	depth := 0
	start := -1
	for i, c := range s {
		if c == '{' {
			if depth == 0 {
				start = i
			}
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 && start >= 0 {
				objects = append(objects, s[start:i+1])
				start = -1
			}
		}
	}
	return objects
}

func extractJSONString(s, key string) string {
	search := fmt.Sprintf(`"%s":"`, key)
	idx := 0
	for i := 0; i < len(s)-len(search); i++ {
		match := true
		for j := 0; j < len(search); j++ {
			if s[i+j] != search[j] {
				match = false
				break
			}
		}
		if match {
			idx = i + len(search)
			break
		}
	}
	if idx == 0 {
		return ""
	}
	end := idx
	for end < len(s) && s[end] != '"' {
		end++
	}
	return s[idx:end]
}
