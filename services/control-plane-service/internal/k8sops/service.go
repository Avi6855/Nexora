// Package k8sops wires shared/k8sops into control-plane-service: PDB drain
// safety, placement advice, fragmentation scans, right-sizing and upgrade
// compatibility checks.
package k8sops

import (
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/k8sops"
)

// Service is the control-plane's Kubernetes-operations advisor.
type Service struct {
	logger zerolog.Logger
}

// NewService builds the service.
func NewService(logger zerolog.Logger) *Service {
	return &Service{logger: logger}
}

// CheckDrainSafety evaluates a drain verdict, logging blocks.
func (s *Service) CheckDrainSafety(req shared.DrainRequest) (shared.DrainDecision, error) {
	d, err := shared.EvaluateDrain(req)
	if err != nil {
		return shared.DrainDecision{}, err
	}
	if d.Verdict == shared.VerdictBlock {
		s.logger.Info().Str("verdict", string(d.Verdict)).Str("reason", d.Reason).Msg("k8sops drain blocked")
	}
	return d, nil
}

// AdvisePlacement ranks node candidates for a workload.
func (s *Service) AdvisePlacement(nodes []shared.NodeCandidate, needs shared.WorkloadNeeds) ([]shared.Placement, error) {
	ranked, err := shared.AdvisePlacement(nodes, needs)
	if err != nil {
		return nil, err
	}
	s.logger.Info().Int("options", len(ranked)).Str("top", ranked[0].Node).Msg("k8sops placement advised")
	return ranked, nil
}

// ScanFragmentation reports free-but-unschedulable capacity.
func (s *Service) ScanFragmentation(nodes []shared.NodeUsage) shared.FragmentationReport {
	rep := shared.ScanFragmentation(nodes)
	if rep.FragmentedNodes > 0 {
		s.logger.Info().Int("fragmented", rep.FragmentedNodes).Float64("stranded_cpu", rep.StrandedCPU).Msg("k8sops fragmentation detected")
	}
	return rep
}

// AdviseRightsizing suggests guarded requests.
func (s *Service) AdviseRightsizing(in shared.SizingInput) (shared.SizingAdvice, error) {
	return shared.AdviseRightsizing(in)
}

// ScanUpgrade checks component compatibility for a target version,
// logging blocked upgrades.
func (s *Service) ScanUpgrade(components []shared.Component, deprecated []shared.Deprecation, target string) (shared.UpgradeReport, error) {
	rep, err := shared.ScanUpgrade(components, deprecated, target)
	if err != nil {
		return shared.UpgradeReport{}, err
	}
	if !rep.Compatible {
		s.logger.Info().Str("target", target).Int("blocked", len(rep.Blocked)).Msg("k8sops upgrade blocked by deprecations")
	}
	return rep, nil
}
