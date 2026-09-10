package platform

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/cards"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// Platform wires shared/cards into card-service with mutex-guarded state.
type Platform struct {
	mu         sync.Mutex
	vcards     map[string]cards.VirtualCard
	rules      *cards.RuleEngine
	vault      *cards.TokenVault
	authorizer *cards.LockedAuthorizer
	sagas      map[string]*cards.CardSaga
}

// NewPlatform builds an empty platform.
func NewPlatform() *Platform {
	return &Platform{
		vcards:     map[string]cards.VirtualCard{},
		rules:      cards.NewRuleEngine(),
		vault:      cards.NewTokenVault(),
		authorizer: &cards.LockedAuthorizer{MerchantGroups: map[string]string{}},
		sagas:      map[string]*cards.CardSaga{},
	}
}

// CreateLockedCard provisions a merchant-locked virtual card.
func (p *Platform) CreateLockedCard(linkedCardID, lockedGroup string) (cards.VirtualCard, error) {
	if strings.TrimSpace(linkedCardID) == "" {
		return cards.VirtualCard{}, fmt.Errorf("linked_card_id is required")
	}
	vc := cards.VirtualCard{
		ID:           uuid.NewString(),
		LinkedCardID: linkedCardID,
		LockedGroup:  strings.ToUpper(strings.TrimSpace(lockedGroup)),
		Active:       true,
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vcards[vc.ID] = vc
	return vc, nil
}

// GetCard returns a stored virtual card.
func (p *Platform) GetCard(id string) (cards.VirtualCard, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	vc, ok := p.vcards[id]
	if !ok {
		return cards.VirtualCard{}, fmt.Errorf("virtual card %s: %w", id, ErrNotFound)
	}
	return vc, nil
}

// AuthorizeMerchant enforces the merchant lock.
func (p *Platform) AuthorizeMerchant(cardID string, m cards.MerchantIdentity, amountMinor int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	vc, ok := p.vcards[cardID]
	if !ok {
		return fmt.Errorf("virtual card %s: %w", cardID, ErrNotFound)
	}
	return p.authorizer.Authorize(vc, m, amountMinor)
}

// SetRules replaces the authorization rules for a card.
func (p *Platform) SetRules(cardID string, rules []cards.AuthRule) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.vcards[cardID]; !ok {
		return fmt.Errorf("virtual card %s: %w", cardID, ErrNotFound)
	}
	p.rules.SetRules(cardID, rules)
	return nil
}

// EvaluateSpend runs the rule chain for one spend attempt.
func (p *Platform) EvaluateSpend(cardID string, att cards.AuthAttempt) (cards.AuthAction, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.vcards[cardID]; !ok {
		return "", "", fmt.Errorf("virtual card %s: %w", cardID, ErrNotFound)
	}
	att.CardID = cardID
	return p.rules.Evaluate(cardID, att)
}

// RegisterToken provisions a network token against a card.
func (p *Platform) RegisterToken(tokenID, cardID, merchant string, now time.Time) (*cards.NetworkToken, error) {
	if strings.TrimSpace(cardID) == "" {
		return nil, fmt.Errorf("card_id is required")
	}
	if strings.TrimSpace(merchant) == "" {
		return nil, fmt.Errorf("merchant is required")
	}
	if strings.TrimSpace(tokenID) == "" {
		tokenID = uuid.NewString()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.vault.Register(tokenID, cardID, merchant, now), nil
}

// ReplaceCardCredentials re-points active tokens to a new funding card.
func (p *Platform) ReplaceCardCredentials(oldCardID, newCardID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.vault.ReplaceCard(oldCardID, newCardID)
}

// SuspendToken suspends a network token.
func (p *Platform) SuspendToken(tokenID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.vault.Suspend(tokenID)
}

// ChargeThroughToken resolves a merchant charge to its funding card.
func (p *Platform) ChargeThroughToken(tokenID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.vault.ChargeThroughToken(tokenID)
}

// StartLifecycle starts a card saga at REQUESTED.
func (p *Platform) StartLifecycle(cardID string, now time.Time) (*cards.CardSaga, error) {
	if strings.TrimSpace(cardID) == "" {
		return nil, fmt.Errorf("card_id is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.sagas[cardID]; ok {
		return nil, fmt.Errorf("lifecycle for %s: %w", cardID, ErrConflict)
	}
	s := cards.NewCardSaga(cardID, now)
	p.sagas[cardID] = s
	return s, nil
}

// AdvanceLifecycle moves a saga one stage forward.
func (p *Platform) AdvanceLifecycle(id string, now time.Time) (*cards.CardSaga, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sagas[id]
	if !ok {
		return nil, fmt.Errorf("lifecycle %s: %w", id, ErrNotFound)
	}
	if err := s.Advance(now); err != nil {
		return nil, err
	}
	return s, nil
}

// ReportPartnerFailure records a partner failure with compensation.
func (p *Platform) ReportPartnerFailure(id string, now time.Time, reason string) (*cards.CardSaga, error) {
	if strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("reason is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sagas[id]
	if !ok {
		return nil, fmt.Errorf("lifecycle %s: %w", id, ErrNotFound)
	}
	s.PartnerFailure(now, reason)
	return s, nil
}

// ClassifyDelivery maps a courier failure to its recovery workflow.
func (p *Platform) ClassifyDelivery(rawReason string) (*cards.RecoveryWorkflow, error) {
	return cards.ClassifyDeliveryFailure(rawReason)
}
