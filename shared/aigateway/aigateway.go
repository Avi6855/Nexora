// Package aigateway implements Nexora's delegated-consent AI gateway:
//
//  19. An AI agent acts only under delegated consent: an Intent{action,
//     params} resolves its recipient, the gateway checks the granted-scope
//     map, validates amount limits and the risk gate, requires human
//     confirmation (a token with expiry) for mutating actions, and finally
//     executes a stub returning executed/skipped plus a reason. Anything
//     unconsented, over-limit, over-risk or unconfirmed never executes.
package aigateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrNoConsent       = errors.New("no granted consent scope for this action")
	ErrOverLimit       = errors.New("intent amount exceeds granted scope limit")
	ErrRiskBlocked     = errors.New("intent risk exceeds granted scope gate")
	ErrConfirmRequired = errors.New("human confirmation required before execute")
	ErrConfirmExpired  = errors.New("confirmation token expired")
	ErrBadToken        = errors.New("invalid confirmation token")
	ErrIntentNotFound  = errors.New("unknown intent")
	ErrIntentClosed    = errors.New("intent is already closed")
)

// ConfirmTTL is how long a human-confirmation token stays valid.
const ConfirmTTL = 10 * time.Minute

// Intent statuses.
const (
	StatusPendingConfirmation = "PENDING_CONFIRMATION"
	StatusReady               = "READY"
	StatusConfirmed           = "CONFIRMED"
	StatusExecuted            = "EXECUTED"
	StatusSkipped             = "SKIPPED"
	StatusBlocked             = "BLOCKED"
)

// Scope is one granted delegated-consent scope.
type Scope struct {
	Recipient  string
	Action     string
	LimitMinor int64
	MaxRisk    int
	GrantedAt  time.Time
}

// Intent is one AI-submitted action request.
type Intent struct {
	ID            string
	Action        string
	Recipient     string
	Params        map[string]string
	AmountMinor   int64
	RiskScore     int
	Status        string
	ConfirmToken  string
	ConfirmExpiry time.Time
	Outcome       string
	Reason        string
	CreatedAt     time.Time
}

// IsMutating reports whether an action changes state (and therefore needs
// human confirmation). Read-only actions execute without confirmation.
func IsMutating(action string) bool {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "transfer", "pay", "payout", "withdraw", "send", "refund",
		"update", "close", "delete", "revoke":
		return true
	}
	return false
}

// resolveRecipient finds the consent principal from the intent params.
func resolveRecipient(params map[string]string) string {
	for _, k := range []string{"recipient", "to", "to_account", "payee", "account"} {
		if v := strings.TrimSpace(params[k]); v != "" {
			return v
		}
	}
	return ""
}

// Gateway enforces delegated consent around AI intents.
type Gateway struct {
	mu      sync.Mutex
	scopes  map[string]map[string]Scope // recipient → action → scope
	intents map[string]*Intent
	seq     int
}

// NewGateway builds an empty gateway.
func NewGateway() *Gateway {
	return &Gateway{scopes: map[string]map[string]Scope{}, intents: map[string]*Intent{}}
}

// GrantScope delegates consent for recipient+action with a spend limit and
// a risk gate.
func (g *Gateway) GrantScope(recipient, action string, limitMinor int64, maxRisk int, now time.Time) error {
	if strings.TrimSpace(recipient) == "" || strings.TrimSpace(action) == "" {
		return errors.New("recipient and action are required")
	}
	if limitMinor <= 0 {
		return errors.New("scope limit must be positive")
	}
	if maxRisk < 0 {
		return errors.New("max risk must not be negative")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	recipient = strings.TrimSpace(recipient)
	action = strings.ToLower(strings.TrimSpace(action))
	if g.scopes[recipient] == nil {
		g.scopes[recipient] = map[string]Scope{}
	}
	g.scopes[recipient][action] = Scope{Recipient: recipient, Action: action, LimitMinor: limitMinor, MaxRisk: maxRisk, GrantedAt: now}
	return nil
}

// RevokeScope removes a delegated scope. In-flight intents skip at execute.
func (g *Gateway) RevokeScope(recipient, action string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	recipient = strings.TrimSpace(recipient)
	action = strings.ToLower(strings.TrimSpace(action))
	acts, ok := g.scopes[recipient]
	if !ok {
		return ErrNoConsent
	}
	if _, ok := acts[action]; !ok {
		return ErrNoConsent
	}
	delete(acts, action)
	if len(acts) == 0 {
		delete(g.scopes, recipient)
	}
	return nil
}

func parseAmount(params map[string]string) (int64, error) {
	raw := strings.TrimSpace(params["amount_minor"])
	if raw == "" {
		raw = strings.TrimSpace(params["amount"])
	}
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errors.New("amount must be a non-negative integer (minor units)")
	}
	return n, nil
}

