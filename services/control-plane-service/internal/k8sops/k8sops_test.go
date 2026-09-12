package k8sops

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/k8sops"
)

func testService() *Service {
	return NewService(zerolog.Nop())
}

func TestServiceDelegates(t *testing.T) {
	s := testService()
	d, err := s.CheckDrainSafety(shared.DrainRequest{Replicas: 5, MaxUnavailable: 1})
	if err != nil || d.Verdict != shared.VerdictSafe {
		t.Fatalf("expected SAFE, got %+v %v", d, err)
	}
	d, err = s.CheckDrainSafety(shared.DrainRequest{Replicas: 1, MaxUnavailable: 1})
	if err != nil || d.Verdict != shared.VerdictBlock {
		t.Fatalf("expected BLOCK, got %+v %v", d, err)
	}
	nodes := []shared.NodeCandidate{
		{Name: "n1", CPUAvail: 8, MemAvailGB: 32, NetMbps: 1000, DiskAvailGB: 100, Zone: "z1", Rack: "r1", CostPerHour: 1},
		{Name: "n2", CPUAvail: 8, MemAvailGB: 32, NetMbps: 5000, DiskAvailGB: 100, Zone: "z2", Rack: "r2", CostPerHour: 0.5},
	}
	ranked, err := s.AdvisePlacement(nodes, shared.WorkloadNeeds{CPUCores: 1, MemGB: 1, PreferSpread: true})
	if err != nil || ranked[0].Node != "n2" {
		t.Fatalf("expected n2 first, got %v %v", ranked, err)
	}
	rep := s.ScanFragmentation([]shared.NodeUsage{
		{Name: "n1", CapacityCPU: 16, RequestedCPU: 4, CapacityMemGB: 64, RequestedMemGB: 8, Taints: []string{"t:NoSchedule"}},
	})
	if rep.FragmentedNodes != 1 {
		t.Fatalf("expected 1 fragmented node, got %+v", rep)
	}
	adv, err := s.AdviseRightsizing(shared.SizingInput{Workload: "api", RequestedCPU: 4, P95CPU: 1, P99CPU: 2, RequestedMemGB: 8, P95MemGB: 2, P99MemGB: 3, Critical: true})
	if err != nil || adv.SuggestedCPU < 2 {
		t.Fatalf("critical guardrail broken: %+v %v", adv, err)
	}
	up, err := s.ScanUpgrade(
		[]shared.Component{{Kind: "API", Name: "old/v1", Version: "v1"}},
		[]shared.Deprecation{{Kind: "API", Name: "old/v1", RemovedIn: "v1.29", Note: "gone"}},
		"v1.29",
	)
	if err != nil || up.Compatible {
		t.Fatalf("expected blocked upgrade, got %+v %v", up, err)
	}
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)
	post := func(path string, body interface{}) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := post("/v1/k8sops/drain-safety", shared.DrainRequest{Replicas: 5, MaxUnavailable: 1}); rec.Code != http.StatusOK {
		t.Fatalf("drain-safety: %d %s", rec.Code, rec.Body.String())
	} else {
		var d shared.DrainDecision
		if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
			t.Fatal(err)
		}
		if d.Verdict != shared.VerdictSafe {
			t.Fatalf("expected SAFE, got %+v", d)
		}
	}
	if rec := post("/v1/k8sops/drain-safety", shared.DrainRequest{Replicas: 0}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad drain must 400, got %d", rec.Code)
	}
	placeBody := map[string]interface{}{
		"nodes": []shared.NodeCandidate{{Name: "n1", CPUAvail: 8, MemAvailGB: 32, NetMbps: 1000, DiskAvailGB: 100, Zone: "z1", Rack: "r1"}},
		"needs": shared.WorkloadNeeds{CPUCores: 1, MemGB: 1},
	}
	if rec := post("/v1/k8sops/placement/advise", placeBody); rec.Code != http.StatusOK {
		t.Fatalf("placement: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/v1/k8sops/placement/advise", map[string]interface{}{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty placement must 400, got %d", rec.Code)
	}
	fragBody := map[string]interface{}{
		"nodes": []shared.NodeUsage{{Name: "n1", CapacityCPU: 16, RequestedCPU: 4, CapacityMemGB: 64, RequestedMemGB: 8, Taints: []string{"t:NoSchedule"}}},
	}
	if rec := post("/v1/k8sops/fragmentation/scan", fragBody); rec.Code != http.StatusOK {
		t.Fatalf("fragmentation: %d", rec.Code)
	}
	sizeBody := shared.SizingInput{Workload: "api", RequestedCPU: 4, P95CPU: 1, P99CPU: 1.1, RequestedMemGB: 8, P95MemGB: 2, P99MemGB: 2.1}
	if rec := post("/v1/k8sops/rightsizing/advise", sizeBody); rec.Code != http.StatusOK {
		t.Fatalf("rightsizing: %d %s", rec.Code, rec.Body.String())
	}
	upBody := map[string]interface{}{
		"components":   []shared.Component{{Kind: "API", Name: "old/v1"}},
		"deprecations": []shared.Deprecation{{Kind: "API", Name: "old/v1", RemovedIn: "v1.29"}},
		"target":       "v1.29",
	}
	if rec := post("/v1/k8sops/upgrade/scan", upBody); rec.Code != http.StatusOK {
		t.Fatalf("upgrade: %d", rec.Code)
	}
	if rec := post("/v1/k8sops/upgrade/scan", map[string]interface{}{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty upgrade must 400, got %d", rec.Code)
	}
}
