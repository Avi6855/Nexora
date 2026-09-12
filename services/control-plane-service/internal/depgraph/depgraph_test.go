package depgraph

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func testService() *Service {
	return NewService(zerolog.Nop())
}

func TestBlastRadiusOrdered(t *testing.T) {
	s := testService()
	_ = s.ReportHealth("ledger-service", "HEALTHY", 5)
	_ = s.ReportHealth("payment-service", "HEALTHY", 5)
	g := s.Graph()
	if len(g.Nodes) == 0 {
		t.Fatal("expected nodes")
	}
	for i := 1; i < len(g.Nodes); i++ {
		if g.Nodes[i].BlastRadius > g.Nodes[i-1].BlastRadius {
			t.Fatalf("nodes not blast-radius ordered: %+v", g.Nodes)
		}
	}
	// ledger-service fans into many dependents → top blast radius.
	if g.Nodes[0].Name != "ledger-service" {
		t.Fatalf("expected ledger-service first, got %s", g.Nodes[0].Name)
	}
}

func TestThrottleAdvice(t *testing.T) {
	s := testService()
	if got := s.Advice(); len(got) != 0 {
		t.Fatalf("expected no advice when healthy, got %v", got)
	}
	_ = s.ReportHealth("ledger-service", "DEGRADED", 900)
	advice := s.Advice()
	if len(advice) == 0 {
		t.Fatal("expected throttle advice for degraded ledger")
	}
	found := false
	for _, a := range advice {
		if !strings.Contains(a.Message, "ledger-service") || !strings.Contains(a.Message, "throttle") {
			t.Fatalf("bad advice message: %q", a.Message)
		}
		if a.ThrottlePct != 40 {
			t.Fatalf("expected 40%% for degraded, got %d", a.ThrottlePct)
		}
		if a.Target == "payment-service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected advice targeting payment-service, got %+v", advice)
	}
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)

	raw, _ := json.Marshal(map[string]interface{}{"service": "ledger-service", "status": "DEGRADED", "latency_ms": 800})
	req := httptest.NewRequest("POST", "/v1/depgraph/health", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/v1/depgraph/graph", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("graph: %d", rec.Code)
	}
	var g Graph
	if err := json.NewDecoder(rec.Body).Decode(&g); err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) == 0 {
		t.Fatal("expected graph nodes")
	}

	req = httptest.NewRequest("GET", "/v1/depgraph/advice", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("advice: %d", rec.Code)
	}
	var advice []Advice
	if err := json.NewDecoder(rec.Body).Decode(&advice); err != nil {
		t.Fatal(err)
	}
	if len(advice) == 0 {
		t.Fatal("expected advice")
	}
}
