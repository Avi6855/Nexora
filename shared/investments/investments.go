// Package investments implements Nexora's investment-platform engines:
//
//  14. ISA allowance control tower: one envelope per customer per tax year
//     spanning Cash and Stocks & Shares ISAs. HMRC's rule is the binding
//     constraint: the ANNUAL ALLOWANCE is shared across all ISA types, so
//     every contribution (and correction!) across every wrapper must be
//     admitted by one gateway. Duplicate submissions and backdated
//     corrections are the failure modes this guards.
//
//  15. Joint ISA goal: the UI is joint, the wrappers are NOT — two legal
//     owners, each with their own allowance, contributing to one shared
//     household goal. The engine splits contributions and enforces per-owner
//     allowance.
//
//  16. Tax-lot engine: sell orders consume BUY lots under a versioned
//     selection method (FIFO, LIFO, HIFO) with exact per-lot gain/loss.
//
//  17. Corporate-action processor: splits, mergers, tickers changes and
//     dividends rewrite holdings deterministically, with full audit of what
//     moved.
package investments

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrAllowanceExceeded = errors.New("contribution exceeds remaining ISA allowance")
	ErrUnknownHolding    = errors.New("holding not found")
	ErrInsufficientUnits = errors.New("insufficient units to sell")
	ErrUnknownAction     = errors.New("unknown corporate action type")
	ErrGoalOverfunded    = errors.New("joint goal fully funded")
)

// ── 14. ISA allowance control tower ─────────────────────────────────────────

// ISAWrapper is the legal wrapper type.
type ISAWrapper string

const (
	WrapperCash ISAWrapper = "CASH_ISA"
	WrapperSNS  ISAWrapper = "STOCKS_AND_SHARES_ISA"
)

// Contribution is one money-in event to any wrapper.
type Contribution struct {
	ID          string     `json:"id"`
	OwnerID     string     `json:"owner_id"`
	Wrapper     ISAWrapper `json:"wrapper"`
	AmountMinor int64      `json:"amount_minor"`
	At          time.Time  `json:"at"`
	// CorrectedFrom marks a correction replacing a prior contribution ID
	// (a mistaken £8,000 corrected to £800 still must re-admit correctly).
	CorrectedFrom string `json:"corrected_from,omitempty"`
}

// ControlTower enforces the annual allowance across all wrappers per owner
// and tax year.
type ControlTower struct {
	AnnualAllowanceMinor int64
	// used[(owner, taxYear)] → admitted contributions by ID.
	used map[usageKey]int64
	ids  map[string]bool // global contribution idempotency
	// amounts[contributionID] → admitted net amount + usage key, so a
	// correction releases exactly what the original consumed.
	amounts map[string]amountRec
}

type usageKey struct {
	owner   string
	taxYear string
}

// NewControlTower builds a tower for a tax year's allowance.
func NewControlTower(annualAllowanceMinor int64) *ControlTower {
	return &ControlTower{
		AnnualAllowanceMinor: annualAllowanceMinor,
		used:                 map[usageKey]int64{},
		ids:                  map[string]bool{},
		amounts:              map[string]amountRec{},
	}
}

// TaxYear returns the UK tax year label containing t (6 April boundary).
func TaxYear(t time.Time) string {
	y := t.Year()
	if t.Month() < time.April || (t.Month() == time.April && t.Day() < 6) {
		return fmt.Sprintf("%d/%d", y-1, y%100)
	}
	return fmt.Sprintf("%d/%d", y, (y+1)%100)
}

// Remaining reports unused allowance for owner+tax year.
func (c *ControlTower) Remaining(owner string, taxYear string) int64 {
	return c.AnnualAllowanceMinor - c.used[usageKey{owner, taxYear}]
}

