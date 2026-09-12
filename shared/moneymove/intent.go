package moneymove

import (
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
)

// Intent is the customer's stated will: what they asked for, in their words.
type Intent struct {
	ID                string    `json:"id"`
	CustomerStatement string    `json:"customer_statement"`
	Amount            int64     `json:"amount"`
	Payee             string    `json:"payee"`
	CreatedAt         time.Time `json:"created_at"`
}

// ExecutionPlan is the planned set of legs derived from the intent.
type ExecutionPlan struct {
	IntentID string   `json:"intent_id"`
	Steps    []string `json:"steps"`
}

// Execution is what actually happened on the rails.
type Execution struct {
	IntentID   string    `json:"intent_id"`
	Legs       []string  `json:"legs"`
	ExecutedAt time.Time `json:"executed_at"`
}

// IntentRecord pairs an intent and its plan with the (optional) execution,
// flagging DIVERGED when legs differ from the plan.
type IntentRecord struct {
	Intent    Intent        `json:"intent"`
	Plan      ExecutionPlan `json:"plan"`
	Execution *Execution    `json:"execution,omitempty"`
	Diverged  bool          `json:"diverged"`
	Reason    string        `json:"reason,omitempty"`
}

// RecordIntent stores a new intent with its execution plan.
func (s *Store) RecordIntent(customerStatement string, amount int64, payee string, steps []string) (*IntentRecord, error) {
	if customerStatement == "" {
		return nil, fmt.Errorf("customer_statement is required")
	}
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if payee == "" {
		return nil, fmt.Errorf("payee is required")
	}
	if len(steps) == 0 {
		return nil, fmt.Errorf("at least one plan step is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.NewString()
	rec := &IntentRecord{
		Intent: Intent{
			ID:                id,
			CustomerStatement: customerStatement,
			Amount:            amount,
			Payee:             payee,
			CreatedAt:         time.Now().UTC(),
		},
		Plan: ExecutionPlan{IntentID: id, Steps: append([]string(nil), steps...)},
	}
	s.intents[id] = rec
	s.logger.Info().Str("intent_id", id).Str("payee", payee).Int64("amount", amount).Msg("moneymove intent recorded")
	cp := *rec
	return &cp, nil
}

// ExecuteIntent records the actual legs for an intent, flagging DIVERGED when
// legs != plan with a human-readable reason.
func (s *Store) ExecuteIntent(id string, legs []string) (*IntentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.intents[id]
	if !ok {
		return nil, ErrIntentNotFound
	}
	if len(legs) == 0 {
		return nil, fmt.Errorf("at least one execution leg is required")
	}
	exec := &Execution{IntentID: id, Legs: append([]string(nil), legs...), ExecutedAt: time.Now().UTC()}
	rec.Execution = exec
	if reflect.DeepEqual(exec.Legs, rec.Plan.Steps) {
		rec.Diverged = false
		rec.Reason = ""
	} else {
		rec.Diverged = true
		rec.Reason = fmt.Sprintf("legs != plan: expected %d step(s) %v, got %d leg(s) %v",
			len(rec.Plan.Steps), rec.Plan.Steps, len(exec.Legs), exec.Legs)
	}
	s.logger.Info().Str("intent_id", id).Bool("diverged", rec.Diverged).Msg("moneymove intent executed")
	cp := *rec
	return &cp, nil
}

// GetIntent returns a copy of an intent record.
func (s *Store) GetIntent(id string) (*IntentRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.intents[id]
	if !ok {
		return nil, ErrIntentNotFound
	}
	cp := *rec
	return &cp, nil
}

// Divergence reports whether an executed intent diverged from its plan.
func (s *Store) Divergence(id string) (bool, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.intents[id]
	if !ok {
		return false, "", ErrIntentNotFound
	}
	if rec.Execution == nil {
		return false, "not yet executed", nil
	}
	return rec.Diverged, rec.Reason, nil
}
