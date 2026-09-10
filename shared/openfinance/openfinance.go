// Package openfinance implements Nexora's Open-Finance platform engines.
//
//  16. Linking health: every connected external account gets a composite
//     health score (authentication, freshness, availability, data quality).
//     A silently degrading link breaks budgeting and categorisation
//     downstream — the score makes degradation visible before it hurts.
//  17. Semantic normalizer: HSBC says "CARD PAYMENT", another provider says
//     "DEBIT CARD PURCHASE", a third "POS PURCHASE" — all become one
//     canonical CARD_PAYMENT. One schema across N providers.
//  18. Confidence scoring: enriched external transactions carry per-field
//     confidence so downstream decisions (budgeting, fraud, reporting) can
//     weigh evidence rather than trust it blindly.
package openfinance

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// ── 16. Linking health score ────────────────────────────────────────────────

// LinkComponent is one measured dimension of a connected account.
type LinkComponent struct {
	// Score 0..100.
	Score float64 `json:"score"`
	// Weight multiplies into the composite; weights are normalised.
	Weight float64 `json:"weight"`
	// Detail explains the score for support/debugging.
	Detail string `json:"detail,omitempty"`
}

// LinkHealthInputs are the four measured dimensions.
type LinkHealthInputs struct {
	Authentication LinkComponent // did the last re-auth succeed?
	Freshness      LinkComponent // how stale is the last successful sync?
	Availability   LinkComponent // provider API success rate over the window
	DataQuality    LinkComponent // schema/field completeness of returned data
}

// LinkHealth is the composite verdict.
type LinkHealth struct {
	Score   float64       `json:"score"` // 0..100
	State   string        `json:"state"` // HEALTHY / DEGRADED / CRITICAL
	Weakest LinkComponent `json:"weakest"`
}

// LinkHealthScore computes the weighted composite. Weights are normalised so
// a mis-configured weight can't push the score out of range. The weakest
// component is reported because that is what the re-link UX must fix.
func LinkHealthScore(in LinkHealthInputs) (*LinkHealth, error) {
	comps := []struct {
		name string
		c    LinkComponent
	}{
		{"authentication", in.Authentication},
		{"freshness", in.Freshness},
		{"availability", in.Availability},
		{"data_quality", in.DataQuality},
	}
	totalW := 0.0
	for _, c := range comps {
		if c.c.Score < 0 || c.c.Score > 100 {
			return nil, fmt.Errorf("%s score %v out of range 0..100", c.name, c.c.Score)
		}
		if c.c.Weight < 0 {
			return nil, fmt.Errorf("%s weight %v negative", c.name, c.c.Weight)
		}
		totalW += c.c.Weight
	}
	if totalW == 0 {
		return nil, fmt.Errorf("component weights sum to zero")
	}
	score := 0.0
	weakest := comps[0]
	for _, c := range comps {
		score += c.c.Score * (c.c.Weight / totalW)
		if c.c.Score < weakest.c.Score {
			weakest = c
		}
	}
	state := "HEALTHY"
	switch {
	case score < 50:
		state = "CRITICAL"
	case score < 80:
		state = "DEGRADED"
	}
	// Floor rule: a composite must never mask a collapsed component. A link
	// with 99% availability but 10% freshness is NOT healthy regardless of
	// the weighted mean — customers notice stale data.
	if floorState := weakestFloor(weakest.c.Score); floorState != "HEALTHY" {
		if state == "HEALTHY" || (state == "DEGRADED" && floorState == "CRITICAL") {
			state = floorState
		}
	}
	return &LinkHealth{Score: round1(score), State: state, Weakest: weakest.c}, nil
}

// FreshnessScore converts a sync age into a 0..100 freshness component.
// Fresh enough → 100; linearly decaying to 0 at MaxStale. Weight defaults to
// 2 so a caller forgetting to set it still counts freshness in the composite
// (a silently unweighted component is exactly the bug this score exists to
// surface elsewhere).
func FreshnessScore(lastSync time.Time, now time.Time, maxStale time.Duration) LinkComponent {
	c := LinkComponent{Weight: 2}
	age := now.Sub(lastSync)
	if age <= 0 {
		c.Score, c.Detail = 100, "synced just now"
		return c
	}
	if age >= maxStale {
		c.Score = 0
		c.Detail = fmt.Sprintf("last sync %v ago exceeds max %v", age.Round(time.Minute), maxStale)
		return c
	}
	frac := 1 - float64(age)/float64(maxStale)
	c.Score, c.Detail = round1(frac*100), fmt.Sprintf("last sync %v ago", age.Round(time.Minute))
	return c
}

// ── 17. Semantic normalizer ─────────────────────────────────────────────────

// CanonicalTransactionType is the one internal schema.
type CanonicalTransactionType string

