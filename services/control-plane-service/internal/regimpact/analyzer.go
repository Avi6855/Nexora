// Package regimpact implements the Regulatory Change Impact Analyzer.
//
// When a new UK banking regulation lands, the question is never "does the
// policy doc change?" — it is "which services, APIs, data models and
// CUSTOMERS does this touch, and how do we prove we found all of them?".
// This package answers that by walking the platform dependency graph and the
// service capability registry, propagating rule applicability along real call
// paths, and quantifying blast radius the same way the change-risk analyzer
// does — but from the regulation inward, not the change outward.
package regimpact

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Domain is a regulatory domain a rule can belong to.
type Domain string

const (
	DomainPayments       Domain = "PAYMENTS"
	DomainCards          Domain = "CARDS"
	DomainAccounts       Domain = "ACCOUNTS"
	DomainKYC            Domain = "KYC"
	DomainDataProtection Domain = "DATA_PROTECTION"
	DomainFraud          Domain = "FRAUD"
	DomainComplaints     Domain = "COMPLAINTS"
)

// Rule is an incoming (or proposed) regulatory requirement.
type Rule struct {
	RuleID      string `json:"rule_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Domain      Domain `json:"domain"`
	// ProductScopes narrows applicability, e.g. ["personal_current_account"].
	// Empty = applies to every product.
	ProductScopes []string `json:"product_scopes,omitempty"`
	// EffectiveFrom drives the deadline math.
	EffectiveFrom time.Time `json:"effective_from"`
}

// Capability is what a service does that a rule may regulate. Tagging services
// with capabilities (rather than hard-coding "rule X hits service Y") is what
// makes the analyzer general: a NEW rule needs no code change, only that
// services honestly declare what they do.
type Capability struct {
	Name string `json:"name"` // e.g. "process_card_payment"
	// DataClasses processed, e.g. ["transaction_history", "kyc_documents"].
	DataClasses []string `json:"data_classes"`
	// CustomerFacing capabilities create direct customer impact.
	CustomerFacing bool `json:"customer_facing"`
	// APIs that implement this capability.
	APIs []string `json:"apis"`
	// DataModels touched, e.g. ["payments", "accounts"].
	DataModels []string `json:"data_models"`
}

// ServiceRegistration is a service's declared capabilities.
type ServiceRegistration struct {
	Service      string       `json:"service"`
	Capabilities []Capability `json:"capabilities"`
	// DependsOn propagation edges (same wiring as the dependency graph).
	DependsOn []string `json:"depends_on"`
}

// Impact is one service's exposure to a rule.
type Impact struct {
	Service        string   `json:"service"`
	MatchedCaps    []string `json:"matched_capabilities"`
	APIs           []string `json:"apis"`
	DataModels     []string `json:"data_models"`
	DataClasses    []string `json:"data_classes"`
	HopsFromSource int      `json:"hops_from_source"`
	CustomerFacing bool     `json:"customer_facing"`
	// Indirect: reached only via downstream propagation (needs review — the
	// owning team may not have the rule on their radar).
	Indirect bool `json:"indirect"`
}

// Analysis is the full impact assessment.
type Analysis struct {
	RuleID             string    `json:"rule_id"`
	Title              string    `json:"title"`
	AnalyzedAt         time.Time `json:"analyzed_at"`
	EffectiveFrom      time.Time `json:"effective_from"`
	DaysUntilEffective int       `json:"days_until_effective"`
	AffectedServices   []Impact  `json:"affected_services"`
	UniqueAPIs         []string  `json:"unique_apis"`
	UniqueDataModels   []string  `json:"unique_data_models"`
	UniqueDataClasses  []string  `json:"unique_data_classes"`
	EstimatedCustomers int       `json:"estimated_customers_affected"`
	RiskLevel          string    `json:"risk_level"`
	Summary            string    `json:"summary"`
}

// Analyzer walks the platform graph for a rule.
type Analyzer struct {
	mu      sync.RWMutex
	catalog map[string]*ServiceRegistration
	// customersPerProduct feeds the customer estimate (docs: 16M-scale bank).
	customersPerProduct map[string]int
}

func NewAnalyzer() *Analyzer {
	return &Analyzer{
		catalog: map[string]*ServiceRegistration{},
		customersPerProduct: map[string]int{
			"personal_current_account": 10_000_000,
			"business_current_account": 500_000,
			"joint_account":            2_000_000,
			"savings":                  3_000_000,
		},
	}
}

// Register adds/updates a service's capability declaration.
func (a *Analyzer) Register(reg *ServiceRegistration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.catalog[reg.Service] = reg
}

// Unregister removes a service (decommissioning support).
func (a *Analyzer) Unregister(service string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.catalog, service)
}

// Registered lists registered services (deterministic order).
func (a *Analyzer) Registered() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, 0, len(a.catalog))
	for k := range a.catalog {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Analyze computes the full impact of a rule.
func (a *Analyzer) Analyze(rule Rule) *Analysis {
	a.mu.RLock()
	defer a.mu.RUnlock()

	now := time.Now().UTC()
	analysis := &Analysis{
		RuleID:        rule.RuleID,
		Title:         rule.Title,
		AnalyzedAt:    now,
		EffectiveFrom: rule.EffectiveFrom,
	}
	if d := int(rule.EffectiveFrom.Sub(now).Hours() / 24); d > 0 {
		analysis.DaysUntilEffective = d
	}

	// 1. Direct matches: services whose capabilities the rule regulates.
	direct := map[string]*Impact{}
	for name, reg := range a.catalog {
		imp := matchRule(rule, reg)
		if imp != nil {
			direct[name] = imp
		}
	}

	// 2. Propagate downstream: anything depending on an affected service is
	// indirectly affected (its inputs change semantics).
	impacts := map[string]*Impact{}
	for name, imp := range direct {
		impacts[name] = imp
	}
	frontier := make([]string, 0, len(direct))
	for name := range direct {
		frontier = append(frontier, name)
	}
	hops := 0
	visited := map[string]bool{}
	for len(frontier) > 0 && hops < 8 { // bounded: the platform is ~20 services
		hops++
		var next []string
		for _, name := range frontier {
			visited[name] = true
			for _, reg := range a.catalog {
				if visited[reg.Service] {
					continue
				}
				if !contains(reg.DependsOn, name) {
					continue
				}
				visited[reg.Service] = true
				existing, ok := impacts[reg.Service]
				if !ok {
					existing = &Impact{Service: reg.Service, Indirect: true, HopsFromSource: hops}
					impacts[reg.Service] = existing
					next = append(next, reg.Service)
				}
				if hops < existing.HopsFromSource {
					existing.HopsFromSource = hops
				}
			}
		}
		frontier = next
	}

	// 3. Aggregate.
	names := make([]string, 0, len(impacts))
	for n := range impacts {
		names = append(names, n)
	}
	sort.Strings(names)
	analysis.AffectedServices = make([]Impact, 0, len(names))
	apiSet := map[string]bool{}
	modelSet := map[string]bool{}
	classSet := map[string]bool{}
	anyCustomerFacing := false
	for _, n := range names {
		imp := impacts[n]
		analysis.AffectedServices = append(analysis.AffectedServices, *imp)
		for _, api := range imp.APIs {
			apiSet[api] = true
		}
		for _, m := range imp.DataModels {
			modelSet[m] = true
		}
		for _, c := range imp.DataClasses {
			classSet[c] = true
		}
		if imp.CustomerFacing {
			anyCustomerFacing = true
		}
	}
	analysis.UniqueAPIs = sortedKeys(apiSet)
	analysis.UniqueDataModels = sortedKeys(modelSet)
	analysis.UniqueDataClasses = sortedKeys(classSet)

	// 4. Customer estimate: union of products in scope.
	analysis.EstimatedCustomers = a.estimateCustomers(rule)
	analysis.RiskLevel = a.riskLevel(len(names), anyCustomerFacing, analysis.DaysUntilEffective)
	analysis.Summary = fmt.Sprintf("rule %s (%s): %d services affected (%d direct), %d APIs, %d data models, ~%d customers; %d days to comply",
		rule.RuleID, rule.Domain, len(names), len(direct), len(analysis.UniqueAPIs), len(analysis.UniqueDataModels),
		analysis.EstimatedCustomers, analysis.DaysUntilEffective)
	return analysis
}

// matchRule decides whether a rule regulates a service's declared capabilities.
func matchRule(rule Rule, reg *ServiceRegistration) *Impact {
	var matched []Capability
	for _, cap := range reg.Capabilities {
		if ruleAppliesToCapability(rule, cap) {
			matched = append(matched, cap)
		}
	}
	if len(matched) == 0 {
		return nil
	}
	imp := &Impact{Service: reg.Service, HopsFromSource: 0}
	for _, c := range matched {
		imp.MatchedCaps = append(imp.MatchedCaps, c.Name)
		imp.APIs = append(imp.APIs, c.APIs...)
		imp.DataModels = append(imp.DataModels, c.DataModels...)
		imp.DataClasses = append(imp.DataClasses, c.DataClasses...)
		if c.CustomerFacing {
			imp.CustomerFacing = true
		}
	}
	return imp
}

// ruleAppliesToCapability encodes domain semantics: a payments rule regulates
// capabilities that process payment data, etc. Product scoping narrows further.
func ruleAppliesToCapability(rule Rule, cap Capability) bool {
	for _, dc := range cap.DataClasses {
		switch rule.Domain {
		case DomainPayments:
			if dc == "payments" || dc == "transaction_history" {
				return true
			}
		case DomainCards:
			if dc == "cards" || dc == "card_credentials" || dc == "transaction_history" {
				return true
			}
		case DomainAccounts:
			if dc == "accounts" || dc == "balances" {
				return true
			}
		case DomainKYC:
			if dc == "kyc_documents" || dc == "identity" {
				return true
			}
		case DomainDataProtection:
			// Data-protection rules apply to ANY personal data class.
			if isPersonalData(dc) {
				return true
			}
		case DomainFraud:
			if dc == "fraud_signals" || dc == "transaction_history" || dc == "device_fingerprints" {
				return true
			}
		case DomainComplaints:
			if dc == "complaints" || dc == "support_cases" {
				return true
			}
		}
	}
	return false
}

func isPersonalData(dc string) bool {
	switch dc {
	case "identity", "kyc_documents", "transaction_history", "contact_details",
		"device_fingerprints", "balances", "accounts", "cards":
		return true
	}
	return false
}

func (a *Analyzer) estimateCustomers(rule Rule) int {
	total := 0
	for product, n := range a.customersPerProduct {
		if len(rule.ProductScopes) == 0 || contains(rule.ProductScopes, product) {
			total += n
		}
	}
	return total
}

func (a *Analyzer) riskLevel(services int, customerFacing bool, days int) string {
	switch {
	case services == 0:
		return "NONE"
	case services >= 8 || (customerFacing && days > 0 && days <= 30):
		return "CRITICAL"
	case services >= 4 || customerFacing:
		return "HIGH"
	case services >= 2:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
