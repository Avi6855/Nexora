// Package kyc implements Nexora's KYC Platform core engines.
//
//  1. Progressive Verification: customers start at the lowest verification
//     tier their risk allows; escalating risk (behaviour, volume, geography)
//     demands escalating evidence. Full verification for everyone up-front
//     harms conversion AND concentrates PII; tiering is both a compliance
//     and a data-minimisation strategy.
//  2. Evidence Freshness: every evidence type has its own validity window
//     (identity documents outlive address proofs; business ownership
//     outlives both). The engine answers fresh/stale/expired/requires-refresh
//     per type and drives the refresh pipeline.
package kyc

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// ── Progressive verification ────────────────────────────────────────────────

// Tier is the verification level a customer currently satisfies.
type Tier int

const (
	TierBasic    Tier = 1 // email + phone verified
	TierStandard Tier = 2 // government ID verified
	TierEnhanced Tier = 3 // ID + address + liveness
	TierBusiness Tier = 4 // + business ownership / UBO evidence
)

// String renders the tier name.
func (t Tier) String() string {
	switch t {
	case TierBasic:
		return "BASIC"
	case TierStandard:
		return "STANDARD"
	case TierEnhanced:
		return "ENHANCED"
	case TierBusiness:
		return "BUSINESS"
	default:
		return "UNKNOWN"
	}
}

// RiskLevel drives the required tier.
type RiskLevel string

const (
	RiskLow        RiskLevel = "LOW"
	RiskMedium     RiskLevel = "MEDIUM"
	RiskHigh       RiskLevel = "HIGH"
	RiskProhibited RiskLevel = "PROHIBITED"
)

// Requirements encodes the risk→tier policy. It is DATA, not code paths, so
// a policy change (e.g. new regulation raises HIGH to enhanced) is a config
// change reviewed like any other.
type Requirements struct {
	Low    Tier `json:"low"`
	Medium Tier `json:"medium"`
	High   Tier `json:"high"`
	// MaxBalanceByTier caps what each tier may hold (minor units).
	MaxBalanceByTier map[Tier]int64 `json:"max_balance_by_tier"`
}

// DefaultRequirements is the documented starting policy.
func DefaultRequirements() Requirements {
	return Requirements{
		Low:    TierBasic,
		Medium: TierStandard,
		High:   TierEnhanced,
		MaxBalanceByTier: map[Tier]int64{
			TierBasic:    100_000,    // £1,000
			TierStandard: 2_000_000,  // £20,000
			TierEnhanced: 50_000_000, // £500,000
			TierBusiness: 1_000_000_000,
		},
	}
}

// Customer is the verification subject.
type Customer struct {
	ID           string
	CurrentTier  Tier
	RiskLevel    RiskLevel
	BalanceMinor int64
	// OpenInvestigations blocks tier upgrades (and most downgrades).
	OpenInvestigations bool
}

// ErrProhibited is terminal: the customer cannot hold an account.
var ErrProhibited = errors.New("customer risk level is prohibited")

// ErrTierInsufficient means the customer's evidence does not satisfy the
// risk-mandated tier.
var ErrTierInsufficient = errors.New("verification tier insufficient for current risk level")

// ErrBalanceExceedsTier means the customer holds more than their tier allows.
var ErrBalanceExceedsTier = errors.New("balance exceeds the cap for the current verification tier")

// Engine evaluates progressive verification.
type Engine struct {
	req Requirements
	now func() time.Time
}

func NewEngine(req Requirements) *Engine {
	return &Engine{req: req, now: time.Now}
}

// RequiredTier maps risk level to the minimum tier.
func (e *Engine) RequiredTier(r RiskLevel) (Tier, error) {
	switch r {
	case RiskLow:
		return e.req.Low, nil
	case RiskMedium:
		return e.req.Medium, nil
	case RiskHigh:
		return e.req.High, nil
	case RiskProhibited:
		return 0, ErrProhibited
	default:
		return 0, fmt.Errorf("unknown risk level %q", r)
	}
}

// Evaluate decides the customer's compliance state.
type Evaluation struct {
	CurrentTier  Tier   `json:"current_tier"`
	RequiredTier Tier   `json:"required_tier"`
	Compliant    bool   `json:"compliant"`
	UpgradeTo    Tier   `json:"upgrade_to,omitempty"`
	BlockedBy    string `json:"blocked_by,omitempty"`
}

func (e *Engine) Evaluate(c Customer) (*Evaluation, error) {
	required, err := e.RequiredTier(c.RiskLevel)
	if err != nil {
		return nil, err
	}
	ev := &Evaluation{CurrentTier: c.CurrentTier, RequiredTier: required}
	if c.CurrentTier < required {
		ev.Compliant = false
		ev.UpgradeTo = required
		if c.OpenInvestigations {
			ev.BlockedBy = "open_investigation"
		}
		return ev, nil
	}
	if cap, ok := e.req.MaxBalanceByTier[c.CurrentTier]; ok && c.BalanceMinor > cap {
		ev.Compliant = false
		ev.BlockedBy = "balance_exceeds_tier"
		return ev, nil
	}
	ev.Compliant = true
	return ev, nil
}

