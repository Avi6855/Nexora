// Package economics implements Nexora's operational-economics engines.
//
//  38. Journey cost attribution: every customer journey carries an
//     infrastructure price. Attributing £0.0021 to a card-payment journey
//     turns "optimise latency" into "optimise the 40% of cost in the fraud
//     call" — engineering economics become legible.
//  39. Reliability cost optimiser: availability is purchasable, but the price
//     curve is steep. This engine quantifies the marginal cost of each extra
//     nine against the measured customer impact, so 99.999% is a decision,
//     not a habit.
package economics

import (
	"fmt"
	"sort"
)

// ── 38. Journey cost attribution ────────────────────────────────────────────

// ServiceCost is one service's measured infrastructure spend and traffic.
type ServiceCost struct {
	Service        string  `json:"service"`
	MonthlyCostGBP float64 `json:"monthly_cost_gbp"`
	// MonthlyRequests attributable to journeys.
	MonthlyRequests float64 `json:"monthly_requests"`
}

// JourneyStep is one hop in a journey.
type JourneyStep struct {
	Service string `json:"service"`
	// RequestsPerJourney: how many requests this journey makes to the
	// service (fan-out included).
	RequestsPerJourney float64 `json:"requests_per_journey"`
}

// Journey is a named customer flow.
type Journey struct {
	Name  string        `json:"name"`
	Steps []JourneyStep `json:"steps"`
	// MonthlyVolume of journeys.
	MonthlyVolume float64 `json:"monthly_volume"`
}

// CostBreakdown attributes spend to one journey.
type CostBreakdown struct {
	Journey           string             `json:"journey"`
	CostPerJourneyGBP float64            `json:"cost_per_journey_gbp"`
	MonthlyCostGBP    float64            `json:"monthly_cost_gbp"`
	PerService        map[string]float64 `json:"per_service_gbp"`
	// TopCostDriver is the service to optimise first.
	TopCostDriver string `json:"top_cost_driver"`
}

// AttributeJourney computes the per-journey price from measured unit costs.
func AttributeJourney(j Journey, costs []ServiceCost) (*CostBreakdown, error) {
	unit := map[string]float64{}
	for _, c := range costs {
		if c.MonthlyRequests <= 0 {
			return nil, fmt.Errorf("service %s has zero request volume — cannot derive unit cost", c.Service)
		}
		unit[c.Service] = c.MonthlyCostGBP / c.MonthlyRequests
	}
	bd := &CostBreakdown{Journey: j.Name, PerService: map[string]float64{}}
	for _, s := range j.Steps {
		u, ok := unit[s.Service]
		if !ok {
			return nil, fmt.Errorf("journey %s uses service %s with no cost data", j.Name, s.Service)
		}
		bd.PerService[s.Service] += u * s.RequestsPerJourney
		bd.CostPerJourneyGBP += u * s.RequestsPerJourney
	}
	top := ""
	for svc, amt := range bd.PerService {
		if top == "" || amt > bd.PerService[top] {
			top = svc
		}
	}
	bd.TopCostDriver = top
	bd.MonthlyCostGBP = bd.CostPerJourneyGBP * j.MonthlyVolume
	return bd, nil
}

// RankJourneys returns journeys by monthly cost, descending — where the
// optimisation effort pays.
func RankJourneys(bs []CostBreakdown) []CostBreakdown {
	out := append([]CostBreakdown(nil), bs...)
	sort.Slice(out, func(i, j int) bool { return out[i].MonthlyCostGBP > out[j].MonthlyCostGBP })
	return out
}

// ── 39. Reliability cost optimiser ──────────────────────────────────────────

// AvailabilityTier is one purchasable reliability level.
type AvailabilityTier struct {
	Nines          float64 `json:"nines"`             // 99.9, 99.95, 99.99, 99.999
	InfraCostGBPMo float64 `json:"infra_cost_gbp_mo"` // monthly cost of this tier
	// DowntimeMinutes/month implied by the availability.
	DowntimeMinutes float64 `json:"downtime_minutes_per_month"`
}

// ImpactModel quantifies what a minute of downtime costs the customer and
// the bank.
type ImpactModel struct {
	// CustomersPerMinute affected during downtime.
	CustomersPerMinute float64 `json:"customers_per_minute"`
	// SupportCostPerCustomer per incident-affected customer (£ calls, chats).
	SupportCostPerCustomer float64 `json:"support_cost_per_customer"`
	// ChurnPerMinute: fraction of affected customers who churn per down minute.
	ChurnPerMinute float64 `json:"churn_per_minute"`
	// LTVGBP average customer lifetime value.
	LTVGBP float64 `json:"ltv_gbp"`
	// ReputationalGBP is a flat penalty per incident (app-store ratings,
	// press) — deliberately coarse and documented.
	ReputationalGBP float64 `json:"reputational_gbp_per_incident"`
}

// ReliabilityDecision compares tiers on total cost of ownership.
type ReliabilityDecision struct {
	Recommended AvailabilityTier `json:"recommended"`
	Reasoning   string           `json:"reasoning"`
	Comparison  []TierComparison `json:"comparison"`
}

// TierComparison is one tier's total monthly cost.
type TierComparison struct {
	Tier       AvailabilityTier `json:"tier"`
	InfraCost  float64          `json:"infra_cost_gbp_mo"`
	ImpactCost float64          `json:"impact_cost_gbp_mo"`
	TotalCost  float64          `json:"total_cost_gbp_mo"`
}

// OptimiseReliability picks the tier minimising infra cost + expected
// customer impact. The 99.999% tier wins only when downtime actually hurts
// more than it costs — which for an internal tool it never does.
func OptimiseReliability(tiers []AvailabilityTier, m ImpactModel) (*ReliabilityDecision, error) {
	if len(tiers) == 0 {
		return nil, fmt.Errorf("no availability tiers provided")
	}
	dec := &ReliabilityDecision{}
	for _, t := range tiers {
		minutes := t.DowntimeMinutes
		if minutes <= 0 {
			// Derive from nines: 43,200 minutes/month.
			avail := t.Nines
			if avail >= 100 {
				avail = 100
			}
			minutes = 43200 * (1 - avail/100)
		}
		impact := minutes*m.CustomersPerMinute*
			(m.SupportCostPerCustomer+m.ChurnPerMinute*m.LTVGBP) +
			m.ReputationalGBP // flat per-incident penalty once any downtime occurs
		if minutes == 0 {
			impact = 0
		}
		cmp := TierComparison{Tier: t, InfraCost: t.InfraCostGBPMo, ImpactCost: round2(impact),
			TotalCost: round2(t.InfraCostGBPMo + impact)}
		dec.Comparison = append(dec.Comparison, cmp)
	}
	sort.Slice(dec.Comparison, func(i, j int) bool {
		return dec.Comparison[i].TotalCost < dec.Comparison[j].TotalCost
	})
	best := dec.Comparison[0]
	dec.Recommended = best.Tier
	dec.Reasoning = fmt.Sprintf(
		"tier %.3f%%: £%.0f infra + £%.0f expected impact = £%.0f/month — cheapest total cost",
		best.Tier.Nines, best.InfraCost, best.ImpactCost, best.TotalCost)
	return dec, nil
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
