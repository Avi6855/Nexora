// Package incidentops wires shared/incidentops into incident-service:
// signal triage, impact calculation and compensation awards, held as
// service state.
package incidentops

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/incidentops"
)

// Service holds the incident-ops store.
type Service struct {
	store  *shared.Store
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{store: shared.NewStore(), logger: logger}
}

// Ingest records one signal.
func (s *Service) Ingest(service string, kind, message string, isError bool, at time.Time) (shared.Signal, error) {
	sig, err := s.store.Ingest(service, kind, message, isError, at)
	if err != nil {
		s.logger.Warn().Err(err).Str("service", service).Str("kind", kind).Msg("signal ingest failed")
		return shared.Signal{}, err
	}
	s.logger.Info().Str("signal_id", sig.ID).Str("service", service).Str("kind", kind).Msg("signal ingested")
	return sig, nil
}

// Correlate groups signals into incident candidates.
func (s *Service) Correlate(window time.Duration, now time.Time) ([]shared.Candidate, error) {
	if window <= 0 {
		err := fmt.Errorf("window must be positive")
		s.logger.Warn().Err(err).Msg("correlate failed")
		return nil, err
	}
	cands := s.store.Correlate(window, now)
	s.logger.Info().Int("candidates", len(cands)).Msg("signals correlated")
	return cands, nil
}

// Triage assesses one candidate.
func (s *Service) Triage(candidateID string) (shared.TriageResult, error) {
	res, err := s.store.Triage(candidateID)
	if err != nil {
		s.logger.Warn().Err(err).Str("candidate_id", candidateID).Msg("triage failed")
		return shared.TriageResult{}, err
	}
	s.logger.Info().Str("candidate_id", candidateID).Str("severity", res.Severity).Msg("candidate triaged")
	return res, nil
}

// Calculate maps incident services onto a ledger snapshot.
func (s *Service) Calculate(services []string, snapshot []shared.ServiceLedger) (shared.Impact, error) {
	if len(services) == 0 {
		err := fmt.Errorf("at least one service is required")
		s.logger.Warn().Err(err).Msg("impact calculate failed")
		return shared.Impact{}, err
	}
	imp := s.store.Calculate(services, snapshot)
	s.logger.Info().Int("services", len(services)).Int("customers", imp.Customers).Msg("impact calculated")
	return imp, nil
}

// AddPolicy registers a compensation rule.
func (s *Service) AddPolicy(id string, minOutageMinutes int, eligibility string, amountMinor int64) error {
	if err := s.store.AddPolicy(id, minOutageMinutes, eligibility, amountMinor); err != nil {
		s.logger.Warn().Err(err).Str("policy_id", id).Msg("comp policy add failed")
		return err
	}
	s.logger.Info().Str("policy_id", id).Int64("amount_minor", amountMinor).Msg("comp policy added")
	return nil
}

// Evaluate finds the best matching compensation policy.
func (s *Service) Evaluate(account string, outageMinutes int, tier string) (bool, int64, string) {
	return s.store.Evaluate(account, outageMinutes, tier)
}

// Award grants compensation for a claim.
func (s *Service) Award(claimKey, account string, outageMinutes int, tier string, now time.Time) (*shared.CompAward, error) {
	if strings.TrimSpace(claimKey) == "" || strings.TrimSpace(account) == "" {
		err := fmt.Errorf("claim key and account are required")
		s.logger.Warn().Err(err).Msg("comp award failed")
		return nil, err
	}
	a, err := s.store.Award(claimKey, account, outageMinutes, tier, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("claim_key", claimKey).Str("account", account).Msg("comp award failed")
		return nil, err
	}
	if a.FraudFlag {
		s.logger.Warn().Str("award_id", a.ID).Str("account", account).Msg("comp award flagged")
	} else {
		s.logger.Info().Str("award_id", a.ID).Str("account", account).Int64("amount_minor", a.AmountMinor).Msg("comp award granted")
	}
	return a, nil
}

// Override corrects an award.
func (s *Service) Override(awardID, approver string, newAmount *int64, newStatus string, now time.Time) (*shared.CompAward, error) {
	a, err := s.store.Override(awardID, approver, newAmount, newStatus, now)
	if err != nil {
		s.logger.Warn().Err(err).Str("award_id", awardID).Msg("comp override failed")
		return nil, err
	}
	s.logger.Info().Str("award_id", awardID).Str("approver", approver).Msg("comp award overridden")
	return a, nil
}

// GetAward returns an award.
func (s *Service) GetAward(id string) (*shared.CompAward, error) {
	a, err := s.store.GetAward(id)
	if err != nil {
		s.logger.Warn().Err(err).Str("award_id", id).Msg("comp award get failed")
		return nil, err
	}
	return a, nil
}