// Admit accepts a contribution against the allowance. It is idempotent by
// contribution ID (a redelivered event never double-counts — HMRC reports
// would be wrong forever) and supports corrections: a CorrectedFrom
// contribution releases the replaced one's amount first.
func (c *ControlTower) Admit(con Contribution) error {
	if con.AmountMinor < 0 {
		return fmt.Errorf("contribution must be non-negative, got %d", con.AmountMinor)
	}
	if c.ids[con.ID] {
		return nil // duplicate delivery: already admitted
	}
	key := usageKey{con.OwnerID, TaxYear(con.At)}
	if con.CorrectedFrom != "" {
		if !c.ids[con.CorrectedFrom] {
			return fmt.Errorf("correction references unknown contribution %s", con.CorrectedFrom)
		}
	}
	// Compute the net effect including any released correction.
	net := con.AmountMinor
	if con.CorrectedFrom != "" {
		net -= c.amountOf(con.OwnerID, key.taxYear, con.CorrectedFrom)
	}
	if net > c.Remaining(con.OwnerID, key.taxYear) {
		return fmt.Errorf("%w: need %d, remaining %d", ErrAllowanceExceeded, net, c.Remaining(con.OwnerID, key.taxYear))
	}
	// Apply: release the replaced contribution from its ORIGINAL usage key,
	// then admit the new amount in full. The allowance CHECK used the net
	// figure (new minus released — equivalent once the release is added back),
	// but applying `net` after an explicit release double-counts it and
	// refunds money the customer never had.
	c.ids[con.ID] = true
	if con.CorrectedFrom != "" {
		c.removeContribution(con.OwnerID, key.taxYear, con.CorrectedFrom)
	}
	c.used[key] += con.AmountMinor
	c.recordAmount(con.OwnerID, key.taxYear, con.ID, con.AmountMinor)
	return nil
}

// per-contribution amounts let corrections release exactly the original.
// This state lives ON the tower, not in a package global: a global is shared
// across tower instances (and tests), so a second tower would "correct"
// contributions it never admitted — the package-level map made remaining
// allowance differ between otherwise-identical towers.
type amountRec struct {
	key    usageKey
	amount int64
}

func (c *ControlTower) recordAmount(owner, taxYear, id string, amount int64) {
	c.amounts[id] = amountRec{usageKey{owner, taxYear}, amount}
}

func (c *ControlTower) amountOf(owner, taxYear, id string) int64 {
	if rec, ok := c.amounts[id]; ok {
		return rec.amount
	}
	return 0
}

func (c *ControlTower) removeContribution(owner, taxYear, id string) {
	if rec, ok := c.amounts[id]; ok {
		c.used[rec.key] -= rec.amount
		delete(c.amounts, id)
	}
}

// ── 15. Joint ISA goal ──────────────────────────────────────────────────────

// JointGoal is a household savings goal funded by two individual wrappers.
type JointGoal struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TargetMinor int64    `json:"target_minor"`
	Owners      []string `json:"owners"` // exactly two legal owners
	FundedMinor int64    `json:"funded_minor"`
}

// NewJointGoal validates the two-owner model — a "joint ISA" never merges
// the legal wrappers, it only shares the goal.
func NewJointGoal(id, name string, targetMinor int64, owners []string) (*JointGoal, error) {
	if len(owners) != 2 || owners[0] == owners[1] {
		return nil, errors.New("joint goal requires exactly two distinct legal owners")
	}
	if targetMinor <= 0 {
		return nil, errors.New("target must be positive")
	}
	return &JointGoal{ID: id, Name: name, TargetMinor: targetMinor, Owners: owners}, nil
}

// Contribute adds one owner's money to the goal from THEIR wrapper, enforcing
// both the goal target and (via the caller) the per-owner allowance.
func (g *JointGoal) Contribute(ownerID string, amountMinor int64) error {
	if !containsOwner(g.Owners, ownerID) {
		return fmt.Errorf("%s is not an owner of goal %s", ownerID, g.ID)
	}
	if g.FundedMinor+amountMinor > g.TargetMinor {
		return fmt.Errorf("%w: funded %d, target %d", ErrGoalOverfunded, g.FundedMinor, g.TargetMinor)
	}
	g.FundedMinor += amountMinor
	return nil
}

// BalanceView is the per-owner share for the joint UI.
func (g *JointGoal) BalanceView() map[string]float64 {
	// Equal legal ownership of the GOAL; wrappers stay individual.
	share := 0.5
	if g.TargetMinor == 0 {
		return map[string]float64{g.Owners[0]: 0, g.Owners[1]: 0}
	}
	return map[string]float64{g.Owners[0]: share, g.Owners[1]: share}
}

