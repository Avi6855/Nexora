// Package decisionops wires shared/decisionops into fraud-service:
// right-to-explain explanations, the human-in-the-loop review queue and
// the model audit trail, held as service state.
package decisionops

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/decisionops"
)

// Service holds the HITL queue and the model audit store.
type Service struct {
	queue  *shared.Queue
	models *shared.ModelStore
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{queue: shared.NewQueue(), models: shared.NewModelStore(), logger: logger}
}

// Explain renders a decision in plain language.
func (s *Service) Explain(d shared.Decision) (shared.Explanation, error) {
	if strings.TrimSpace(d.Outcome) == "" {
		err := fmt.Errorf("outcome is required")
		s.logger.Warn().Err(err).Msg("decision explain failed")
		return shared.Explanation{}, err
	}
	if strings.TrimSpace(d.PolicyID) == "" {
		err := fmt.Errorf("policy_id is required")
		s.logger.Warn().Err(err).Msg("decision explain failed")
		return shared.Explanation{}, err
	}
	exp := shared.Explain(d)
	s.logger.Info().Str("policy_id", d.PolicyID).Str("outcome", d.Outcome).Msg("decision explained")
	return exp, nil
}

// Enqueue adds a review case.
func (s *Service) Enqueue(subject string, risk float64, sla time.Duration, now time.Time) (*shared.Case, error) {
	if strings.TrimSpace(subject) == "" {
		err := fmt.Errorf("subject is required")
		s.logger.Warn().Err(err).Msg("review case enqueue failed")
		return nil, err
	}
	if sla <= 0 {
		err := fmt.Errorf("sla must be positive")
		s.logger.Warn().Err(err).Str("subject", subject).Msg("review case enqueue failed")
		return nil, err
	}
	c := s.queue.Enqueue(subject, risk, sla, now)
	s.logger.Info().Str("case_id", c.ID).Str("subject", subject).Float64("risk", risk).Msg("review case enqueued")
	return c, nil
}

// Get returns a case.
func (s *Service) Get(id string) (*shared.Case, error) {
	c, err := s.queue.Get(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("case_id", id).Msg("review case get failed")
		return nil, err
	}
	return c, nil
}

// Lease takes the analyst lock.
func (s *Service) Lease(id, analyst string, ttl time.Duration, now time.Time) error {
	if err := s.queue.Lease(id, analyst, ttl, now); err != nil {
		s.logger.Warn().Err(err).Str("case_id", id).Str("analyst", analyst).Msg("review case lease failed")
		return err
	}
	s.logger.Info().Str("case_id", id).Str("analyst", analyst).Msg("review case leased")
	return nil
}

// Decide records an analyst decision.
func (s *Service) Decide(id, analyst, decision, note string, now time.Time) error {
	if err := s.queue.Decide(id, analyst, decision, note, now); err != nil {
		s.logger.Warn().Err(err).Str("case_id", id).Str("decision", decision).Msg("review case decide failed")
		return err
	}
	s.logger.Info().Str("case_id", id).Str("analyst", analyst).Str("decision", decision).Msg("review case decided")
	return nil
}

// Escalate hands a case to a higher tier.
func (s *Service) Escalate(id, actor, to string, now time.Time) error {
	if err := s.queue.Escalate(id, actor, to, now); err != nil {
		s.logger.Warn().Err(err).Str("case_id", id).Str("to", to).Msg("review case escalate failed")
		return err
	}
	s.logger.Info().Str("case_id", id).Str("to", to).Msg("review case escalated")
	return nil
}

// Reassign moves a case to another analyst.
func (s *Service) Reassign(id, actor, to string, now time.Time) error {
	if err := s.queue.Reassign(id, actor, to, now); err != nil {
		s.logger.Warn().Err(err).Str("case_id", id).Str("to", to).Msg("review case reassign failed")
		return err
	}
	s.logger.Info().Str("case_id", id).Str("to", to).Msg("review case reassigned")
	return nil
}

// SweepTimeouts releases expired leases and escalates SLA breaches.
func (s *Service) SweepTimeouts(now time.Time) []string {
	swept := s.queue.SweepTimeouts(now)
	if len(swept) > 0 {
		s.logger.Info().Int("swept", len(swept)).Msg("review queue sweep completed")
	}
	return swept
}

// Audit returns the full audit trail for a case.
func (s *Service) Audit(id string) ([]shared.AuditEntry, error) {
	trail, err := s.queue.Audit(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("case_id", id).Msg("review case audit failed")
		return nil, err
	}
	return trail, nil
}

// RecordModel appends an immutable model record.
func (s *Service) RecordModel(modelVersion, snapshot, policyVersion, decision string, confidence float64, humanOverride bool, overrideBy string, now time.Time) (*shared.ModelRecord, error) {
	if strings.TrimSpace(modelVersion) == "" {
		err := fmt.Errorf("model_version is required")
		s.logger.Warn().Err(err).Msg("model record failed")
		return nil, err
	}
	if strings.TrimSpace(snapshot) == "" {
		err := fmt.Errorf("feature_snapshot is required")
		s.logger.Warn().Err(err).Msg("model record failed")
		return nil, err
	}
	rec := s.models.Record(modelVersion, snapshot, policyVersion, decision, confidence, humanOverride, overrideBy, now)
	s.logger.Info().Str("record_id", rec.ID).Str("model_version", modelVersion).Str("decision", decision).Msg("model record stored")
	return rec, nil
}

// VerifyModel replays a record against a snapshot.
func (s *Service) VerifyModel(id, snapshot string) (string, error) {
	verdict, err := s.models.Verify(id, snapshot)
	if err != nil {
		s.logger.Warn().Err(err).Str("record_id", id).Msg("model verify failed")
		return "", err
	}
	if verdict == shared.VerdictTampered {
		s.logger.Warn().Str("record_id", id).Str("verdict", verdict).Msg("model verification tampered")
	} else {
		s.logger.Info().Str("record_id", id).Str("verdict", verdict).Msg("model verified")
	}
	return verdict, nil
}

// GetModel returns a model record.
func (s *Service) GetModel(id string) (*shared.ModelRecord, error) {
	rec, err := s.models.Get(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("record_id", id).Msg("model get failed")
		return nil, err
	}
	return rec, nil
}
