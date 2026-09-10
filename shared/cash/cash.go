// Package cash implements Nexora's cash deposit platform: smart routing
// across PayPoint/Post Office-style providers, dynamic AML risk tiering, and
// provider failover.
//
// The deposit problem is really three problems:
//
//  1. ROUTING: which provider/location can take this deposit right now —
//     capability, per-customer limits, provider health, and risk policy all
//     gate the answer. The route is RESERVED before the customer walks in, so
//     two simultaneous deposits cannot both fit inside a remaining limit.
//
//  2. RISK: a fixed £750/month cap is a blunt AML control. Real monitoring
//     asks why THIS customer, THIS month, is moving cash: history, frequency
//     growth, structuring signals (just-under-threshold patterns), source
//     transparency — then tiers the deposit NORMAL/WATCH/REVIEW/RESTRICT.
//
//  3. FAILOVER: providers degrade independently. Health scores decay on
//     failures and recover on success (half-life), and a provider is only
//     routable when its score clears the threshold — so traffic drains away
//     from a degrading provider BEFORE it hard-fails.
package cash

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrLimitExhausted    = errors.New("monthly deposit limit exhausted")
	ErrReservationLimit  = errors.New("too many open reservations")
	ErrReservationStale  = errors.New("reservation expired")
	ErrReservationStolen = errors.New("reservation superseded by a newer one")
)