func containsOwner(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ── 16. Portfolio tax-lot engine ────────────────────────────────────────────

// LotMethod is the versioned lot-selection method.
type LotMethod string

const (
	LotFIFO LotMethod = "FIFO" // oldest first (default HMRC behaviour)
	LotLIFO LotMethod = "LIFO" // newest first
	LotHIFO LotMethod = "HIFO" // highest cost basis first (minimises gain)
)

// Lot is one BUY.
type Lot struct {
	ID             string    `json:"id"`
	Symbol         string    `json:"symbol"`
	UnitsMilli     int64     `json:"units_milli"`      // units × 1000 (fractional units)
	CostBasisMinor int64     `json:"cost_basis_minor"` // total cost of the lot
	BoughtAt       time.Time `json:"bought_at"`
}

// Disposal records one consumed slice of a lot.
type Disposal struct {
	LotID         string `json:"lot_id"`
	UnitsMilli    int64  `json:"units_milli"`
	CostMinor     int64  `json:"cost_minor"` // basis of the consumed slice
	ProceedsMinor int64  `json:"proceeds_minor"`
	GainMinor     int64  `json:"gain_minor"`
}

// SellResult is the outcome of one sell order.
type SellResult struct {
	Disposals []Disposal `json:"disposals"`
	// TotalGainMinor and TotalProceedsMinor summarise the order.
	TotalGainMinor     int64 `json:"total_gain_minor"`
	TotalProceedsMinor int64 `json:"total_proceeds_minor"`
}

// TaxLotEngine selects lots under a versioned method.
type TaxLotEngine struct {
	method LotMethod
	lots   []Lot
}

// NewTaxLotEngine builds an engine for one holding with a method.
func NewTaxLotEngine(method LotMethod, lots []Lot) *TaxLotEngine {
	return &TaxLotEngine{method: method, lots: append([]Lot(nil), lots...)}
}

// remainingUnits of a lot after prior disposals is derived by the engine
// (real implementation tracks per-lot remaining; here lots are pre-sale).
func (e *TaxLotEngine) orderedLots() []Lot {
	sorted := append([]Lot(nil), e.lots...)
	switch e.method {
	case LotFIFO:
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].BoughtAt.Before(sorted[j].BoughtAt) })
	case LotLIFO:
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].BoughtAt.After(sorted[j].BoughtAt) })
	case LotHIFO:
		sort.SliceStable(sorted, func(i, j int) bool {
			pi := perUnitBasis(sorted[i])
			pj := perUnitBasis(sorted[j])
			return pi > pj
		})
	}
	return sorted
}

func perUnitBasis(l Lot) float64 {
	if l.UnitsMilli == 0 {
		return 0
	}
	return float64(l.CostBasisMinor) / float64(l.UnitsMilli)
}

// Sell consumes lots in method order until unitsMilli is filled. Partial-lot
// consumption is exact: cost basis scales with the fraction sold.
func (e *TaxLotEngine) Sell(symbol string, unitsMilli int64, pricePerUnitMilli int64) (*SellResult, error) {
	if unitsMilli <= 0 {
		return nil, errors.New("sell units must be positive")
	}
	owned := int64(0)
	for _, l := range e.lots {
		if l.Symbol == symbol {
			owned += l.UnitsMilli
		}
	}
	if owned < unitsMilli {
		return nil, fmt.Errorf("%w: have %d, want %d", ErrInsufficientUnits, owned, unitsMilli)
	}
	res := &SellResult{}
	remaining := unitsMilli
	for _, l := range e.orderedLots() {
		if l.Symbol != symbol || remaining <= 0 {
			continue
		}
		take := remaining
		if take > l.UnitsMilli {
			take = l.UnitsMilli
		}
		cost := int64(perUnitBasis(l) * float64(take))
		proceeds := int64(float64(pricePerUnitMilli) / 1000 * float64(take))
		d := Disposal{
			LotID: l.ID, UnitsMilli: take, CostMinor: cost, ProceedsMinor: proceeds,
			GainMinor: proceeds - cost,
		}
		res.Disposals = append(res.Disposals, d)
		res.TotalGainMinor += d.GainMinor
		res.TotalProceedsMinor += d.ProceedsMinor
		remaining -= take
	}
	return res, nil
}

// ── 17. Corporate-action processor ──────────────────────────────────────────

// ActionType enumerates corporate actions.
type ActionType string

const (
	ActionSplit    ActionType = "SPLIT"         // 2:1 doubles units, halves basis/unit
	ActionMerger   ActionType = "MERGER"        // old symbol → new symbol (ratio)
	ActionTicker   ActionType = "TICKER_CHANGE" // rename only
	ActionDividend ActionType = "DIVIDEND"      // cash per unit
)

// CorporateAction is one announced event.
type CorporateAction struct {
	Type         ActionType `json:"type"`
	Symbol       string     `json:"symbol"` // affected security
	NewSymbol    string     `json:"new_symbol,omitempty"`
	RatioNum     int64      `json:"ratio_num,omitempty"` // SPLIT: new/old; MERGER: exchange ratio
	RatioDen     int64      `json:"ratio_den,omitempty"`
	PerUnitMinor int64      `json:"per_unit_minor,omitempty"` // DIVIDEND
	At           time.Time  `json:"at"`
}

