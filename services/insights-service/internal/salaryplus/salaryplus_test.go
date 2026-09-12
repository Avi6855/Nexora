package salaryplus

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
	RegisterSalaryPlusRoutes(r, NewService(zerolog.Nop()))
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

func TestServiceEvaluate(t *testing.T) {
	svc := NewService(zerolog.Nop())
	plan, err := svc.CreatePlan("acc-1", []RaiseAllocation{{PotID: "holiday", Percent: 50}, {PotID: "savings", Percent: 30}})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := svc.EvaluatePlan(plan.ID, 320000, 300000)
	if err != nil {
		t.Fatal(err)
	}
	if ev.RaiseMinor != 20000 || len(ev.Intents) != 2 {
		t.Fatalf("evaluation = %+v", ev)
	}
	if ev.Intents[0].AmountMinor != 10000 || ev.Intents[1].AmountMinor != 6000 {
		t.Fatalf("intents = %+v", ev.Intents)
	}
	flat, err := svc.EvaluatePlan(plan.ID, 300000, 300000)
	if err != nil || len(flat.Intents) != 0 {
		t.Fatalf("no-raise = %+v %v", flat, err)
	}
	if _, err := svc.CreatePlan("acc-1", []RaiseAllocation{{PotID: "a", Percent: 60}, {PotID: "b", Percent: 50}}); err == nil {
		t.Fatal("expected over-100% rejection")
	}
	if err := svc.DeletePlan(plan.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeletePlan(plan.ID); err == nil {
		t.Fatal("expected not-found on double delete")
	}
}

func TestHTTPCrudEvaluate(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/salary-plus/raise-plans", map[string]interface{}{
		"account_id": "acc-1",
		"allocations": []map[string]interface{}{
			{"pot_id": "holiday", "percent_of_raise": 50},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var plan RaisePlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil || plan.ID == "" {
		t.Fatalf("bad plan: %v %s", err, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/salary-plus/raise-plans?account_id=acc-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/salary-plus/raise-plans/"+plan.ID+"/evaluate", map[string]int64{
		"last_amount": 310000, "monthly_avg": 300000,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("evaluate = %d (%s)", rec.Code, rec.Body.String())
	}
	var ev RaiseEvaluation
	if err := json.NewDecoder(rec.Body).Decode(&ev); err != nil || len(ev.Intents) != 1 || ev.Intents[0].AmountMinor != 5000 {
		t.Fatalf("evaluation = %+v (%s)", ev, rec.Body.String())
	}
	rec = doRequest(t, r, "DELETE", "/v1/salary-plus/raise-plans/"+plan.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/salary-plus/raise-plans/"+plan.ID+"/evaluate", map[string]int64{})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("evaluate deleted = %d, want 404", rec.Code)
	}
}
