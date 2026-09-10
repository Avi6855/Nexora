// Package cards implements Nexora's advanced card platform:
//
//  1. Merchant-locked virtual cards: a virtual card that pays ONLY a named
//     merchant — the classic defence against card-details leakage. The lock
//     resolves merchant identity through the merchant GROUP (Netflix billed
//     via "Netflix International B.V." must still pass), but a stolen-number
//     attempt at any other merchant declines.
//
//  2. Programmable authorization rules: user-authored rules (time windows,
//     countries, categories, daily caps, online-only, ATM blocks) evaluated
//     deterministically in priority order with an explicit default action.
//
//  3. Credential continuity: network-token mapping that survives card
//     replacement — the token stays, the underlying funding credential
//     rotates beneath it, and saved-merchant payments keep flowing.
//
//  4. Lifecycle orchestrator: REQUESTED→…→EXPIRED as a saga with per-stage
//     partner failure compensation.
//
//  5. Delivery exception recovery: courier failure reasons classified into
//     automated recovery workflows instead of support tickets.
package cards

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrMerchantLocked   = errors.New("card is locked to a different merchant")
	ErrRuleDeclined     = errors.New("declined by card authorization rules")
	ErrCardNotActive    = errors.New("card is not active for payments")
	ErrTokenUnknown     = errors.New("network token not found")
	ErrIllegalStage     = errors.New("illegal card lifecycle transition")
	ErrDeliveryUnknown  = errors.New("delivery exception reason unknown")
	ErrDailyCapExceeded = errors.New("daily spend cap exceeded")
)

// ── 1. Merchant-locked virtual cards ────────────────────────────────────────

// MerchantIdentity is the acquirer-presented merchant.
type MerchantIdentity struct {
	ID    string `json:"id"` // acquirer merchant id
	Name  string `json:"name"`
	Group string `json:"group"` // merchant group/parent (e.g. "NETFLIX")
}

// VirtualCard is a merchant-locked card.
type VirtualCard struct {
	ID           string `json:"id"`
	LinkedCardID string `json:"linked_card_id"` // funding physical card
	LockedGroup  string `json:"locked_group"`   // empty = not locked
	Active       bool   `json:"active"`
}

// LockedAuthorizer decides whether a virtual card can pay a merchant.
type LockedAuthorizer struct {
	// MerchantGroups maps merchant IDs/names to their canonical group —
	// production backs this with the merchant identity graph.
	MerchantGroups map[string]string
}

// Authorize evaluates a merchant-locked payment. Identity resolution first:
// the presented merchant resolves to its group via the identity graph, with
// a direct name match as fallback. Only then is the lock enforced.
func (a *LockedAuthorizer) Authorize(c VirtualCard, m MerchantIdentity, amountMinor int64) error {
	if !c.Active {
		return ErrCardNotActive
	}
	if c.LockedGroup == "" {
		return nil // unlocked virtual card pays anywhere
	}
	group := a.resolveGroup(m)
	if group != c.LockedGroup {
		return fmt.Errorf("%w: card locked to %s, presented %q (group %q)",
			ErrMerchantLocked, c.LockedGroup, m.Name, group)
	}
	return nil
}

func (a *LockedAuthorizer) resolveGroup(m MerchantIdentity) string {
	if g, ok := a.MerchantGroups[m.ID]; ok {
		return g
	}
	if m.Group != "" {
		return m.Group
	}
	// Fallback: exact name match, else the merchant's own name is its group.
	if g, ok := a.MerchantGroups[m.Name]; ok {
		return g
	}
	return strings.ToUpper(m.Name)
}

// ── 2. Programmable card authorization rules ────────────────────────────────

// AuthRule is one user-authored rule. Priority is evaluated ascending —
// lower number wins, first match decides.
type AuthRule struct {
	Priority      int      `json:"priority"`
	Action        Action   `json:"action"`              // ALLOW or BLOCK
	TimeFrom      string   `json:"time_from,omitempty"` // "09:00" local
	TimeTo        string   `json:"time_to,omitempty"`
	Countries     []string `json:"countries,omitempty"` // empty = any
	Categories    []string `json:"categories,omitempty"`
	BlockGambling bool     `json:"block_gambling,omitempty"`
	OnlineOnly    bool     `json:"online_only,omitempty"`
	BlockATM      bool     `json:"block_atm,omitempty"`
	DailyCapMinor int64    `json:"daily_cap_minor,omitempty"`
}

