package txpolicy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func testRouter() *mux.Router {
	svc := NewService()
	h := NewHandlers(svc, zerolog.Nop())
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	return router
}

func doRequest(t *testing.T, router *mux.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestRoutesRulesEvaluateAudit(t *testing.T) {
	router := testRouter()

	rec := doRequest(t, router, "PUT", "/v1/tx-policy/rules",
		`{"id":"cap","priority":1,"predicate":{"min_amount_minor":50000},"effect":"block"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put rule status %d: %s", rec.Code, rec.Body.String())
	}
	var rule map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatal(err)
	}
	if rule["version"] != float64(1) {
		t.Fatalf("version %v, want 1", rule)
	}

	// Missing id -> 400; unknown effect -> 400.
	if rec := doRequest(t, router, "PUT", "/v1/tx-policy/rules", `{"effect":"block"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id status %d, want 400", rec.Code)
	}
	if rec := doRequest(t, router, "PUT", "/v1/tx-policy/rules", `{"id":"x","effect":"nuke"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad effect status %d, want 400", rec.Code)
	}

	// Evaluate a blocked and an allowed context.
	rec = doRequest(t, router, "POST", "/v1/tx-policy/evaluate", `{"amount_minor":60000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("evaluate status %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res["effect"] != "block" || res["rule_id"] != "cap" {
		t.Fatalf("evaluate wrong: %v", res)
	}

	// Audit records both evaluations below (this one + the blocked one).
	if rec := doRequest(t, router, "POST", "/v1/tx-policy/evaluate", `{"amount_minor":10}`); rec.Code != http.StatusOK {
		t.Fatalf("evaluate small status %d", rec.Code)
	}
	rec = doRequest(t, router, "GET", "/v1/tx-policy/audit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status %d", rec.Code)
	}
	var entries []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit length %d, want 2", len(entries))
	}
}

func TestRoutesRollbackAndSimulate(t *testing.T) {
	router := testRouter()

	doRequest(t, router, "PUT", "/v1/tx-policy/rules",
		`{"id":"cap","priority":1,"predicate":{"min_amount_minor":50000},"effect":"block"}`)
	doRequest(t, router, "PUT", "/v1/tx-policy/rules",
		`{"id":"cap","priority":1,"predicate":{"min_amount_minor":50000},"effect":"approve"}`)

	// Rollback restores v1 (block).
	rec := doRequest(t, router, "POST", "/v1/tx-policy/rules/cap/rollback", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status %d: %s", rec.Code, rec.Body.String())
	}
	var restored map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&restored); err != nil {
		t.Fatal(err)
	}
	if restored["version"] != float64(1) || restored["effect"] != "block" {
		t.Fatalf("rollback wrong: %v", restored)
	}
	// Second rollback -> 409 (no prior version); unknown -> 404.
	if rec := doRequest(t, router, "POST", "/v1/tx-policy/rules/cap/rollback", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("double rollback status %d, want 409", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/tx-policy/rules/missing/rollback", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown rollback status %d, want 404", rec.Code)
	}

	// Shadow simulation: candidate loosens cap to 500000 but adds a 100000
	// big-block. Current blocks both large contexts; candidate blocks only
	// the 200000 one.
	rec = doRequest(t, router, "POST", "/v1/tx-policy/simulate", `{
		"candidate_rules": [
			{"id":"cap","priority":1,"predicate":{"min_amount_minor":500000},"effect":"block"},
			{"id":"big-block","priority":5,"predicate":{"min_amount_minor":100000},"effect":"block"}
		],
		"contexts": [
			{"amount_minor":60000,"destination_risk":10},
			{"amount_minor":200000,"destination_risk":10},
			{"amount_minor":100,"destination_risk":5}
		]
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("simulate status %d: %s", rec.Code, rec.Body.String())
	}
	var sim map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&sim); err != nil {
		t.Fatal(err)
	}
	if sim["current_blocks"] != float64(2) || sim["new_blocks"] != float64(1) || sim["delta"] != float64(-1) {
		t.Fatalf("simulate math wrong: %v", sim)
	}
	if sim["false_positive_delta"] != float64(-1) {
		t.Fatalf("simulate false-positive delta wrong: %v", sim)
	}
	// Candidate without id -> 400.
	if rec := doRequest(t, router, "POST", "/v1/tx-policy/simulate",
		`{"candidate_rules":[{"effect":"block"}],"contexts":[]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad candidate status %d, want 400", rec.Code)
	}
}
