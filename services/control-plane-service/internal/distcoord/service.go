// Package distcoord wires shared/distcoord into control-plane-service:
// correlation protocol, causality tracking, clock-skew policy, HLC
// timestamps, lock diagnostics and the contention advisor.
package distcoord

import (
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/distcoord"
)

// Service is the control-plane's distributed-coordination controller.
type Service struct {
	corr      *shared.CorrelationStore
	causality *shared.CausalityTracker
	clocks    *shared.SkewMonitor
	hlc       *shared.Clock
	locks     *shared.LockRegistry
	logger    zerolog.Logger
}

// NewService builds the service with default skew threshold (500ms) and a
// control-plane HLC node id.
func NewService(logger zerolog.Logger) *Service {
	return &Service{
		corr:      shared.NewCorrelationStore(),
		causality: shared.NewCausalityTracker(),
		clocks:    shared.NewSkewMonitor(500),
		hlc:       shared.NewClock("control-plane"),
		locks:     shared.NewLockRegistry(),
		logger:    logger,
	}
}

// IssueCorrelation starts a new trace for one inbound action.
func (s *Service) IssueCorrelation(action string) (shared.Correlation, error) {
	c, err := s.corr.Issue(action)
	if err != nil {
		return shared.Correlation{}, err
	}
	s.logger.Info().Str("correlation_id", c.CorrelationID).Str("action", action).Msg("distcoord correlation issued")
	return c, nil
}

// PropagateCorrelation derives a child span (child causation = parent id).
func (s *Service) PropagateCorrelation(parent shared.Correlation, action string) (shared.Correlation, error) {
	return s.corr.Propagate(parent, action)
}

// ValidateChain checks chain completeness.
func (s *Service) ValidateChain(chain []shared.Correlation) error {
	return shared.ValidateChain(chain)
}

// AddCausalEdge records A → B.
func (s *Service) AddCausalEdge(from, to string) error {
	if err := s.causality.AddEdge(from, to); err != nil {
		return err
	}
	s.logger.Info().Str("from", from).Str("to", to).Msg("distcoord causal edge recorded")
	return nil
}

// WhyHappened returns the full causal chain for a node.
func (s *Service) WhyHappened(node string) ([]string, error) {
	return s.causality.WhyHappened(node)
}

// RecordClockSample stores one node's wall reading.
func (s *Service) RecordClockSample(node string, wallMs int64) error {
	if err := s.clocks.RecordSample(node, wallMs); err != nil {
		return err
	}
	if p := s.clocks.Policy(); p.DisableCritical {
		s.logger.Info().Int64("max_skew_ms", p.MaxSkewMs).Int64("threshold_ms", p.ThresholdMs).Msg("distcoord clock skew over threshold; critical workflows disabled")
	}
	return nil
}

// ClockPolicy returns the skew policy.
func (s *Service) ClockPolicy() shared.SkewPolicy {
	return s.clocks.Policy()
}

// SkewMatrix returns pairwise absolute skews.
func (s *Service) SkewMatrix() map[string]map[string]int64 {
	return s.clocks.SkewMatrix()
}

// HLCIssue returns the next HLC timestamp.
func (s *Service) HLCIssue(wallMs int64) shared.Timestamp {
	ts := s.hlc.Issue(wallMs)
	s.logger.Info().Int64("wall_ms", ts.WallMs).Uint64("logical", ts.Logical).Msg("distcoord hlc issued")
	return ts
}

// HLCReceive merges a remote timestamp on receipt.
func (s *Service) HLCReceive(remote shared.Timestamp, wallMs int64) shared.Timestamp {
	return s.hlc.Receive(remote, wallMs)
}

// LockAcquire takes a lock or queues as waiter.
func (s *Service) LockAcquire(resource, owner string, ttlMs int64, now time.Time) (bool, error) {
	acquired, err := s.locks.Acquire(resource, owner, ttlMs, now)
	if err != nil {
		return false, err
	}
	if acquired {
		s.logger.Info().Str("resource", resource).Str("owner", owner).Msg("distcoord lock acquired")
	} else {
		s.logger.Info().Str("resource", resource).Str("owner", owner).Msg("distcoord lock contended; waiter queued")
	}
	return acquired, nil
}

// LockRelease frees a held lock.
func (s *Service) LockRelease(resource, owner string) error {
	return s.locks.Release(resource, owner)
}

// StuckLocks returns locks held > k×TTL with waiters.
func (s *Service) StuckLocks(k float64, now time.Time) []shared.LockInfo {
	stuck := s.locks.StuckLocks(k, now)
	for _, l := range stuck {
		s.logger.Info().Str("resource", l.Resource).Str("owner", l.Owner).Int("waiters", len(l.Waiters)).Msg("distcoord stuck lock detected")
	}
	return stuck
}

// LockStats returns per-resource contention counters.
func (s *Service) LockStats() []shared.ResourceStats {
	return s.locks.Stats()
}

// AdviseContention maps a wait graph to remediation recommendations.
func (s *Service) AdviseContention(edges []shared.WaitEdge) []shared.ContentionAdvice {
	adv := shared.AdviseContention(edges)
	s.logger.Info().Int("resources", len(adv)).Msg("distcoord contention advice computed")
	return adv
}
