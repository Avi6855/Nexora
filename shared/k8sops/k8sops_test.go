package k8sops

import (
	"strings"
	"testing"
)

func TestEvaluateDrain(t *testing.T) {
	safe, err := EvaluateDrain(DrainRequest{Replicas: 5, MaxUnavailable: 1, SLOImpactPct: 0.5, JourneyImpact: "LOW"})
	if err != nil {
		t.Fatal(err)
	}
	if safe.Verdict != VerdictSafe {
		t.Fatalf("expected SAFE, got %+v", safe)
	}
	blocked, err := EvaluateDrain(DrainRequest{Replicas: 3, MaxUnavailable: 3})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Verdict != VerdictBlock {
		t.Fatalf("expected BLOCK when evicting all, got %+v", blocked)
	}
	overBudget, _ := EvaluateDrain(DrainRequest{Replicas: 5, MaxUnavailable: 1, SLOImpactPct: 20})
	if overBudget.Verdict != VerdictBlock {
		t.Fatalf("expected BLOCK on SLO breach, got %+v", overBudget)
	}
	thin, _ := EvaluateDrain(DrainRequest{Replicas: 2, MaxUnavailable: 1, JourneyImpact: "CRITICAL"})
	if thin.Verdict != VerdictBlock {
		t.Fatalf("expected BLOCK for critical journey on thin set, got %+v", thin)
	}
	if _, err := EvaluateDrain(DrainRequest{Replicas: 0}); err == nil {
		t.Fatalf("expected replicas validation error")
	}
	if _, err := EvaluateDrain(DrainRequest{Replicas: 3, JourneyImpact: "bogus"}); err == nil {
		t.Fatalf("expected journey validation error")
	}
}

