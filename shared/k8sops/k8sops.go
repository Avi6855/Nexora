// Package k8sops implements Kubernetes operational safety for the control
// plane:
//
//  35. PDB safety: {replicas, max_unavailable, slo_impact, journey_impact}
//     → drain verdict SAFE/BLOCK + reason.
//  36. Placement advisor: node candidates {cpu, mem, net, disk, rack, zone}
//     + workload needs → ranked placements balancing failure-domain
//     spread, performance and cost.
//  37. Fragmentation detector: nodes with free-but-unschedulable capacity
//     (constraints/affinity/taints) → fragmentation report.
//  38. Right-sizing: requested vs p95 usage → suggested request with
//     criticality guardrails (never below p99 for critical).
//  39. Upgrade scanner: cluster components {apis, CRDs, controllers,
//     policies} vs target version deprecation list → compatibility report.
package k8sops

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// ─── 35. PDB safety ─────────────────────────────────────────────────────

// DrainVerdict is the drain safety answer.
type DrainVerdict string

const (
	// VerdictSafe allows the drain to proceed.
	VerdictSafe DrainVerdict = "SAFE"
	// VerdictBlock stops the drain.
	VerdictBlock DrainVerdict = "BLOCK"
)

// DrainRequest describes one drain decision.
type DrainRequest struct {
	Replicas       int     `json:"replicas"`
	MaxUnavailable int     `json:"max_unavailable"`
	SLOImpactPct   float64 `json:"slo_impact_pct"`
	JourneyImpact  string  `json:"journey_impact"`
}

// DrainDecision is the verdict plus human-readable reason.
type DrainDecision struct {
	Verdict DrainVerdict `json:"verdict"`
	Reason  string       `json:"reason"`
}

// EvaluateDrain computes SAFE/BLOCK. Blocks when the drain could take all
// replicas, leaves zero survivors, breaches the error-budget guardrail, or
// risks a critical journey on a thin replica set.
func EvaluateDrain(req DrainRequest) (DrainDecision, error) {
	if req.Replicas <= 0 {
		return DrainDecision{}, fmt.Errorf("replicas must be positive")
	}
	if req.MaxUnavailable < 0 {
		return DrainDecision{}, fmt.Errorf("max_unavailable must not be negative")
	}
	journey := strings.ToUpper(strings.TrimSpace(req.JourneyImpact))
	if journey == "" {
		journey = "LOW"
	}
	switch journey {
	case "NONE", "LOW", "HIGH", "CRITICAL":
	default:
		return DrainDecision{}, fmt.Errorf("journey_impact must be NONE, LOW, HIGH or CRITICAL")
	}
	if req.MaxUnavailable >= req.Replicas {
		return DrainDecision{Verdict: VerdictBlock,
			Reason: fmt.Sprintf("max_unavailable %d >= replicas %d: drain could evict everything", req.MaxUnavailable, req.Replicas)}, nil
	}
	if req.Replicas-req.MaxUnavailable < 1 {
		return DrainDecision{Verdict: VerdictBlock,
			Reason: "drain leaves zero surviving replicas"}, nil
	}
	if req.SLOImpactPct > 5 {
		return DrainDecision{Verdict: VerdictBlock,
			Reason: fmt.Sprintf("slo impact %.1f%% exceeds 5%% error-budget guardrail", req.SLOImpactPct)}, nil
	}
	if journey == "CRITICAL" && req.Replicas < 3 {
		return DrainDecision{Verdict: VerdictBlock,
			Reason: fmt.Sprintf("critical journey on %d replicas: needs >= 3 for a safe drain", req.Replicas)}, nil
	}
	if journey == "CRITICAL" && req.SLOImpactPct > 1 && req.MaxUnavailable > 1 {
		return DrainDecision{Verdict: VerdictBlock,
			Reason: fmt.Sprintf("critical journey with slo impact %.1f%% cannot tolerate %d unavailable", req.SLOImpactPct, req.MaxUnavailable)}, nil
	}
	return DrainDecision{Verdict: VerdictSafe,
		Reason: fmt.Sprintf("%d replicas, %d max unavailable, slo impact %.1f%%, journey %s: quorum survives",
			req.Replicas, req.MaxUnavailable, req.SLOImpactPct, journey)}, nil
}

