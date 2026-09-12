// Package releaseops wires shared/releaseops into control-plane-service:
// capacity forecasts, the dependency-gated release manager with canaries,
// and the contract observatory, held as service state.
package releaseops

import (
	"fmt"
	"strings"

	shared "github.com/nexora/nexora/shared/releaseops"
)

// Service holds the forecaster, release manager and observatory.
type Service struct {
	forecast  *shared.Forecaster
	releases  *shared.ReleaseManager
	contracts *shared.Observatory
}

// NewService builds the service.
func NewService() *Service {
	return &Service{
		forecast:  shared.NewForecaster(),
		releases:  shared.NewReleaseManager(),
		contracts: shared.NewObservatory(),
	}
}

// AddSamples records traffic samples for a service.
func (s *Service) AddSamples(service string, samples []float64) error {
	return s.forecast.AddSamples(service, samples)
}

// Forecast computes the capacity recommendation.
func (s *Service) Forecast(service string, samples []float64, uplifts []shared.EventUplift, perReplicaRPS float64) (shared.Forecast, error) {
	return s.forecast.Forecast(service, samples, uplifts, perReplicaRPS)
}

// RegisterDep declares a service dependency.
func (s *Service) RegisterDep(service, dependsOn string) error {
	return s.releases.RegisterDep(service, dependsOn)
}

// ReportHealth records service health.
func (s *Service) ReportHealth(service, status string) error {
	if strings.TrimSpace(service) == "" {
		return fmt.Errorf("service is required")
	}
	return s.releases.ReportHealth(service, status)
}

// GateDeploy reports whether a deploy may proceed.
func (s *Service) GateDeploy(service string) (bool, string) {
	return s.releases.GateDeploy(service)
}

// StartCanary opens a canary at 1%.
func (s *Service) StartCanary(service, version string) (*shared.Canary, error) {
	return s.releases.StartCanary(service, version)
}

// AdvanceCanary moves a canary to the next stage.
func (s *Service) AdvanceCanary(id string) (*shared.Canary, error) {
	return s.releases.AdvanceCanary(id)
}

// GetCanary returns a canary.
func (s *Service) GetCanary(id string) (*shared.Canary, error) {
	return s.releases.GetCanary(id)
}

// RecordUsage records one field-usage sample.
func (s *Service) RecordUsage(endpoint, field string, used bool) error {
	return s.contracts.RecordUsage(endpoint, field, used)
}

// FieldStats returns usage percentages for a field.
func (s *Service) FieldStats(endpoint, field string) (shared.FieldStats, error) {
	return s.contracts.Stats(endpoint, field)
}

// SafeToRemove reports whether usage sits under the threshold.
func (s *Service) SafeToRemove(endpoint, field string, thresholdPct float64) (bool, shared.FieldStats, error) {
	return s.contracts.SafeToRemove(endpoint, field, thresholdPct)
}

// Deprecate moves a field into the deprecation workflow.
func (s *Service) Deprecate(endpoint, field string) error {
	return s.contracts.Deprecate(endpoint, field)
}
