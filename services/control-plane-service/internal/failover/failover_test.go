package failover

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/failover"
)

func testService() *Service {
	return NewService(shared.Config{RPOLagMs: 1000, RTOSeconds: 60}, zerolog.Nop())
}

func TestServiceFailoverRouting(t *testing.T) {
	s := testService()
	if err := s.RegisterRegion("eu-west", shared.RolePrimary); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterRegion("eu-north", shared.RoleStandby); err != nil {
		t.Fatal(err)
	}
	if got := s.Route(); got.Route != shared.RoutePrimary {
		t.Fatalf("expected PRIMARY, got %s", got.Route)
	}
	if err := s.ReportHealth("eu-west", false, 0); err != nil {
		t.Fatal(err)
	}
	if got := s.Route(); got.Route != shared.RouteFailover || got.Target != "eu-north" {
		t.Fatalf("expected FAILOVER to eu-north, got %+v", got)
	}
}

func TestServiceFencingAndDedupe(t *testing.T) {
	s := testService()
	_ = s.RegisterRegion("eu-west", shared.RolePrimary)
	_ = s.RegisterRegion("eu-north", shared.RoleStandby)
	epoch := s.CurrentEpoch()
	if ok, err := s.ExecuteOp("eu-west", "op-1", epoch); err != nil || !ok {
		t.Fatalf("expected execute, got %v %v", ok, err)
	}
	if ok, err := s.ExecuteOp("eu-north", "op-1", epoch); err != nil || ok {
		t.Fatalf("expected dedupe, got %v %v", ok, err)
	}
	s.AdvanceEpoch()
	if _, err := s.ExecuteOp("eu-west", "op-2", epoch); err == nil {
		t.Fatal("expected stale epoch to be fenced")
	}
}

func testRouter(s *Service) *mux.Router {
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)
	return r
}

func doRequest(t *testing.T, r *mux.Router, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		raw, _ := json.Marshal(body)
		buf = bytes.NewBuffer(raw)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := testRouter(s)

	rec := doRequest(t, r, "POST", "/v1/failover/regions", map[string]string{"name": "eu-west", "role": "PRIMARY"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register primary: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/failover/regions", map[string]string{"name": "eu-west", "role": "PRIMARY"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 duplicate, got %d", rec.Code)
	}
	_ = doRequest(t, r, "POST", "/v1/failover/regions", map[string]string{"name": "eu-north", "role": "STANDBY"})

	rec = doRequest(t, r, "POST", "/v1/failover/regions/eu-west/health", map[string]interface{}{"healthy": false, "lag_ms": 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/failover/regions/unknown/health", map[string]interface{}{"healthy": true})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 unknown region, got %d", rec.Code)
	}

	rec = doRequest(t, r, "GET", "/v1/failover/route", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("route: %d", rec.Code)
	}
	var decision shared.RouteDecision
	if err := json.NewDecoder(rec.Body).Decode(&decision); err != nil {
		t.Fatal(err)
	}
	if decision.Route != shared.RouteFailover {
		t.Fatalf("expected FAILOVER, got %s", decision.Route)
	}

	rec = doRequest(t, r, "POST", "/v1/failover/fence/advance", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("advance: %d", rec.Code)
	}
	var adv struct {
		Epoch uint64 `json:"epoch"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&adv); err != nil {
		t.Fatal(err)
	}

	rec = doRequest(t, r, "POST", "/v1/failover/ops/execute", map[string]interface{}{"region": "eu-west", "op_id": "op-9", "epoch": adv.Epoch})
	if rec.Code != http.StatusCreated {
		t.Fatalf("execute: %d %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/failover/ops/execute", map[string]interface{}{"region": "eu-north", "op_id": "op-9", "epoch": adv.Epoch})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 dedupe, got %d", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/failover/ops/execute", map[string]interface{}{"region": "eu-west", "op_id": "op-10", "epoch": adv.Epoch - 1})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 fenced, got %d", rec.Code)
	}

	rec = doRequest(t, r, "GET", "/v1/failover/recovery/diff?primary=eu-west&standby=eu-north", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff: %d %s", rec.Code, rec.Body.String())
	}
}
