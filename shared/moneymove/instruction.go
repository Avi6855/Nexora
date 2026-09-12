package moneymove

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Instruction is a versioned payment instruction. Every mutation bumps the
// monotonic version v1..vN and appends a snapshot to history.
type Instruction struct {
	ID        string    `json:"id"`
	Amount    int64     `json:"amount"`
	Payee     string    `json:"payee"`
	Status    string    `json:"status"`
	Version   int       `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateInstruction stores a new v1 instruction.
func (s *Store) CreateInstruction(amount int64, payee string) (*Instruction, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if payee == "" {
		return nil, fmt.Errorf("payee is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ins := &Instruction{
		ID:        uuid.NewString(),
		Amount:    amount,
		Payee:     payee,
		Status:    "DRAFT",
		Version:   1,
		UpdatedAt: time.Now().UTC(),
	}
	s.instructions[ins.ID] = ins
	s.histories[ins.ID] = []Instruction{*ins}
	s.logger.Info().Str("instruction_id", ins.ID).Int("version", ins.Version).Msg("moneymove instruction created")
	cp := *ins
	return &cp, nil
}

// GetInstruction returns the current version of an instruction.
func (s *Store) GetInstruction(id string) (*Instruction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ins, ok := s.instructions[id]
	if !ok {
		return nil, ErrInstructionNotFound
	}
	cp := *ins
	return &cp, nil
}

// GetVersion returns the current monotonic version vN.
func (s *Store) GetVersion(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ins, ok := s.instructions[id]
	if !ok {
		return 0, ErrInstructionNotFound
	}
	return ins.Version, nil
}

// History returns every version snapshot v1..vN in order.
func (s *Store) History(id string) ([]Instruction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h, ok := s.histories[id]
	if !ok {
		return nil, ErrInstructionNotFound
	}
	return append([]Instruction(nil), h...), nil
}

// UpdateInstruction applies an optimistic-concurrency mutation: the write
// succeeds only when expectedVersion equals the current version, otherwise it
// fails with ErrVersionConflict (HTTP 409) and no state changes.
func (s *Store) UpdateInstruction(id string, expectedVersion int, amount int64, payee string) (*Instruction, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if payee == "" {
		return nil, fmt.Errorf("payee is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ins, ok := s.instructions[id]
	if !ok {
		return nil, ErrInstructionNotFound
	}
	if ins.Version != expectedVersion {
		return nil, fmt.Errorf("%w: have v%d, expected v%d", ErrVersionConflict, ins.Version, expectedVersion)
	}
	ins.Amount = amount
	ins.Payee = payee
	ins.Version++
	ins.UpdatedAt = time.Now().UTC()
	s.histories[id] = append(s.histories[id], *ins)
	s.logger.Info().Str("instruction_id", id).Int("version", ins.Version).Msg("moneymove instruction updated")
	cp := *ins
	return &cp, nil
}