// ─── 36. Placement advisor ──────────────────────────────────────────────

// NodeCandidate is one schedulable node.
type NodeCandidate struct {
	Name        string  `json:"name"`
	CPUAvail    float64 `json:"cpu_avail"`
	MemAvailGB  float64 `json:"mem_avail_gb"`
	NetMbps     float64 `json:"net_mbps"`
	DiskAvailGB float64 `json:"disk_avail_gb"`
	Rack        string  `json:"rack"`
	Zone        string  `json:"zone"`
	CostPerHour float64 `json:"cost_per_hour"`
}

// WorkloadNeeds is what the workload requires.
type WorkloadNeeds struct {
	CPUCores     float64 `json:"cpu_cores"`
	MemGB        float64 `json:"mem_gb"`
	MinNetMbps   float64 `json:"min_net_mbps"`
	PreferSpread bool    `json:"prefer_spread"`
}

// Placement is one ranked option.
type Placement struct {
	Node   string  `json:"node"`
	Score  float64 `json:"score"`
	Reason string  `json:"reason"`
}

// AdvisePlacement filters infeasible nodes and ranks the rest by
// failure-domain spread (minority zone/rack preferred), performance
// headroom (network + disk) and cost (cheaper preferred).
func AdvisePlacement(nodes []NodeCandidate, needs WorkloadNeeds) ([]Placement, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("at least one node candidate is required")
	}
	if needs.CPUCores <= 0 || needs.MemGB <= 0 {
		return nil, fmt.Errorf("workload cpu and memory must be positive")
	}
	zoneCount := map[string]int{}
	rackCount := map[string]int{}
	for _, n := range nodes {
		zoneCount[n.Zone]++
		rackCount[n.Rack]++
	}
	var maxNet, maxDisk float64
	for _, n := range nodes {
		if n.NetMbps > maxNet {
			maxNet = n.NetMbps
		}
		if n.DiskAvailGB > maxDisk {
			maxDisk = n.DiskAvailGB
		}
	}
	var maxCost float64
	for _, n := range nodes {
		if n.CostPerHour > maxCost {
			maxCost = n.CostPerHour
		}
	}
	out := []Placement{}
	for _, n := range nodes {
		if n.CPUAvail < needs.CPUCores || n.MemAvailGB < needs.MemGB {
			continue
		}
		if needs.MinNetMbps > 0 && n.NetMbps < needs.MinNetMbps {
			continue
		}
		score := 0.0
		reasons := []string{}
		if needs.PreferSpread {
			zb := 1.0 / float64(zoneCount[n.Zone])
			rb := 1.0 / float64(rackCount[n.Rack])
			score += 40*zb + 20*rb
			reasons = append(reasons, fmt.Sprintf("spread: zone %s rack %s", n.Zone, n.Rack))
		}
		if maxNet > 0 {
			score += 20 * (n.NetMbps / maxNet)
		}
		if maxDisk > 0 {
			score += 10 * (n.DiskAvailGB / maxDisk)
		}
		reasons = append(reasons, fmt.Sprintf("perf: %.0fmbps %.0fgb-disk", n.NetMbps, n.DiskAvailGB))
		if maxCost > 0 {
			score += 10 * (1 - n.CostPerHour/maxCost)
			reasons = append(reasons, fmt.Sprintf("cost %.3f/h", n.CostPerHour))
		}
		// Headroom bonus: comfortably large nodes absorb bursts.
		score += 5 * math.Min(n.CPUAvail/needs.CPUCores, 4) / 4
		score = math.Round(score*100) / 100
		out = append(out, Placement{Node: n.Name, Score: score, Reason: strings.Join(reasons, "; ")})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no feasible node for %.2f cpu / %.2fGB", needs.CPUCores, needs.MemGB)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Node < out[j].Node
	})
	return out, nil
}

// ─── 37. Fragmentation detector ─────────────────────────────────────────

