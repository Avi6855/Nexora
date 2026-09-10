// Package dataplatform wires shared/dataplatform into control-plane-service:
// quality contracts, dataset health registry, the privacy query gateway,
// tamper-evident access sessions and the migration safety pipeline.
package dataplatform

import (
	"fmt"
	"strings"
	"sync"
	"time"

	shared "github.com/nexora/nexora/shared/dataplatform"
)

// Service holds the dataset registry, access recorder and migration engine.
type Service struct {
	mu       sync.Mutex
	registry *shared.DatasetRegistry
	products map[string]*shared.DataProduct
	recorder *shared.AccessRecorder
	sessions map[string]*shared.AccessSession
	engine   *shared.MigrationEngine
	policy   shared.GovernancePolicy
}

// NewService builds the service with default governance policy.
func NewService() *Service {
	return &Service{
		registry: shared.NewDatasetRegistry(),
		products: map[string]*shared.DataProduct{},
		recorder: shared.NewAccessRecorder(),
		sessions: map[string]*shared.AccessSession{},
		engine:   shared.NewMigrationEngine(),
		policy:   shared.DefaultGovernancePolicy(),
	}
}

// PublishContractEvaluation evaluates a measurement and records it when the
// dataset is registered.
func (s *Service) PublishContractEvaluation(contract shared.QualityContract, m shared.QualityMeasurement) (shared.QualityVerdict, error) {
	if strings.TrimSpace(contract.Dataset) == "" {
		return shared.QualityVerdict{}, fmt.Errorf("contract dataset is required")
	}
	v := shared.EvaluateContract(contract, m)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.products[contract.Dataset]; ok {
		_ = s.registry.RecordVerdict(contract.Dataset, v)
	}
	return v, nil
}

// RegisterDataset registers a data product.
func (s *Service) RegisterDataset(name, owner, tier string, schema map[string]string, now time.Time) (*shared.DataProduct, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("dataset name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.registry.Register(name, owner, tier, schema, now)
	s.products[name] = p
	return p, nil
}

// GetDataset returns the registered product.
func (s *Service) GetDataset(name string) (*shared.DataProduct, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.products[name]
	if !ok {
		return nil, fmt.Errorf("unknown dataset %s", name)
	}
	return p, nil
}

// DatasetHealth computes the dashboard verdict.
func (s *Service) DatasetHealth(name string) (shared.HealthState, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("dataset name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registry.Health(name)
}

// OpenDatasetIncident records an incident against a dataset.
func (s *Service) OpenDatasetIncident(name string, inc shared.Incident) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("dataset name is required")
	}
	if strings.TrimSpace(inc.ID) == "" {
		return fmt.Errorf("incident id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.products[name]; !ok {
		return fmt.Errorf("unknown dataset %s", name)
	}
	return s.registry.OpenIncident(name, inc)
}

// EvaluateQuery decides the gateway verdict with the service policy.
func (s *Service) EvaluateQuery(req shared.QueryRequest) (shared.QueryVerdict, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return shared.EvaluateQuery(req, s.policy)
}

// StartAccessSession opens a recorded session; duplicates conflict.
func (s *Service) StartAccessSession(id, engineer, ticketRef string, now time.Time) (*shared.AccessSession, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(engineer) == "" {
		return nil, fmt.Errorf("session id and engineer are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[id]; exists {
		return nil, fmt.Errorf("access session %s already exists", id)
	}
	sess, err := s.recorder.StartSession(id, engineer, ticketRef, now)
	if err != nil {
		return nil, err
	}
	s.sessions[id] = sess
	return sess, nil
}

// RecordAccess appends an event to a session.
func (s *Service) RecordAccess(sessionID string, ev shared.AccessEvent) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("unknown access session %s", sessionID)
	}
	sess.Record(ev)
	return nil
}

// VerifyAccessChain recomputes the tamper-evident chain.
func (s *Service) VerifyAccessChain(sessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false, fmt.Errorf("unknown access session %s", sessionID)
	}
	return sess.VerifyChain(), nil
}

// AccessSessionInfo returns the session for inspection.
func (s *Service) AccessSessionInfo(sessionID string) (*shared.AccessSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("unknown access session %s", sessionID)
	}
	return sess, nil
}

// SubmitMigration enters a migration into the gated pipeline.
func (s *Service) SubmitMigration(req shared.MigrationRequest, now time.Time) (*shared.MigrationState, error) {
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Target) == "" {
		return nil, fmt.Errorf("migration id and target are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.engine.Stage(req.ID); err == nil {
		return nil, fmt.Errorf("migration %s already exists", req.ID)
	}
	return s.engine.Submit(req, now)
}

// ValidateMigration moves SUBMITTED → VALIDATING → CAPACITY_ESTIMATED.
func (s *Service) ValidateMigration(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Validate(id)
}

// CanaryMigration runs the migration against a slice of traffic.
func (s *Service) CanaryMigration(id string, pct float64, bad bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Canary(id, pct, bad)
}

// ApplyMigration promotes canary → full application.
func (s *Service) ApplyMigration(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Apply(id)
}

// FailVerification injects a verification failure before completion.
func (s *Service) FailVerification(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.MarkVerifyFailed(id)
}

// PauseMigration halts a running migration.
func (s *Service) PauseMigration(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Pause(id)
}

// ResumeMigration continues a paused migration.
func (s *Service) ResumeMigration(id string) (shared.MigrationStage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Resume(id)
}

// RollbackMigration unwinds a migration before completion.
func (s *Service) RollbackMigration(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Rollback(id)
}

// MigrationStage reports the current stage.
func (s *Service) MigrationStage(id string) (shared.MigrationStage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.engine.Stage(id)
}