const (
	CanonCardPayment   CanonicalTransactionType = "CARD_PAYMENT"
	CanonDirectDebit   CanonicalTransactionType = "DIRECT_DEBIT"
	CanonFasterPayment CanonicalTransactionType = "FASTER_PAYMENT"
	CanonStandingOrder CanonicalTransactionType = "STANDING_ORDER"
	CanonTransfer      CanonicalTransactionType = "INTERNAL_TRANSFER"
	CanonCash          CanonicalTransactionType = "CASH"
	CanonInterest      CanonicalTransactionType = "INTEREST"
	CanonFee           CanonicalTransactionType = "FEE"
	CanonRefund        CanonicalTransactionType = "REFUND"
	CanonUnknown       CanonicalTransactionType = "UNKNOWN"
)

// Normalizer maps provider-specific transaction descriptors onto the
// canonical schema. Mappings are data (provider → raw → canonical) so adding
// a bank is a config change, not a code change. Unmapped descriptors fall
// through rule-based heuristics, then to UNKNOWN — never a wrong guess.
type Normalizer struct {
	// mappings[provider][rawUpper] = canonical
	mappings map[string]map[string]CanonicalTransactionType
}

func NewNormalizer() *Normalizer {
	return &Normalizer{mappings: map[string]map[string]CanonicalTransactionType{}}
}

// AddMapping registers one provider descriptor mapping.
func (n *Normalizer) AddMapping(provider, raw string, canon CanonicalTransactionType) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if n.mappings[provider] == nil {
		n.mappings[provider] = map[string]CanonicalTransactionType{}
	}
	n.mappings[provider][raw] = canon
}

// Normalize resolves a provider descriptor. Order: exact provider mapping →
// keyword heuristics → UNKNOWN.
func (n *Normalizer) Normalize(provider, rawDescriptor string) CanonicalTransactionType {
	key := strings.ToUpper(strings.TrimSpace(rawDescriptor))
	if m, ok := n.mappings[provider][key]; ok {
		return m
	}
	return heuristic(key)
}

// heuristic uses keyword precedence: the most specific first. "REFUND"
// before "CARD" because "CARD REFUND" is a refund, not a payment.
func heuristic(d string) CanonicalTransactionType {
	switch {
	case contains(d, "REFUND", "REVERSAL"):
		return CanonRefund
	case contains(d, "DIRECT DEBIT", "DD "):
		return CanonDirectDebit
	case contains(d, "STANDING ORDER", "SO "):
		return CanonStandingOrder
	case contains(d, "INTEREST"):
		return CanonInterest
	case contains(d, "FEE", "CHARGE"):
		return CanonFee
	case contains(d, "ATM", "CASH WITHDRAWAL", "CASH DEPOSIT"):
		return CanonCash
	case contains(d, "FASTER PAYMENT", "FPS", "BANK TRANSFER"):
		return CanonFasterPayment
	case contains(d, "TRANSFER"):
		return CanonTransfer
	case contains(d, "CARD", "POS", "POINT OF SALE", "MASTERCARD", "VISA"):
		return CanonCardPayment
	default:
		return CanonUnknown
	}
}

func contains(d string, keys ...string) bool {
	for _, k := range keys {
		if strings.Contains(d, k) {
			return true
		}
	}
	return false
}

// ── 18. Transaction confidence scoring ──────────────────────────────────────

// Confidence is per-field evidence quality for one enriched external
// transaction. 0..1 per field; the composite is the weighted mean, but
// callers are expected to gate on individual fields (e.g. never auto-match
// an invoice on merchant confidence < 0.9 regardless of the composite).
type Confidence struct {
	Merchant float64 `json:"merchant_confidence"`
	Category float64 `json:"category_confidence"`
	Location float64 `json:"location_confidence"`
	Amount   float64 `json:"amount_confidence"`
}

// ConfidenceVerdict adds the composite and per-field flags.
type ConfidenceVerdict struct {
	Composite float64            `json:"composite"`
	Fields    map[string]float64 `json:"fields"`
	// LowFields lists fields below the gate, sorted ascending — the first
	// thing an analyst sees.
	LowFields []string `json:"low_fields,omitempty"`
}

// ScoreConfidence computes the composite (equal weights across the four
// fields) and flags fields under the gate.
func ScoreConfidence(c Confidence, gate float64) (*ConfidenceVerdict, error) {
	fields := map[string]float64{
		"merchant": c.Merchant, "category": c.Category,
		"location": c.Location, "amount": c.Amount,
	}
	composite := 0.0
	var low []string
	for name, v := range fields {
		if v < 0 || v > 1 {
			return nil, fmt.Errorf("field %s confidence %v out of range 0..1", name, v)
		}
		composite += v
		if v < gate {
			low = append(low, name)
		}
	}
	sort.Strings(low)
	return &ConfidenceVerdict{
		Composite: round1(composite / 4),
		Fields:    fields,
		LowFields: low,
	}, nil
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// weakestFloor caps the state by the single worst component.
func weakestFloor(s float64) string {
	switch {
	case s < 35:
		return "CRITICAL"
	case s < 60:
		return "DEGRADED"
	default:
		return "HEALTHY"
	}
}