// NodeUsage is one node's capacity vs scheduled load plus scheduling
// constraints.
type NodeUsage struct {
	Name            string   `json:"name"`
	CapacityCPU     float64  `json:"capacity_cpu"`
	RequestedCPU    float64  `json:"requested_cpu"`
	CapacityMemGB   float64  `json:"capacity_mem_gb"`
	RequestedMemGB  float64  `json:"requested_mem_gb"`
	Taints          []string `json:"taints"`
	AffinityBlocked bool     `json:"affinity_blocked"`
	Unschedulable   bool     `json:"unschedulable"`
}

// FragmentedNode is one node with stranded capacity.
type FragmentedNode struct {
	Node      string   `json:"node"`
	FreeCPU   float64  `json:"free_cpu"`
	FreeMemGB float64  `json:"free_mem_gb"`
	Reasons   []string `json:"reasons"`
}

// FragmentationReport is the detector answer.
type FragmentationReport struct {
	FragmentedNodes int              `json:"fragmented_nodes"`
	StrandedCPU     float64          `json:"stranded_cpu"`
	StrandedMemGB   float64          `json:"stranded_mem_gb"`
	Nodes           []FragmentedNode `json:"nodes"`
}

const (
	// FragmentCPUMin is the free-CPU floor that counts as stranded.
	FragmentCPUMin = 1.0
	// FragmentMemMinGB is the free-memory floor that counts as stranded.
	FragmentMemMinGB = 2.0
)

// ScanFragmentation reports nodes holding significant free capacity that
// cannot be scheduled due to taints, affinity or cordoning.
func ScanFragmentation(nodes []NodeUsage) FragmentationReport {
	rep := FragmentationReport{Nodes: []FragmentedNode{}}
	for _, n := range nodes {
		freeCPU := n.CapacityCPU - n.RequestedCPU
		freeMem := n.CapacityMemGB - n.RequestedMemGB
		if freeCPU < 0 {
			freeCPU = 0
		}
		if freeMem < 0 {
			freeMem = 0
		}
		reasons := []string{}
		if len(n.Taints) > 0 {
			reasons = append(reasons, fmt.Sprintf("taints: %s", strings.Join(n.Taints, ",")))
		}
		if n.AffinityBlocked {
			reasons = append(reasons, "affinity/anti-affinity blocks scheduling")
		}
		if n.Unschedulable {
			reasons = append(reasons, "node cordoned (unschedulable)")
		}
		if len(reasons) == 0 {
			continue
		}
		if freeCPU >= FragmentCPUMin || freeMem >= FragmentMemMinGB {
			freeCPU = math.Round(freeCPU*100) / 100
			freeMem = math.Round(freeMem*100) / 100
			rep.Nodes = append(rep.Nodes, FragmentedNode{Node: n.Name, FreeCPU: freeCPU, FreeMemGB: freeMem, Reasons: reasons})
			rep.StrandedCPU += freeCPU
			rep.StrandedMemGB += freeMem
		}
	}
	rep.FragmentedNodes = len(rep.Nodes)
	rep.StrandedCPU = math.Round(rep.StrandedCPU*100) / 100
	rep.StrandedMemGB = math.Round(rep.StrandedMemGB*100) / 100
	sort.Slice(rep.Nodes, func(i, j int) bool { return rep.Nodes[i].Node < rep.Nodes[j].Node })
	if rep.Nodes == nil {
		rep.Nodes = []FragmentedNode{}
	}
	return rep
}

// ─── 38. Right-sizing ───────────────────────────────────────────────────

// SizingInput pairs requested resources with observed usage.
type SizingInput struct {
	Workload       string  `json:"workload"`
	RequestedCPU   float64 `json:"requested_cpu"`
	P95CPU         float64 `json:"p95_cpu"`
	P99CPU         float64 `json:"p99_cpu"`
	RequestedMemGB float64 `json:"requested_mem_gb"`
	P95MemGB       float64 `json:"p95_mem_gb"`
	P99MemGB       float64 `json:"p99_mem_gb"`
	Critical       bool    `json:"critical"`
}