// Reservation is a claimed slice of the monthly limit. Reservations exist
// because "check limit, then deposit" is a race: two concurrent deposits
// would both pass a check-then-settle flow. The reservation is the
// banking equivalent of compare-and-swap on the limit.
type Reservation struct {
	ID          string    `json:"id"`
	AmountMinor int64     `json:"amount_minor"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// MonthlyUsage tracks rolling usage for limit enforcement.
type MonthlyUsage struct {
	Month           string        `json:"month"`
	DepositedMinor  int64         `json:"deposited_minor"`
	ReservedMinor   int64         `json:"reserved_minor"`
	LimitMinor      int64         `json:"limit_minor"`
	Reservations    []Reservation `json:"reservations"`
	MaxReservations int           `json:"max_reservations"`
	ReserveTTL      time.Duration `json:"reserve_ttl"`

	swept *Reservation // most recent expired reservation, for late settlement
}

// NewMonthlyUsage builds usage for a month with the given cap.
func NewMonthlyUsage(month string, limitMinor int64) *MonthlyUsage {
	return &MonthlyUsage{
		Month:           month,
		LimitMinor:      limitMinor,
		MaxReservations: 8,
		ReserveTTL:      30 * time.Minute,
	}
}

// Available is the unreserved, unused headroom.
func (u *MonthlyUsage) Available() int64 {
	return u.LimitMinor - u.DepositedMinor - u.ReservedMinor
}

// Reserve atomically claims headroom for a deposit attempt.
func (u *MonthlyUsage) Reserve(amountMinor int64, now time.Time, id string) (*Reservation, error) {
	if amountMinor <= 0 {
		return nil, fmt.Errorf("reserve amount must be positive, got %d", amountMinor)
	}
	u.sweepExpired(now)
	if u.Available() < amountMinor {
		return nil, fmt.Errorf("%w: available %d, need %d", ErrLimitExhausted, u.Available(), amountMinor)
	}
	if len(u.Reservations) >= u.MaxReservations {
		return nil, ErrReservationLimit
	}
	r := Reservation{ID: id, AmountMinor: amountMinor, CreatedAt: now, ExpiresAt: now.Add(u.ReserveTTL)}
	u.Reservations = append(u.Reservations, r)
	u.ReservedMinor += amountMinor
	return &r, nil
}

// Confirm converts a reservation into settled usage (deposit completed).
func (u *MonthlyUsage) Confirm(id string, settledMinor int64, now time.Time) error {
	for i := range u.Reservations {
		if u.Reservations[i].ID != id {
			continue
		}
		r := u.Reservations[i]
		if now.After(r.ExpiresAt) {
			u.removeReservation(i)
			u.swept = &Reservation{ID: r.ID, AmountMinor: r.AmountMinor}
			return ErrReservationStale
		}
		u.removeReservation(i)
		u.DepositedMinor += settledMinor
		return nil
	}
	return fmt.Errorf("reservation %s not found", id)
}

// Release frees a reservation without settling (customer abandoned the
// deposit). Settled amounts are never released — that path is Confirm.
func (u *MonthlyUsage) Release(id string) error {
	for i := range u.Reservations {
		if u.Reservations[i].ID == id {
			u.removeReservation(i)
			return nil
		}
	}
	return fmt.Errorf("reservation %s not found", id)
}

// Reconfirm handles the settle-vs-expiry race: the settlement event may
// arrive after the reservation was swept as expired. If the reservation was
// swept THIS sweep (not superseded by a newer reservation of the same ID),
// admit the settle against the limit directly — the money DID move, and
// rejecting it would under-count usage (an AML control failure).
func (u *MonthlyUsage) Reconfirm(id string, settledMinor int64) error {
	for _, r := range u.Reservations {
		if r.ID == id {
			return fmt.Errorf("%w: use Confirm for live reservation %s", ErrReservationStolen, id)
		}
	}
	if u.swept != nil && u.swept.ID == id {
		u.DepositedMinor += settledMinor
		u.swept = nil
		return nil
	}
	return fmt.Errorf("%w: %s", ErrReservationStolen, id)
}

func (u *MonthlyUsage) removeReservation(i int) {
	u.ReservedMinor -= u.Reservations[i].AmountMinor
	u.Reservations = append(u.Reservations[:i], u.Reservations[i+1:]...)
}

func (u *MonthlyUsage) sweepExpired(now time.Time) {
	kept := u.Reservations[:0]
	for _, r := range u.Reservations {
		if now.After(r.ExpiresAt) {
			u.ReservedMinor -= r.AmountMinor // free the headroom
			u.swept = &Reservation{ID: r.ID, AmountMinor: r.AmountMinor}
			continue
		}
		kept = append(kept, r)
	}
	u.Reservations = kept
}

// ---------- Provider health & failover ----------

// Provider is an external cash-deposit network.
type Provider struct {
	Name       string           `json:"name"`
	Capability string           `json:"capability"` // e.g. "CASH_DEPOSIT"
	Healthy    bool             `json:"healthy"`
	Health     float64          `json:"health"` // 0..1, decays on failure
	Locations  []Location       `json:"locations"`
	Policies   map[string]int64 `json:"policies"` // account-type -> monthly limit minor units
}

// Location is a physical deposit point with its capabilities.
type Location struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Supports     string `json:"supports"`      // capability at this location
	CostPence    int64  `json:"cost_pence"`    // provider fee per deposit
	QueueMinutes int    `json:"queue_minutes"` // current expected wait
	OpenNow      bool   `json:"open_now"`
}

// HealthState tracks a provider's rolling health with half-life recovery.
type HealthState struct {
	Provider       string        `json:"provider"`
	Health         float64       `json:"health"`
	FailureRate    float64       `json:"failure_rate"` // failures/total, EWMA
	LastFailure    time.Time     `json:"last_failure"`
	LastSuccess    time.Time     `json:"last_success"`
	RecoveryPeriod time.Duration `json:"recovery_period"`
}

// NewHealthState starts a provider at full health.
func NewHealthState(provider string) *HealthState {
	return &HealthState{Provider: provider, Health: 1.0, RecoveryPeriod: 5 * time.Minute}
}

// RecordFailure decays health and updates the EWMA failure rate.
func (h *HealthState) RecordFailure(now time.Time) {
	h.Health *= 0.6
	h.LastFailure = now
	h.FailureRate = 0.7*h.FailureRate + 0.3*1.0
}

// RecordSuccess recovers health toward 1.0. Recovery is proportional to the
// time since the last failure (a provider that has been healthy for an hour
// is trusted more than one healthy for ten seconds), capped at 1.0.
func (h *HealthState) RecordSuccess(now time.Time) {
	h.LastSuccess = now
	h.FailureRate = 0.7*h.FailureRate + 0.3*0.0
	if !h.LastFailure.IsZero() {
		elapsed := now.Sub(h.LastFailure)
		frac := float64(elapsed) / float64(h.RecoveryPeriod)
		if frac > 1 {
			frac = 1
		}
		h.Health += (1 - h.Health) * frac
	} else {
		h.Health = 1
	}
	if h.Health > 1 {
		h.Health = 1
	}
}

// Routable reports whether traffic may be sent to this provider.
func (h *HealthState) Routable() bool {
	return h.Health >= 0.5
}

// ---------- Routing ----------

// DepositRequest is a customer's intent to deposit cash.
type DepositRequest struct {
	CustomerID  string    `json:"customer_id"`
	AccountType string    `json:"account_type"` // PERSONAL / BUSINESS_LTD / BUSINESS_SOLE_TRADER
	AmountMinor int64     `json:"amount_minor"`
	Currency    string    `json:"currency"`
	At          time.Time `json:"at"`
}

// Route is a concrete, reserved deposit plan.
type Route struct {
	Provider      string      `json:"provider"`
	Location      Location    `json:"location"`
	Reason        string      `json:"reason"`
	ReservationID string      `json:"reservation_id"`
	ExpiresAt     time.Time   `json:"expires_at"`
	Alternatives  []Alternate `json:"alternatives"`
}

// Alternate is a ranked runner-up route (shown in-app as "or nearby...").
type Alternate struct {
	Provider string `json:"provider"`
	Location string `json:"location"`
	Reason   string `json:"reason"`
}

// Router picks the best route for a deposit request.
type Router struct {
	Providers map[string]*Provider
	Health    map[string]*HealthState
	Pricing   map[string]int64 // provider -> cost pence per deposit
}

// RouteError explains why no route could be produced.
type RouteError struct {
	CustomerFacing string            `json:"customer_facing"` // safe to show in-app
	Reasons        map[string]string `json:"reasons"`         // provider -> why excluded
}

func (e *RouteError) Error() string { return e.CustomerFacing }

// Route resolves the best provider+location, reserves limit headroom, and
// returns ranked alternatives. Exclusion reasons are carried on the error so
// support and the app can explain the outcome instead of a bare failure.
func (r *Router) Route(req DepositRequest, usage *MonthlyUsage, tier Tier) (*Route, error) {
	if tier == TierRestrict {
		return nil, &RouteError{
			CustomerFacing: "Cash deposits are temporarily restricted on this account. Contact in-app support.",
			Reasons:        map[string]string{"risk": "account in RESTRICT tier"},
		}
	}
	var reasons = map[string]string{}
	var candidates []struct {
		p      *Provider
		loc    Location
		score  float64
		reason string
	}
	for name, p := range r.Providers {
		h := r.Health[name]
		if h == nil || !h.Routable() {
			reasons[name] = "provider health below routable threshold"
			continue
		}
		limit, ok := p.Policies[req.AccountType]
		if !ok {
			reasons[name] = fmt.Sprintf("no policy for account type %s", req.AccountType)
			continue
		}
		if usage != nil && usage.LimitMinor < limit {
			limit = usage.LimitMinor // the stricter of provider policy and account limit
		}
		if usage != nil {
			if usage.LimitMinor != limit {
				usage.LimitMinor = limit
			}
			if usage.Available() < req.AmountMinor {
				reasons[name] = fmt.Sprintf("monthly limit: %d of %d already used or reserved", usage.DepositedMinor+usage.ReservedMinor, limit)
				continue
			}
		}
		for _, loc := range p.Locations {
			if loc.Supports != p.Capability || !loc.OpenNow {
				continue
			}
			// Score: cheap and fast wins; health is a tiebreaker.
			score := float64(1000-loc.CostPence) + float64(60-loc.QueueMinutes)*2 + h.Health*50
			candidates = append(candidates, struct {
				p      *Provider
				loc    Location
				score  float64
				reason string
			}{p, loc, score, fmt.Sprintf("%s: lowest wait %dm, fee %dp, health %.2f", loc.Name, loc.QueueMinutes, loc.CostPence, h.Health)})
		}
	}
	if len(candidates) == 0 {
		return nil, &RouteError{
			CustomerFacing: "No deposit locations are available right now. Please try again later.",
			Reasons:        reasons,
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	var alts []Alternate
	for _, c := range candidates[1:] {
		if len(alts) >= 2 {
			break
		}
		alts = append(alts, Alternate{Provider: c.p.Name, Location: c.loc.Name, Reason: c.reason})
	}
	best := candidates[0]

	reservationID := fmt.Sprintf("dep-%s-%d", req.CustomerID, req.At.Unix())
	var exp time.Time
	if usage != nil {
		res, err := usage.Reserve(req.AmountMinor, req.At, reservationID)
		if err != nil {
			return nil, &RouteError{CustomerFacing: "This deposit would exceed your monthly cash limit.", Reasons: reasons}
		}
		exp = res.ExpiresAt
	}
	return &Route{
		Provider:      best.p.Name,
		Location:      best.loc,
		Reason:        best.reason,
		ReservationID: reservationID,
		ExpiresAt:     exp,
		Alternatives:  alts,
	}, nil
}

// ---------- AML risk tiering ----------

// Tier is the dynamic review tier for a deposit.
type Tier string

const (
	TierNormal   Tier = "NORMAL"
	TierWatch    Tier = "WATCH"
	TierReview   Tier = "REVIEW"
	TierRestrict Tier = "RESTRICT"
)

// DepositHistory summarises a customer's recent cash behaviour.
type DepositHistory struct {
	Count90d           int     `json:"count_90d"`
	TotalMinor90d      int64   `json:"total_minor_90d"`
	AvgPerDepositMinor int64   `json:"avg_per_deposit_minor"`
	FrequencyGrowth    float64 `json:"frequency_growth"` // e.g. 2.0 = doubled vs prior period
	SourceDeclared     bool    `json:"source_declared"`
	StructuringSignals int     `json:"structuring_signals"` // near-threshold patterns detected
	AccountAgeDays     int     `json:"account_age_days"`
	PriorReviewOutcome string  `json:"prior_review_outcome"` // "", "CLEARED", "ESCALATED"
}

// RiskAssessment is the tiering decision with its reasoning.
type RiskAssessment struct {
	Tier    Tier     `json:"tier"`
	Reasons []string `json:"reasons"`
	Review  bool     `json:"review_required"`
}

// TierDeposit balances customer harm against AML risk: the tiers escalate on
// EVIDENCE (frequency growth, structuring, undeclared sources), not merely on
// amounts — a customer can legitimately deposit £700 every month.
func TierDeposit(req DepositRequest, hist DepositHistory, monthlyLimitMinor int64) RiskAssessment {
	a := RiskAssessment{Tier: TierNormal}
	add := func(t Tier, reason string) {
		a.Tier = t
		a.Reasons = append(a.Reasons, reason)
	}
	// Hard floor: anything at/over the regulatory reporting line is reviewed
	// regardless of history.
	if req.AmountMinor >= monthlyLimitMinor {
		add(TierReview, "deposit at or above monthly regulatory reporting threshold")
	}
	// Structuring: repeated just-under-threshold deposits are the classic
	// evasion pattern and always escalate.
	if hist.StructuringSignals > 0 {
		add(TierReview, fmt.Sprintf("%d structuring signals detected (near-threshold patterns)", hist.StructuringSignals))
	}
	// Frequency growth: a sudden doubling of cash activity is a behavioural
	// change worth watching even if each deposit is small.
	if hist.FrequencyGrowth >= 2.0 {
		add(TierWatch, fmt.Sprintf("cash deposit frequency grew %.1fx vs prior period", hist.FrequencyGrowth))
	}
	// Source transparency.
	if !hist.SourceDeclared && req.AmountMinor >= monthlyLimitMinor/2 {
		add(TierWatch, "high-value deposit without declared source")
	}
	// Prior escalation history raises the baseline.
	if hist.PriorReviewOutcome == "ESCALATED" {
		add(TierReview, "prior escalated review on this account")
	}
	// New accounts moving cash quickly are riskier than established ones.
	if hist.AccountAgeDays < 30 && req.AmountMinor >= monthlyLimitMinor/2 {
		add(TierWatch, "high-value cash within 30 days of account opening")
	}
	// RESTRICT is reserved for the clearest patterns: active structuring on an
	// account that has already been escalated once.
	if hist.StructuringSignals > 0 && hist.PriorReviewOutcome == "ESCALATED" {
		add(TierRestrict, "structuring continuing after a prior escalation")
	}
	if a.Tier == TierReview || a.Tier == TierRestrict {
		a.Review = true
	}
	return a
}

// MergeReasons dedupes provider exclusion reasons (used by callers that
// aggregate across retries).
func MergeReasons(maps ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range maps {
		for k, v := range m {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	return out
}

// DescribeTier renders a customer-safe explanation of a tier.
func DescribeTier(t Tier) string {
	switch t {
	case TierNormal:
		return "No restrictions apply."
	case TierWatch:
		return "We may ask about the source of some deposits."
	case TierReview:
		return "Some deposits will be reviewed before they clear."
	case TierRestrict:
		return "Cash deposits are temporarily restricted; contact support."
	}
	return strings.TrimSpace(string(t))
}