// HoldingActionResult records what changed for one customer holding.
type HoldingActionResult struct {
	Symbol      string `json:"symbol"`
	UnitsBefore int64  `json:"units_before_milli"`
	UnitsAfter  int64  `json:"units_after_milli"`
	BasisAfter  int64  `json:"basis_after_minor"`
	CashMinor   int64  `json:"cash_minor,omitempty"`
	Note        string `json:"note"`
}

// Holding is a customer's position in one security.
type Holding struct {
	Symbol         string `json:"symbol"`
	UnitsMilli     int64  `json:"units_milli"`
	CostBasisMinor int64  `json:"cost_basis_minor"`
}

// ApplyCorporateAction rewrites a holding for one action, preserving
// economic value for non-cash actions: a split changes NOTHING except unit
// count and per-unit basis.
func ApplyCorporateAction(h Holding, a CorporateAction) (*Holding, *HoldingActionResult, error) {
	if h.UnitsMilli <= 0 {
		return nil, nil, ErrUnknownHolding
	}
	switch a.Type {
	case ActionSplit:
		if a.RatioNum <= 0 || a.RatioDen <= 0 {
			return nil, nil, fmt.Errorf("%w: split ratio %d/%d invalid", ErrUnknownAction, a.RatioNum, a.RatioDen)
		}
		// Units scale by num/den; total basis is unchanged.
		newUnits := h.UnitsMilli * a.RatioNum / a.RatioDen
		out := Holding{Symbol: h.Symbol, UnitsMilli: newUnits, CostBasisMinor: h.CostBasisMinor}
		ar := &HoldingActionResult{
			Symbol: h.Symbol, UnitsBefore: h.UnitsMilli, UnitsAfter: newUnits,
			BasisAfter: h.CostBasisMinor,
			Note:       fmt.Sprintf("split %d:%d — %d→%d units, total basis unchanged", a.RatioNum, a.RatioDen, h.UnitsMilli, newUnits),
		}
		return &out, ar, nil
	case ActionMerger:
		if a.NewSymbol == "" || a.RatioNum <= 0 || a.RatioDen <= 0 {
			return nil, nil, fmt.Errorf("%w: merger requires new symbol and ratio", ErrUnknownAction)
		}
		newUnits := h.UnitsMilli * a.RatioNum / a.RatioDen
		out := Holding{Symbol: a.NewSymbol, UnitsMilli: newUnits, CostBasisMinor: h.CostBasisMinor}
		ar := &HoldingActionResult{
			Symbol: a.NewSymbol, UnitsBefore: h.UnitsMilli, UnitsAfter: newUnits,
			BasisAfter: h.CostBasisMinor,
			Note:       fmt.Sprintf("merged %s→%s at %d:%d — basis carried over", h.Symbol, a.NewSymbol, a.RatioNum, a.RatioDen),
		}
		return &out, ar, nil
	case ActionTicker:
		out := Holding{Symbol: a.NewSymbol, UnitsMilli: h.UnitsMilli, CostBasisMinor: h.CostBasisMinor}
		ar := &HoldingActionResult{
			Symbol: a.NewSymbol, UnitsBefore: h.UnitsMilli, UnitsAfter: h.UnitsMilli,
			BasisAfter: h.CostBasisMinor,
			Note:       "ticker change only — economics identical",
		}
		return &out, ar, nil
	case ActionDividend:
		if a.PerUnitMinor <= 0 {
			return nil, nil, fmt.Errorf("%w: dividend per-unit missing", ErrUnknownAction)
		}
		cash := h.UnitsMilli * a.PerUnitMinor / 1000
		// Units and basis unchanged; cash lands separately.
		out := h
		ar := &HoldingActionResult{
			Symbol: h.Symbol, UnitsBefore: h.UnitsMilli, UnitsAfter: h.UnitsMilli,
			BasisAfter: h.CostBasisMinor, CashMinor: cash,
			Note: "dividend paid as cash; holding unchanged",
		}
		return &out, ar, nil
	default:
		return nil, nil, fmt.Errorf("%w: %s", ErrUnknownAction, a.Type)
	}
}

// ActionAudit renders the human audit line for a corporate action result.
func ActionAudit(ar *HoldingActionResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: units %d→%d, basis %d", ar.Symbol, ar.UnitsBefore, ar.UnitsAfter, ar.BasisAfter)
	if ar.CashMinor > 0 {
		fmt.Fprintf(&b, ", cash %d", ar.CashMinor)
	}
	b.WriteString(" — " + ar.Note)
	return b.String()
}
