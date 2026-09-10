// Package lifeevents wires the shared/lifeevents platform (bereavement
// workflow + life-event workspaces) into user-service as an in-memory
// service. Persistence is intentionally out of scope: the workflow gates
// and DAG/beneficiary invariants live in the shared library; this package
// owns identity (UUIDs), lookup (404s) and mutual exclusion.
package lifeevents

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	shared "github.com/nexora/nexora/shared/lifeevents"
)

// ErrNotFound is returned when a case or workspace ID is unknown.
var ErrNotFound = errors.New("not found")

// Service holds bereavement cases and life-event workspaces.
type Service struct {
	mu         sync.RWMutex
	cases      map[string]*shared.BereavementCase
	workspaces map[string]*shared.Workspace
}

// NewService constructs an empty Service.
func NewService() *Service {
	return &Service{
		cases:      map[string]*shared.BereavementCase{},
		workspaces: map[string]*shared.Workspace{},
	}
}

// ReportBereavement opens a case. Reporting immediately blocks outgoing
// payments (shared guarantee); initial unsettled obligations seed the
// obligations gate.
func (s *Service) ReportBereavement(customerID string, obligations []string) (*shared.BereavementCase, error) {
	if customerID == "" {
		return nil, fmt.Errorf("customer_id is required")
	}
	id := uuid.NewString()
	c := shared.NewBereavementCase(id, customerID, time.Now().UTC())
	if len(obligations) > 0 {
		c.Obligations = append([]string(nil), obligations...)
	}
	s.mu.Lock()
	s.cases[id] = c
	s.mu.Unlock()
	return c, nil
}

// GetCase returns a case by ID.
func (s *Service) GetCase(id string) (*shared.BereavementCase, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	return c, nil
}

// AdvanceCase moves a case to its next stage if the gate is satisfied.
func (s *Service) AdvanceCase(id, actor, reason string) (*shared.BereavementCase, error) {
	if id == "" {
		return nil, fmt.Errorf("case id is required")
	}
	if actor == "" {
		return nil, fmt.Errorf("actor is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	if err := c.Advance(time.Now().UTC(), actor, reason); err != nil {
		return nil, err
	}
	return c, nil
}

// DisputeCase halts a pre-closure case.
func (s *Service) DisputeCase(id, actor, reason string) (*shared.BereavementCase, error) {
	if id == "" {
		return nil, fmt.Errorf("case id is required")
	}
	if actor == "" {
		return nil, fmt.Errorf("actor is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	if err := c.Dispute(time.Now().UTC(), actor, reason); err != nil {
		return nil, err
	}
	return c, nil
}

// ResolveDispute returns a disputed case to its pre-dispute stage.
func (s *Service) ResolveDispute(id, actor string) (*shared.BereavementCase, error) {
	if id == "" {
		return nil, fmt.Errorf("case id is required")
	}
	if actor == "" {
		return nil, fmt.Errorf("actor is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	if err := c.ResolveDispute(time.Now().UTC(), actor); err != nil {
		return nil, err
	}
	return c, nil
}

// AddExecutorDoc records verified executor evidence.
func (s *Service) AddExecutorDoc(id, evidenceID string) (*shared.BereavementCase, error) {
	if id == "" {
		return nil, fmt.Errorf("case id is required")
	}
	if evidenceID == "" {
		return nil, fmt.Errorf("evidence_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	c.VerifyExecutorDoc(evidenceID)
	return c, nil
}

// DiscoverAsset adds an asset to the estate.
func (s *Service) DiscoverAsset(id, assetID string) (*shared.BereavementCase, error) {
	if id == "" {
		return nil, fmt.Errorf("case id is required")
	}
	if assetID == "" {
		return nil, fmt.Errorf("asset_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	c.DiscoverAsset(assetID)
	return c, nil
}

// SettleObligation clears one outgoing obligation (idempotent).
func (s *Service) SettleObligation(id, obligationID string) (*shared.BereavementCase, error) {
	if id == "" {
		return nil, fmt.Errorf("case id is required")
	}
	if obligationID == "" {
		return nil, fmt.Errorf("obligation_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cases[id]
	if !ok {
		return nil, fmt.Errorf("%w: bereavement case %s", ErrNotFound, id)
	}
	c.SettleObligation(obligationID)
	return c, nil
}

// OpenWorkspace creates a life-event workspace for a known event kind.
func (s *Service) OpenWorkspace(kind shared.LifeEventKind) (*shared.Workspace, error) {
	switch kind {
	case shared.EventMarriage, shared.EventBaby, shared.EventDivorce,
		shared.EventMovingHouse, shared.EventRetirement, shared.EventBereavement:
	default:
		return nil, fmt.Errorf("unknown life-event kind %q", kind)
	}
	id := uuid.NewString()
	w, err := shared.NewWorkspace(id, kind, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.workspaces[id] = w
	s.mu.Unlock()
	return w, nil
}

// GetWorkspace returns a workspace by ID.
func (s *Service) GetWorkspace(id string) (*shared.Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.workspaces[id]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", ErrNotFound, id)
	}
	return w, nil
}

// AddTask registers a task, generating an ID when the caller omits one.
func (s *Service) AddTask(workspaceID string, task shared.Task) (*shared.Task, error) {
	if workspaceID == "" {
		return nil, fmt.Errorf("workspace id is required")
	}
	if task.Title == "" {
		return nil, fmt.Errorf("task title is required")
	}
	if task.ID == "" {
		task.ID = uuid.NewString()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workspaces[workspaceID]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", ErrNotFound, workspaceID)
	}
	if err := w.AddTask(task); err != nil {
		return nil, err
	}
	return w.Tasks[task.ID], nil
}

// CompleteTask marks a task done once its dependencies complete.
func (s *Service) CompleteTask(workspaceID, taskID string) (*shared.Task, error) {
	if workspaceID == "" || taskID == "" {
		return nil, fmt.Errorf("workspace id and task id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workspaces[workspaceID]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", ErrNotFound, workspaceID)
	}
	if err := w.CompleteTask(taskID, time.Now().UTC()); err != nil {
		return nil, err
	}
	return w.Tasks[taskID], nil
}

// OverdueTasks returns incomplete past-deadline tasks, oldest first.
func (s *Service) OverdueTasks(workspaceID string) ([]*shared.Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.workspaces[workspaceID]
	if !ok {
		return nil, fmt.Errorf("%w: workspace %s", ErrNotFound, workspaceID)
	}
	return w.Overdue(time.Now().UTC()), nil
}

// SetBeneficiaries replaces beneficiaries; shares must total exactly 100%.
func (s *Service) SetBeneficiaries(workspaceID string, bs []shared.Beneficiary) error {
	if workspaceID == "" {
		return fmt.Errorf("workspace id is required")
	}
	if len(bs) == 0 {
		return fmt.Errorf("at least one beneficiary is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workspaces[workspaceID]
	if !ok {
		return fmt.Errorf("%w: workspace %s", ErrNotFound, workspaceID)
	}
	return w.SetBeneficiaries(bs)
}