// Action is the rule outcome.
type Action string

const (
	ActionAllow AuthAction = "ALLOW"
	ActionBlock AuthAction = "BLOCK"
)

// AuthAction aliases Action for readability in rule contexts.
type AuthAction = Action

// AuthAttempt is one card authorization request.
type AuthAttempt struct {
	CardID      string    `json:"card_id"`
	At          time.Time `json:"at"` // local time of the CARDHOLDER
	Country     string    `json:"country"`
	Category    string    `json:"category"` // MCC category, e.g. GAMBLING, TRANSPORT
	Online      bool      `json:"online"`
	ATM         bool      `json:"atm"`
	AmountMinor int64     `json:"amount_minor"`
}

// RuleEngine evaluates rules with deterministic ordering and daily counters.
type RuleEngine struct {
	rules map[string][]AuthRule       // cardID → ordered rules
	spent map[string]map[string]int64 // cardID → "2026-09-08" → spent minor
	// defaultAction applies when NO rule matches — explicit, never implicit.
	DefaultAction AuthAction
}

// NewRuleEngine builds an engine; default is deny-unless-allowed is FALSE
// (default ALLOW) because a rules engine that silently blocks everything is
// a support incident.
func NewRuleEngine() *RuleEngine {
	return &RuleEngine{
		rules:         map[string][]AuthRule{},
		spent:         map[string]map[string]int64{},
		DefaultAction: ActionAllow,
	}
}