func TestAdvisePlacement(t *testing.T) {
	nodes := []NodeCandidate{
		{Name: "n1", CPUAvail: 8, MemAvailGB: 32, NetMbps: 1000, DiskAvailGB: 500, Rack: "r1", Zone: "z1", CostPerHour: 1.0},
		{Name: "n2", CPUAvail: 8, MemAvailGB: 32, NetMbps: 10000, DiskAvailGB: 500, Rack: "r2", Zone: "z2", CostPerHour: 0.5},
		{Name: "tiny", CPUAvail: 0.1, MemAvailGB: 0.1, NetMbps: 100, DiskAvailGB: 10, Rack: "r1", Zone: "z1", CostPerHour: 0.1},
	}
	ranked, err := AdvisePlacement(nodes, WorkloadNeeds{CPUCores: 2, MemGB: 4, PreferSpread: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 2 {
		t.Fatalf("tiny node must be filtered, got %v", ranked)
	}
	if ranked[0].Node != "n2" {
		t.Fatalf("expected n2 first (faster + cheaper), got %v", ranked)
	}
	// Failure-domain spread: minority zone wins on otherwise-equal nodes.
	spread := []NodeCandidate{
		{Name: "a1", CPUAvail: 8, MemAvailGB: 32, NetMbps: 1000, DiskAvailGB: 100, Rack: "r1", Zone: "z1", CostPerHour: 1},
		{Name: "a2", CPUAvail: 8, MemAvailGB: 32, NetMbps: 1000, DiskAvailGB: 100, Rack: "r2", Zone: "z1", CostPerHour: 1},
		{Name: "b1", CPUAvail: 8, MemAvailGB: 32, NetMbps: 1000, DiskAvailGB: 100, Rack: "r3", Zone: "z2", CostPerHour: 1},
	}
	ranked, err = AdvisePlacement(spread, WorkloadNeeds{CPUCores: 1, MemGB: 1, PreferSpread: true})
	if err != nil {
		t.Fatal(err)
	}
	if ranked[0].Node != "b1" {
		t.Fatalf("expected minority-zone b1 first, got %v", ranked)
	}
	if _, err := AdvisePlacement(nil, WorkloadNeeds{CPUCores: 1, MemGB: 1}); err == nil {
		t.Fatalf("expected empty nodes error")
	}
	if _, err := AdvisePlacement(nodes, WorkloadNeeds{CPUCores: 999, MemGB: 999}); err == nil {
		t.Fatalf("expected no-feasible-node error")
	}
}

func TestScanFragmentation(t *testing.T) {
	rep := ScanFragmentation([]NodeUsage{
		{Name: "n1", CapacityCPU: 16, RequestedCPU: 4, CapacityMemGB: 64, RequestedMemGB: 8, Taints: []string{"gpu:NoSchedule"}},
		{Name: "n2", CapacityCPU: 16, RequestedCPU: 15.5, CapacityMemGB: 64, RequestedMemGB: 63, Taints: []string{"x:NoSchedule"}},
		{Name: "n3", CapacityCPU: 16, RequestedCPU: 4, CapacityMemGB: 64, RequestedMemGB: 8},
		{Name: "n4", CapacityCPU: 8, RequestedCPU: 2, CapacityMemGB: 16, RequestedMemGB: 4, AffinityBlocked: true},
	})
	if rep.FragmentedNodes != 2 {
		t.Fatalf("expected 2 fragmented nodes, got %+v", rep)
	}
	names := []string{rep.Nodes[0].Node, rep.Nodes[1].Node}
	if names[0] != "n1" || names[1] != "n4" {
		t.Fatalf("expected [n1 n4], got %v", names)
	}
	if rep.StrandedCPU <= 0 {
		t.Fatalf("expected stranded cpu, got %+v", rep)
	}
}

func TestAdviseRightsizing(t *testing.T) {
	adv, err := AdviseRightsizing(SizingInput{Workload: "api", RequestedCPU: 4, P95CPU: 1, P99CPU: 2, RequestedMemGB: 8, P95MemGB: 2, P99MemGB: 3, Critical: true})
	if err != nil {
		t.Fatal(err)
	}
	if adv.SuggestedCPU < 2 {
		t.Fatalf("critical must never go below p99, got %+v", adv)
	}
	if !adv.CriticalGuarded {
		t.Fatalf("expected guardrail flag, got %+v", adv)
	}
	plain, err := AdviseRightsizing(SizingInput{Workload: "worker", RequestedCPU: 4, P95CPU: 1, P99CPU: 2, RequestedMemGB: 8, P95MemGB: 2, P99MemGB: 3})
	if err != nil {
		t.Fatal(err)
	}
	if plain.SuggestedCPU != 1.2 {
		t.Fatalf("expected p95+20%% = 1.2, got %v", plain.SuggestedCPU)
	}
	if _, err := AdviseRightsizing(SizingInput{}); err == nil {
		t.Fatalf("expected workload validation error")
	}
}

func TestScanUpgrade(t *testing.T) {
	comps := []Component{
		{Kind: "API", Name: "extensions/v1beta1", Version: "v1"},
		{Kind: "CRD", Name: "widgets.example.com", Version: "v1"},
	}
	deps := []Deprecation{
		{Kind: "API", Name: "extensions/v1beta1", RemovedIn: "v1.29", Note: "use apps/v1"},
		{Kind: "CRD", Name: "widgets.example.com", RemovedIn: "v1.30", Note: "field rename"},
	}
	rep, err := ScanUpgrade(comps, deps, "v1.29")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Compatible || len(rep.Blocked) != 1 || len(rep.Warnings) != 1 {
		t.Fatalf("expected 1 block + 1 warning, got %+v", rep)
	}
	if !strings.Contains(rep.Blocked[0], "extensions/v1beta1") {
		t.Fatalf("bad block message: %v", rep.Blocked)
	}
	ok, err := ScanUpgrade(comps, deps, "v1.28")
	if err != nil {
		t.Fatal(err)
	}
	if !ok.Compatible || len(ok.Blocked) != 0 {
		t.Fatalf("expected compatible for older target, got %+v", ok)
	}
	if _, err := ScanUpgrade(comps, deps, ""); err == nil {
		t.Fatalf("expected target validation error")
	}
}
