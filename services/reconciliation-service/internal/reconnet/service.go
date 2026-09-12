package reconnet

import (
	"github.com/google/uuid"
	"github.com/nexora/nexora/shared/recon"
)

// Service wraps the shared recon Engine with an in-memory instance for
// the live reconciliation network API.
type Service struct {
	engine *recon.Engine
}

// NewService returns a Service backed by a fresh in-memory Engine.
func NewService() *Service {
	return &Service{engine: recon.NewEngine()}
}

// Engine exposes the underlying engine (tests, ops tooling).
func (s *Service) Engine() *recon.Engine {
	return s.engine
}

// OpenBatch creates a batch; empty id is replaced with a UUID.
func (s *Service) OpenBatch(id string, window recon.Window, tolerances []recon.ToleranceRule) (*recon.Batch, error) {
	if id == "" {
		id = uuid.NewString()
	}
	return s.engine.OpenBatch(id, window, tolerances)
}

// Ingest appends source legs to a batch.
func (s *Service) Ingest(batchID string, source recon.Source, items []recon.ReconItem) error {
	return s.engine.Ingest(batchID, source, items)
}

// Run evaluates outcomes for a batch.
func (s *Service) Run(batchID string) (map[string]recon.Outcome, error) {
	return s.engine.Run(batchID)
}

// Repairs returns sub-tolerance auto-repair proposals.
func (s *Service) Repairs(batchID string) ([]recon.RepairProposal, error) {
	return s.engine.ProposeRepairs(batchID)
}

// PostCorrection stores a correction idempotently by correction ID.
func (s *Service) PostCorrection(c recon.Correction) (*recon.Correction, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return s.engine.PostCorrection(c)
}

// AcknowledgeException marks an exception acknowledged.
func (s *Service) AcknowledgeException(id string) (*recon.ExceptionCase, error) {
	return s.engine.AcknowledgeException(id)
}

// ResolveException marks an exception resolved.
func (s *Service) ResolveException(id string, resolution string) (*recon.ExceptionCase, error) {
	return s.engine.ResolveException(id, resolution)
}

// ExceptionQueue lists open + acknowledged exceptions.
func (s *Service) ExceptionQueue() []*recon.ExceptionCase {
	return s.engine.ExceptionQueue()
}

// Audit returns the hash-chained trail for a batch ("" for all).
func (s *Service) Audit(batchID string) []recon.AuditEntry {
	return s.engine.AuditTrail(batchID)
}

// Summary returns batch outcome counts.
func (s *Service) Summary(batchID string) (*recon.BatchSummary, error) {
	return s.engine.BatchSummary(batchID)
}