// SetRules replaces the rule list for a card, normalising priority order.
func (e *RuleEngine) SetRules(cardID string, rules []AuthRule) {
	sorted := append([]AuthRule(nil), rules...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	e.rules[cardID] = sorted
}

// Evaluate runs the rule chain and returns the decision with the reason.
func (e *RuleEngine) Evaluate(cardID string, att AuthAttempt) (AuthAction, string, error) {
	for _, r := range e.rules[cardID] {
		if !ruleMatches(r, att) {
			continue
		}
		if r.Action == ActionBlock {
			return ActionBlock, blockReason(r), nil
		}
		// ALLOW rules still enforce their own daily cap: an allow rule with a
		// cap is "allow up to this much today".
		if r.DailyCapMinor > 0 {
			day := att.At.Format(time.DateOnly)
			spent := e.spent[cardID][day]
			if spent+att.AmountMinor > r.DailyCapMinor {
				return ActionBlock, fmt.Sprintf("daily cap %d exceeded (already spent %d)", r.DailyCapMinor, spent), nil
			}
			e.recordSpend(cardID, day, att.AmountMinor)
		}
		return ActionAllow, ruleDescription(r), nil
	}
	if e.DefaultAction == ActionBlock {
		return ActionBlock, "no rule matched; default action is BLOCK", nil
	}
	return ActionAllow, "no rule matched; default allow", nil
}

func (e *RuleEngine) recordSpend(cardID, day string, amount int64) {
	if e.spent[cardID] == nil {
		e.spent[cardID] = map[string]int64{}
	}
	e.spent[cardID][day] += amount
}

// ruleMatches: a rule applies when ALL of its SPECIFIED constraints hold.
// A constraint that is specified but not satisfied fails the rule — the old
// fall-through-return-true let an unfiltered BLOCK rule (e.g. "block
// gambling", category ≠ gambling) match every attempt and block the card.
func ruleMatches(r AuthRule, a AuthAttempt) bool {
	if r.TimeFrom != "" && r.TimeTo != "" {
		hm := a.At.Format("15:04")
		if hm < r.TimeFrom || hm > r.TimeTo {
			return false
		}
	}
	if r.BlockGambling && !strings.EqualFold(a.Category, "GAMBLING") {
		return false
	}
	if r.BlockATM && !a.ATM {
		return false
	}
	if len(r.Countries) > 0 && !containsFold(r.Countries, a.Country) {
		return false
	}
	if len(r.Categories) > 0 && !containsFold(r.Categories, a.Category) {
		return false
	}
	if r.OnlineOnly && !a.Online {
		return false
	}
	return true
}

func blockReason(r AuthRule) string {
	switch {
	case r.BlockGambling:
		return "gambling merchants are blocked on this card"
	case r.BlockATM:
		return "ATM withdrawals are blocked on this card"
	case len(r.Countries) > 0:
		return "merchant country not in the allowed list"
	case r.OnlineOnly:
		return "card is restricted to online payments"
	default:
		return "blocked by card rule"
	}
}

func ruleDescription(r AuthRule) string {
	var parts []string
	if r.TimeFrom != "" {
		parts = append(parts, "time window "+r.TimeFrom+"–"+r.TimeTo)
	}
	if len(r.Countries) > 0 {
		parts = append(parts, "countries "+strings.Join(r.Countries, "/"))
	}
	if len(r.Categories) > 0 {
		parts = append(parts, "categories "+strings.Join(r.Categories, "/"))
	}
	if r.OnlineOnly {
		parts = append(parts, "online only")
	}
	if r.DailyCapMinor > 0 {
		parts = append(parts, fmt.Sprintf("daily cap %d", r.DailyCapMinor))
	}
	return strings.Join(parts, ", ")
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// ── 3. Card credential continuity (network tokens) ──────────────────────────

// TokenState is the network token lifecycle.
type TokenState string

const (
	TokenActive    TokenState = "ACTIVE"
	TokenSuspended TokenState = "SUSPENDED"
	TokenRevoked   TokenState = "REVOKED"
)

// NetworkToken is an EMV Payment Token: merchants hold the token, not the
// PAN. When the physical card is replaced, the token maps to the NEW PAN and
// saved merchant payments continue without merchant action.
type NetworkToken struct {
	TokenID            string     `json:"token_id"`
	CardID             string     `json:"card_id"` // funding card it currently maps to
	Merchant           string     `json:"merchant"`
	State              TokenState `json:"state"`
	RequestorInitiated bool       `json:"requestor_initiated"`
	CreatedAt          time.Time  `json:"created_at"`
}

// TokenVault manages token lifecycle and card replacement continuity.
type TokenVault struct {
	tokens map[string]*NetworkToken
}

// NewTokenVault creates an empty vault.
func NewTokenVault() *TokenVault { return &TokenVault{tokens: map[string]*NetworkToken{}} }

// Register provisions a token for a merchant against a card.
func (v *TokenVault) Register(tokenID, cardID, merchant string, now time.Time) *NetworkToken {
	t := &NetworkToken{TokenID: tokenID, CardID: cardID, Merchant: merchant,
		State: TokenActive, RequestorInitiated: true, CreatedAt: now}
	v.tokens[tokenID] = t
	return t
}

// Get fetches a token.
func (v *TokenVault) Get(tokenID string) (*NetworkToken, error) {
	t, ok := v.tokens[tokenID]
	if !ok {
		return nil, ErrTokenUnknown
	}
	return t, nil
}

// ReplaceCard re-points EVERY active token from oldCard to newCard. This is
// credential continuity: merchants see the same token; the mapping beneath
// it rotates. Returns how many tokens were re-mapped.
func (v *TokenVault) ReplaceCard(oldCardID, newCardID string) int {
	n := 0
	for _, t := range v.tokens {
		if t.CardID == oldCardID && t.State == TokenActive {
			t.CardID = newCardID
			n++
		}
	}
	return n
}

// Suspend/Revoke gate token usage.
func (v *TokenVault) Suspend(tokenID string) error {
	t, err := v.Get(tokenID)
	if err != nil {
		return err
	}
	t.State = TokenSuspended
	return nil
}

func (v *TokenVault) Revoke(tokenID string) error {
	t, err := v.Get(tokenID)
	if err != nil {
		return err
	}
	t.State = TokenRevoked
	return nil
}

// ChargeThroughToken resolves a merchant charge: revoked/suspended tokens
// decline; active tokens resolve to their current funding card.
func (v *TokenVault) ChargeThroughToken(tokenID string) (string, error) {
	t, err := v.Get(tokenID)
	if err != nil {
		return "", err
	}
	switch t.State {
	case TokenActive:
		return t.CardID, nil
	case TokenSuspended:
		return "", fmt.Errorf("token %s suspended", tokenID)
	default:
		return "", fmt.Errorf("token %s revoked", tokenID)
	}
}

// ── 4. Intelligent card lifecycle orchestrator ──────────────────────────────

// Stage is the physical/digital card lifecycle.
type Stage string

const (
	StageRequested    Stage = "REQUESTED"
	StagePersonalised Stage = "PERSONALISED"
	StageManufactured Stage = "MANUFACTURED"
	StageShipped      Stage = "SHIPPED"
	StageDelivered    Stage = "DELIVERED"
	StageActivated    Stage = "ACTIVATED"
	StageSuspended    Stage = "SUSPENDED"
	StageReplaced     Stage = "REPLACED"
	StageExpired      Stage = "EXPIRED"
	StageFailed       Stage = "FAILED"
)

var forward = map[Stage][]Stage{
	StageRequested:    {StagePersonalised, StageFailed},
	StagePersonalised: {StageManufactured, StageFailed},
	StageManufactured: {StageShipped, StageFailed},
	StageShipped:      {StageDelivered, StageFailed},
	StageDelivered:    {StageActivated, StageFailed},
	// Advance order follows the stolen-card narrative (suspend → replace →
	// expire); explicit Transition() covers the event-driven moves.
	StageActivated: {StageSuspended, StageReplaced, StageExpired},
	StageSuspended: {StageReplaced, StageExpired, StageActivated},
	StageReplaced:  {StageExpired},
}

// StageStep is the customer-facing narration of one stage.
type StageStep struct {
	Stage    Stage     `json:"stage"`
	At       time.Time `json:"at"`
	Customer string    `json:"customer_message"`
	Detail   string    `json:"detail,omitempty"`
}

// CardSaga drives one card through its lifecycle with compensation.
type CardSaga struct {
	CardID     string      `json:"card_id"`
	Stage      Stage       `json:"stage"`
	History    []StageStep `json:"history"`
	FailStage  Stage       `json:"fail_stage,omitempty"`
	FailReason string      `json:"fail_reason,omitempty"`
}

// NewCardSaga starts a card at REQUESTED.
func NewCardSaga(cardID string, now time.Time) *CardSaga {
	s := &CardSaga{CardID: cardID, Stage: StageRequested}
	s.History = append(s.History, StageStep{Stage: StageRequested, At: now,
		Customer: "We're making your card."})
	return s
}

// Transition performs an event-driven move (e.g. reactivate a suspended
// card), validated against the allowed forward edges.
func (s *CardSaga) Transition(to Stage, now time.Time) error {
	for _, n := range forward[s.Stage] {
		if n == to && n != StageFailed {
			s.Stage = to
			s.History = append(s.History, StageStep{Stage: to, At: now, Customer: narration(to)})
			return nil
		}
	}
	return fmt.Errorf("%w: %s → %s", ErrIllegalStage, s.Stage, to)
}

// Advance moves the card one stage forward with its narration.
func (s *CardSaga) Advance(now time.Time) error {
	nexts := forward[s.Stage]
	for _, n := range nexts {
		if n == StageFailed {
			continue
		}
		s.Stage = n
		s.History = append(s.History, StageStep{Stage: n, At: now, Customer: narration(n)})
		return nil
	}
	return fmt.Errorf("%w: %s has no forward stage", ErrIllegalStage, s.Stage)
}

// PartnerFailure models an external-partner stage failure (personalisation
// bureau down, courier lost the item). The saga compensates: the card falls
// back to the last stable stage — or FAILED if the first stage broke — and
// records the reason for retry/reissue logic.
func (s *CardSaga) PartnerFailure(now time.Time, reason string) Stage {
	s.FailStage = s.Stage
	s.FailReason = reason
	// Compensation: first stage has nothing to fall back to.
	fallback := StageFailed
	for _, st := range []Stage{StageRequested, StagePersonalised, StageManufactured, StageShipped, StageDelivered} {
		if s.reached(st) && st != s.Stage {
			fallback = st
		}
	}
	s.Stage = fallback
	s.History = append(s.History, StageStep{Stage: fallback, At: now,
		Customer: "There's a small delay with your card — we're on it.",
		Detail:   reason})
	return fallback
}

func (s *CardSaga) reached(st Stage) bool {
	for _, h := range s.History {
		if h.Stage == st {
			return true
		}
	}
	return false
}

func narration(st Stage) string {
	switch st {
	case StagePersonalised:
		return "Your card has been personalised with your details."
	case StageManufactured:
		return "Your card has been manufactured and is with the courier."
	case StageShipped:
		return "Your card is on its way."
	case StageDelivered:
		return "Your card has been delivered — activate it in the app."
	case StageActivated:
		return "Your card is ready to use."
	case StageSuspended:
		return "Your card is temporarily frozen."
	case StageReplaced:
		return "A replacement card is on the way; the old one will stop working."
	case StageExpired:
		return "This card has expired."
	}
	return ""
}

// ── 5. Card delivery exception recovery ─────────────────────────────────────

// DeliveryReason is the courier failure classification.
type DeliveryReason string

const (
	DeliveryLost         DeliveryReason = "LOST"
	DeliveryDamaged      DeliveryReason = "DAMAGED"
	DeliveryWrongAddress DeliveryReason = "WRONG_ADDRESS"
	DeliveryReturned     DeliveryReason = "RETURNED"
	DeliveryDelayed      DeliveryReason = "DELAYED"
)

// RecoveryWorkflow is the automated plan for a delivery exception.
type RecoveryWorkflow struct {
	Reason         DeliveryReason `json:"reason"`
	CustomerAction string         `json:"customer_action"`
	Automated      []string       `json:"automated_steps"`
	NewCardNeeded  bool           `json:"new_card_needed"` // PAN reissue required
	AddressCheck   bool           `json:"address_check_required"`
	ReDispatch     bool           `json:"re_dispatch"`
}

// ClassifyDeliveryFailure maps a raw courier status to the recovery plan.
// LOST and DAMAGED must reissue (the PAN may be compromised/compromised-adjacent);
// WRONG_ADDRESS and RETURNED only need verification + re-dispatch of the SAME card.
func ClassifyDeliveryFailure(rawReason string) (*RecoveryWorkflow, error) {
	normalised := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(rawReason), " ", "_"))
	r := DeliveryReason(normalised)
	switch r {
	case DeliveryLost:
		return &RecoveryWorkflow{
			Reason:         r,
			CustomerAction: "We're sending you a new card — the old one can't be used.",
			Automated:      []string{"cancel existing card PAN", "issue replacement with new PAN", "notify customer"},
			NewCardNeeded:  true, ReDispatch: true,
		}, nil
	case DeliveryDamaged:
		return &RecoveryWorkflow{
			Reason:         r,
			CustomerAction: "Your card arrived damaged — a replacement is on the way.",
			Automated:      []string{"capture damaged card evidence", "issue replacement", "notify customer"},
			NewCardNeeded:  true, ReDispatch: true,
		}, nil
	case DeliveryWrongAddress:
		return &RecoveryWorkflow{
			Reason:         r,
			CustomerAction: "We need to confirm your address before re-sending your card.",
			Automated:      []string{"hold re-dispatch", "request address confirmation", "verify against KYC record", "re-dispatch on confirmation"},
			AddressCheck:   true, ReDispatch: true,
		}, nil
	case DeliveryReturned:
		return &RecoveryWorkflow{
			Reason:         r,
			CustomerAction: "Your card was returned to us — confirm your address and we'll re-send it.",
			Automated:      []string{"quarantine returned item", "request address confirmation", "re-dispatch on confirmation"},
			AddressCheck:   true, ReDispatch: true,
		}, nil
	case DeliveryDelayed:
		return &RecoveryWorkflow{
			Reason:         r,
			CustomerAction: "Your card is running late — we're tracking it and will update you.",
			Automated:      []string{"open courier trace", "set escalation timer (7 days)", "proactive customer update"},
		}, nil
	case "":
		return nil, ErrDeliveryUnknown
	default:
		return nil, fmt.Errorf("%w: %q", ErrDeliveryUnknown, rawReason)
	}
}
