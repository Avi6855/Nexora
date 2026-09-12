package mldata

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/mldata"
)

func testService() *Service {
	return NewService(zerolog.Nop())
}

func TestServiceFlows(t *testing.T) {
	s := testService()
	if err := s.AddEdge("feat", "model", "feeds"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEdge("model", "dash", "feeds"); err != nil {
		t.Fatal(err)
	}
	imp, err := s.SimulateImpact("model")
	if err != nil || imp.Count != 1 {
		t.Fatalf("expected blast radius 1, got %+v %v", imp, err)
	}
	if err := s.SetSLA("f", time.Hour); err != nil {
		t.Fatal(err)
	}
	res, err := s.CheckFreshness("f", 2*time.Hour)
	if err != nil || res.Verdict != shared.FreshBlock {
		t.Fatalf("expected BLOCK, got %+v %v", res, err)
	}
	if err := s.SetBaseline("k", shared.Distribution{Median: 100, P95: 200}); err != nil {
		t.Fatal(err)
	}
	drift, err := s.CheckDrift("k", shared.Distribution{Median: 200, P95: 200})
	if err != nil || !drift.Alert {
		t.Fatalf("expected drift alert, got %+v %v", drift, err)
	}
	m := shared.ModelDeps{ID: "m", Features: []string{"f"}, Datasets: []string{"d"}, Policies: []string{"p"}}
	if err := s.RegisterModel(m); err != nil {
		t.Fatal(err)
	}
	ok, err := s.VerifyDeps("m", map[string]bool{"d": true})
	if err != nil || !ok.Healthy {
		t.Fatalf("expected healthy, got %+v %v", ok, err)
	}
	safe := s.CheckRollback(map[string]string{"a": "float"}, map[string]string{"a": "float"})
	if safe.Verdict != shared.RollbackSafe {
		t.Fatalf("expected safe, got %+v", safe)
	}
	unsafe := s.CheckRollback(map[string]string{"a": "float"}, map[string]string{"a": "int"})
	if unsafe.Verdict != shared.RollbackUnsafe {
		t.Fatalf("expected unsafe, got %+v", unsafe)
	}
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)
	do := func(method, path string, body interface{}) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	if rec := do("POST", "/v1/mldata/graph/edges", map[string]string{"from": "f", "to": "m"}); rec.Code != http.StatusCreated {
		t.Fatalf("graph edge: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/v1/mldata/graph/edges", map[string]string{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty edge must 400, got %d", rec.Code)
	}
	if rec := do("GET", "/v1/mldata/impact/simulate?node=ghost", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown impact must 404, got %d", rec.Code)
	}
	if rec := do("GET", "/v1/mldata/impact/simulate", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing node must 400, got %d", rec.Code)
	}
	if rec := do("PUT", "/v1/mldata/freshness/sla", map[string]interface{}{"feature": "f", "max_age_ms": 3600000}); rec.Code != http.StatusOK {
		t.Fatalf("sla: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/v1/mldata/freshness/check", map[string]interface{}{"feature": "f", "age_ms": 1000}); rec.Code != http.StatusOK {
		t.Fatalf("freshness check: %d", rec.Code)
	}
	if rec := do("POST", "/v1/mldata/freshness/check", map[string]interface{}{"feature": "ghost", "age_ms": 1}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown feature must 404, got %d", rec.Code)
	}
	if rec := do("POST", "/v1/mldata/drift/baselines", map[string]interface{}{"key": "k", "median": 1, "p95": 2}); rec.Code != http.StatusCreated {
		t.Fatalf("baseline: %d", rec.Code)
	}
	if rec := do("POST", "/v1/mldata/drift/check", map[string]interface{}{"key": "k", "median": 1, "p95": 2}); rec.Code != http.StatusOK {
		t.Fatalf("drift check: %d", rec.Code)
	}
	if rec := do("POST", "/v1/mldata/drift/check", map[string]interface{}{"key": "ghost"}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing baseline must 404, got %d", rec.Code)
	}
	model := shared.ModelDeps{ID: "m", Features: []string{"f"}, Datasets: []string{"d"}, Policies: []string{"p"}}
	if rec := do("POST", "/v1/mldata/models/register", model); rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/v1/mldata/models/register", model); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate must 409, got %d", rec.Code)
	}
	if rec := do("POST", "/v1/mldata/models/m/verify-deps", map[string]interface{}{"dataset_health": map[string]bool{"d": true}}); rec.Code != http.StatusOK {
		t.Fatalf("verify-deps: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("POST", "/v1/mldata/models/ghost/verify-deps", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown model must 404, got %d", rec.Code)
	}
	rb := map[string]interface{}{"expected": map[string]string{"a": "float"}, "current": map[string]string{"a": "float"}}
	if rec := do("POST", "/v1/mldata/models/rollback-check", rb); rec.Code != http.StatusOK {
		t.Fatalf("rollback-check: %d", rec.Code)
	}
	if rec := do("POST", "/v1/mldata/models/rollback-check", map[string]interface{}{}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty rollback must 400, got %d", rec.Code)
	}
}