// SizingAdvice is the suggested request.
type SizingAdvice struct {
	Workload        string  `json:"workload"`
	SuggestedCPU    float64 `json:"suggested_cpu"`
	SuggestedMemGB  float64 `json:"suggested_mem_gb"`
	CriticalGuarded bool    `json:"critical_guarded"`
	Reason          string  `json:"reason"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// AdviseRightsizing suggests requests at p95 + 20% headroom; critical
// workloads never go below p99 (guardrail against tail-latency evictions).
func AdviseRightsizing(in SizingInput) (SizingAdvice, error) {
	if strings.TrimSpace(in.Workload) == "" {
		return SizingAdvice{}, fmt.Errorf("workload is required")
	}
	if in.RequestedCPU <= 0 || in.RequestedMemGB <= 0 {
		return SizingAdvice{}, fmt.Errorf("requested cpu and memory must be positive")
	}
	if in.P95CPU < 0 || in.P99CPU < 0 || in.P95MemGB < 0 || in.P99MemGB < 0 {
		return SizingAdvice{}, fmt.Errorf("usage percentiles must not be negative")
	}
	cpu := in.P95CPU * 1.2
	mem := in.P95MemGB * 1.2
	guarded := false
	if in.Critical {
		if cpu < in.P99CPU {
			cpu = in.P99CPU
			guarded = true
		}
		if mem < in.P99MemGB {
			mem = in.P99MemGB
			guarded = true
		}
	}
	// Never suggest microscopic requests: floor at 0.05 CPU / 0.05GB.
	if cpu < 0.05 {
		cpu = 0.05
	}
	if mem < 0.05 {
		mem = 0.05
	}
	cpu, mem = round2(cpu), round2(mem)
	reason := fmt.Sprintf("p95+20%% headroom (cpu p95 %.2f p99 %.2f, mem p95 %.2f p99 %.2f)", in.P95CPU, in.P99CPU, in.P95MemGB, in.P99MemGB)
	if in.Critical {
		if guarded {
			reason += "; critical guardrail pinned at p99"
		} else {
			reason += "; critical guardrail satisfied (p95+headroom >= p99)"
		}
	}
	if cpu > in.RequestedCPU || mem > in.RequestedMemGB {
		reason += "; above current request — scale up"
	} else {
		reason += "; below current request — safe to right-size down"
	}
	return SizingAdvice{Workload: in.Workload, SuggestedCPU: cpu, SuggestedMemGB: mem, CriticalGuarded: guarded, Reason: reason}, nil
}

// ─── 39. Upgrade scanner ────────────────────────────────────────────────

// Component is one cluster component (API, CRD, controller, policy).
type Component struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Deprecation marks a component removed or changed in a target version.
type Deprecation struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	RemovedIn string `json:"removed_in"`
	Note      string `json:"note"`
}

// UpgradeReport is the compatibility answer for a target version.
type UpgradeReport struct {
	Target     string   `json:"target"`
	Compatible bool     `json:"compatible"`
	Blocked    []string `json:"blocked"`
	Warnings   []string `json:"warnings"`
}

// ScanUpgrade matches components against the target's deprecation list.
// Exact kind+name hits on the target version block; hits slated for a
// later version warn.
func ScanUpgrade(components []Component, deprecated []Deprecation, target string) (UpgradeReport, error) {
	if strings.TrimSpace(target) == "" {
		return UpgradeReport{}, fmt.Errorf("target version is required")
	}
	rep := UpgradeReport{Target: target, Compatible: true, Blocked: []string{}, Warnings: []string{}}
	for _, c := range components {
		for _, d := range deprecated {
			if !strings.EqualFold(c.Kind, d.Kind) || c.Name != d.Name {
				continue
			}
			msg := fmt.Sprintf("%s %q deprecated (%s)", c.Kind, c.Name, d.Note)
			if d.RemovedIn == target {
				rep.Blocked = append(rep.Blocked, msg)
			} else {
				rep.Warnings = append(rep.Warnings, msg+fmt.Sprintf(" — removed in %s", d.RemovedIn))
			}
		}
	}
	sort.Strings(rep.Blocked)
	sort.Strings(rep.Warnings)
	rep.Compatible = len(rep.Blocked) == 0
	return rep, nil
}