func parseRisk(params map[string]string) (int, error) {
	raw := strings.TrimSpace(params["risk_score"])
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errors.New("risk_score must be a non-negative integer")
	}
	return n, nil
}

func newToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func copyParams(p map[string]string) map[string]string {
	out := make(map[string]string, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

func copyIntent(in *Intent) *Intent {
	cp := *in
	cp.Params = copyParams(in.Params)
	return &cp
}

// SubmitIntent resolves the recipient, checks the granted scope, validates
// limits and the risk gate, and stages human confirmation for mutating
// actions. Blocked intents are recorded with StatusBlocked and the
// corresponding error is returned.
func (g *Gateway) SubmitIntent(action string, params map[string]string, now time.Time) (*Intent, error) {
	if strings.TrimSpace(action) == "" {
		return nil, errors.New("action is required")
	}
	amount, err := parseAmount(params)
	if err != nil {
		return nil, err
	}
	risk, err := parseRisk(params)
	if err != nil {
		return nil, err
	}
	recipient := resolveRecipient(params)
	if recipient == "" {
		return nil, errors.New("recipient could not be resolved from intent params")
	}
	action = strings.ToLower(strings.TrimSpace(action))

	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	in := &Intent{
		ID:          fmt.Sprintf("intent-%d", g.seq),
		Action:      action,
		Recipient:   recipient,
		Params:      copyParams(params),
		AmountMinor: amount,
		RiskScore:   risk,
		CreatedAt:   now,
	}
	block := func(reason string, err error) (*Intent, error) {
		in.Status = StatusBlocked
		in.Outcome = "skipped"
		in.Reason = reason
		g.intents[in.ID] = in
		return copyIntent(in), err
	}
	scope, ok := g.scopes[recipient][action]
	if !ok {
		return block("no granted consent scope for "+recipient+":"+action, ErrNoConsent)
	}
	if amount > scope.LimitMinor {
		return block(fmt.Sprintf("amount %d exceeds scope limit %d", amount, scope.LimitMinor), ErrOverLimit)
	}
	if risk > scope.MaxRisk {
		return block(fmt.Sprintf("risk %d exceeds scope gate %d", risk, scope.MaxRisk), ErrRiskBlocked)
	}
	if IsMutating(action) {
		in.Status = StatusPendingConfirmation
		in.ConfirmToken = newToken()
		in.ConfirmExpiry = now.Add(ConfirmTTL)
	} else {
		in.Status = StatusReady
	}
	g.intents[in.ID] = in
	return copyIntent(in), nil
}

// Get returns a copy of an intent.
func (g *Gateway) Get(id string) (*Intent, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, ok := g.intents[id]
	if !ok {
		return nil, ErrIntentNotFound
	}
	return copyIntent(in), nil
}

// Confirm redeems the human-confirmation token before it expires.
func (g *Gateway) Confirm(id, token string, now time.Time) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, ok := g.intents[id]
	if !ok {
		return ErrIntentNotFound
	}
	if in.Status == StatusExecuted || in.Status == StatusSkipped || in.Status == StatusBlocked {
		return ErrIntentClosed
	}
	if !IsMutating(in.Action) {
		in.Status = StatusConfirmed
		return nil
	}
	if in.Status != StatusPendingConfirmation {
		return ErrIntentClosed
	}
	if token == "" || token != in.ConfirmToken {
		return ErrBadToken
	}
	if now.After(in.ConfirmExpiry) {
		return ErrConfirmExpired
	}
	in.Status = StatusConfirmed
	return nil
}

// Execute runs the stub: it returns executed/skipped plus a reason.
// Mutating actions require confirmation; a scope revoked after submit
// skips instead of executing.
func (g *Gateway) Execute(id string, now time.Time) (string, string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in, ok := g.intents[id]
	if !ok {
		return "", "", ErrIntentNotFound
	}
	if in.Status == StatusExecuted {
		return in.Outcome, in.Reason, ErrIntentClosed
	}
	if in.Status == StatusBlocked || in.Status == StatusSkipped {
		return "skipped", in.Reason, nil
	}
	if _, ok := g.scopes[in.Recipient][in.Action]; !ok {
		in.Status = StatusSkipped
		in.Outcome = "skipped"
		in.Reason = "consent scope revoked before execute"
		return "skipped", in.Reason, nil
	}
	if IsMutating(in.Action) && in.Status != StatusConfirmed {
		return "", "", ErrConfirmRequired
	}
	in.Status = StatusExecuted
	in.Outcome = "executed"
	in.Reason = fmt.Sprintf("executed %s for %s under delegated scope", in.Action, in.Recipient)
	_ = now
	return "executed", in.Reason, nil
}