// ── Evidence freshness ──────────────────────────────────────────────────────

// EvidenceType enumerates verifiable evidence classes — each with its own
// freshness window.
type EvidenceType string

const (
	EvidenceIdentityDoc     EvidenceType = "IDENTITY_DOCUMENT"
	EvidenceAddress         EvidenceType = "ADDRESS_PROOF"
	EvidenceBusinessOwner   EvidenceType = "BUSINESS_OWNERSHIP"
	EvidenceSanctionsScreen EvidenceType = "SANCTIONS_SCREENING"
)

// Freshness is the state of one evidence item.
type Freshness string

const (
	Fresh           Freshness = "FRESH"
	Stale           Freshness = "STALE"            // past refresh date, still inside hard expiry
	Expired         Freshness = "EXPIRED"          // past hard expiry — evidence unusable
	RequiresRefresh Freshness = "REQUIRES_REFRESH" // refresh pipeline must be triggered
)

// RetentionPolicy defines the windows for one evidence type.
type RetentionPolicy struct {
	// RefreshAfter: evidence older than this should be re-collected.
	RefreshAfter time.Duration
	// HardExpiry: evidence older than this is unusable for compliance.
	HardExpiry time.Duration
}

// DefaultPolicies are the documented windows.
func DefaultPolicies() map[EvidenceType]RetentionPolicy {
	return map[EvidenceType]RetentionPolicy{
		EvidenceIdentityDoc:     {RefreshAfter: 5 * 365 * 24 * time.Hour, HardExpiry: 10 * 365 * 24 * time.Hour},
		EvidenceAddress:         {RefreshAfter: 12 * 30 * 24 * time.Hour, HardExpiry: 24 * 30 * 24 * time.Hour},
		EvidenceBusinessOwner:   {RefreshAfter: 2 * 365 * 24 * time.Hour, HardExpiry: 5 * 365 * 24 * time.Hour},
		EvidenceSanctionsScreen: {RefreshAfter: 24 * time.Hour, HardExpiry: 7 * 24 * time.Hour},
	}
}

// EvidenceItem is one collected artifact.
type EvidenceItem struct {
	Type        EvidenceType `json:"type"`
	CollectedAt time.Time    `json:"collected_at"`
}

// FreshnessEngine classifies evidence by age.
type FreshnessEngine struct {
	policies map[EvidenceType]RetentionPolicy
	now      func() time.Time
}

func NewFreshnessEngine(policies map[EvidenceType]RetentionPolicy) *FreshnessEngine {
	if policies == nil {
		policies = DefaultPolicies()
	}
	return &FreshnessEngine{policies: policies, now: time.Now}
}

// Classify returns the freshness of one item.
func (e *FreshnessEngine) Classify(item EvidenceItem) (Freshness, error) {
	pol, ok := e.policies[item.Type]
	if !ok {
		return "", fmt.Errorf("no retention policy for evidence type %q", item.Type)
	}
	age := e.now().Sub(item.CollectedAt)
	switch {
	case age >= pol.HardExpiry:
		return Expired, nil
	case age >= pol.RefreshAfter:
		return RequiresRefresh, nil
	default:
		return Fresh, nil
	}
}

// ClassifyAll sorts a customer's evidence by urgency: EXPIRED first.
type EvidenceStatus struct {
	Item      EvidenceItem `json:"item"`
	Freshness Freshness    `json:"freshness"`
}

func (e *FreshnessEngine) ClassifyAll(items []EvidenceItem) ([]EvidenceStatus, error) {
	out := make([]EvidenceStatus, 0, len(items))
	for _, it := range items {
		f, err := e.Classify(it)
		if err != nil {
			return nil, err
		}
		out = append(out, EvidenceStatus{Item: it, Freshness: f})
	}
	rank := map[Freshness]int{Expired: 0, RequiresRefresh: 1, Stale: 2, Fresh: 3}
	sort.Slice(out, func(i, j int) bool { return rank[out[i].Freshness] < rank[out[j].Freshness] })
	return out, nil
}

// SatisfiedForTier checks whether the evidence set covers the tier's required
// evidence types with non-expired items.
func (e *FreshnessEngine) SatisfiedForTier(tier Tier, items []EvidenceItem) (bool, []EvidenceType, error) {
	required := map[Tier][]EvidenceType{
		TierBasic:    {},
		TierStandard: {EvidenceIdentityDoc},
		TierEnhanced: {EvidenceIdentityDoc, EvidenceAddress},
		TierBusiness: {EvidenceIdentityDoc, EvidenceAddress, EvidenceBusinessOwner},
	}
	need := required[tier]
	fresh := map[EvidenceType]bool{}
	for _, it := range items {
		f, err := e.Classify(it)
		if err != nil {
			return false, nil, err
		}
		if f == Fresh {
			fresh[it.Type] = true
		}
	}
	var missing []EvidenceType
	for _, n := range need {
		if !fresh[n] {
			missing = append(missing, n)
		}
	}
	return len(missing) == 0, missing, nil
}
