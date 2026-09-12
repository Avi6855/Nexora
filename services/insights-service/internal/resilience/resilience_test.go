package resilience

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func newTestRouter() *mux.Router {
	r := mux.NewRouter()
	RegisterResilienceRoutes(r, NewService(zerolog.Nop()))
	return r
}

func doRequest(t *testing.T, r *mux.Router, method, path string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestServiceRun(t *testing.T) {
	svc := NewService(zerolog.Nop())
	sc, err := svc.CreateScenario("acc-1", "job loss", true, 20000, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 600k available over (100k total + 20k hike) = 5 months → covers 3-month gap.
	out, err := svc.RunScenario(sc.ID, RunwayInputs{AvailableNow: 600000, EssentialsMonthly: 60000, TotalMonthly: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if out.SurvivalMonthsTotal != 5 {
		t.Fatalf("survival = %v, want 5", out.SurvivalMonthsTotal)
	}
	if !out.CoversGap {
		t.Fatalf("should cover 3-month gap: %+v", out)
	}
	// Rent-hike-only scenario short of its gap.
	sc2, err := svc.CreateScenario("acc-1", "rent hike", false, 50000, 6)
	if err != nil {
		t.Fatal(err)
	}
	out2, err := svc.RunScenario(sc2.ID, RunwayInputs{AvailableNow: 300000, EssentialsMonthly: 80000, TotalMonthly: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if out2.SurvivalMonthsTotal != 2 || out2.CoversGap {
		t.Fatalf("outcome = %+v, want 2 months no cover", out2)
	}
	if _, err := svc.RunScenario("missing", RunwayInputs{}); err == nil {
		t.Fatal("expected not-found")
	}
}

func TestHTTPFlow(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/resilience/scenarios", map[string]interface{}{
		"account_id": "acc-1", "name": "job loss", "job_loss": true,
		"rent_hike_monthly": 20000, "income_gap_months": 3,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var sc Scenario
	if err := json.NewDecoder(rec.Body).Decode(&sc); err != nil || sc.ID == "" {
		t.Fatalf("bad scenario: %v %s", err, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/resilience/scenarios?account_id=acc-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/resilience/scenarios/"+sc.ID+"/run", RunwayInputs{
		AvailableNow: 600000, EssentialsMonthly: 60000, TotalMonthly: 100000,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("run = %d (%s)", rec.Code, rec.Body.String())
	}
	var out ScenarioOutcome
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil || !out.CoversGap || out.Verdict == "" {
		t.Fatalf("outcome = %+v (%s)", out, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/resilience/scenarios/missing/run", RunwayInputs{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing run = %d, want 404", rec.Code)
	}
}
