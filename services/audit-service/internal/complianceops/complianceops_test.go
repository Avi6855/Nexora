package complianceops

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func newTestRouter() *mux.Router {
	r := mux.NewRouter()
	RegisterComplianceOpsRoutes(r, NewService(zerolog.Nop()))
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

func TestHTTPFlow(t *testing.T) {
	r := newTestRouter()
	// 19. Evidence bundle + verify.
	rec := doRequest(t, r, "POST", "/v1/compliance-ops/investigations/inv-1/bundle", map[string]interface{}{
		"customers": []string{"cust-1"}, "transactions": []string{"txn-1"},
		"devices": []string{"dev-1"}, "payments": []string{"pay-1"},
		"decisions": []string{"dec-1"}, "documents": []string{"doc-1"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bundle = %d (%s)", rec.Code, rec.Body.String())
	}
	var bundle map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatal(err)
	}
	bid, _ := bundle["id"].(string)
	rec = doRequest(t, r, "GET", "/v1/compliance-ops/bundles/"+bid+"/verify", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("VALID")) {
		t.Fatalf("verify = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/compliance-ops/bundles/missing/verify", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing bundle = %d, want 404", rec.Code)
	}
	// 20. Snapshot isolation.
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/investigations/inv-1/snapshot", map[string]interface{}{
		"customer_id": "cust-1", "state": `{"tier":"standard"}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("snapshot = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/investigations/inv-1/snapshot", map[string]interface{}{
		"customer_id": "cust-1", "state": `{"tier":"other"}`,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second snapshot = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/compliance-ops/investigations/inv-1/snapshot", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("standard")) {
		t.Fatalf("pinned snapshot = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/compliance-ops/investigations/missing/snapshot", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing snapshot = %d, want 404", rec.Code)
	}
	// 21. SLA policy + tick with seeded case.
	rec = doRequest(t, r, "PUT", "/v1/compliance-ops/sla-policies", map[string]interface{}{
		"case_type": "KYC", "deadline_hours": 24, "priority": "HIGH", "escalation": "fin-crime-queue",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("policy = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "PUT", "/v1/compliance-ops/sla-policies", map[string]interface{}{
		"case_type": "BOGUS", "deadline_hours": 1, "priority": "P1", "escalation": "q",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad policy = %d, want 400", rec.Code)
	}
	now := time.Now().UTC()
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/sla/tick", map[string]interface{}{
		"now": now.Format(time.RFC3339),
		"open": []map[string]interface{}{
			{"id": "sla-1", "case_type": "KYC", "created_at": now.Add(-25 * time.Hour).Format(time.RFC3339)},
		},
	})
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("BREACH")) {
		t.Fatalf("breach tick = %d (%s)", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("ESCALATED")) {
		t.Fatalf("escalation expected (%s)", rec.Body.String())
	}
	// 22. Sampling determinism.
	pop := []map[string]interface{}{
		{"id": "d-1"}, {"id": "d-2", "high_risk": true}, {"id": "d-3", "borderline": true},
	}
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/sampling/run", map[string]interface{}{
		"population": pop, "n": 2, "seed": 7,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("sampling = %d (%s)", rec.Code, rec.Body.String())
	}
	first := rec.Body.String()
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/sampling/run", map[string]interface{}{
		"population": pop, "n": 2, "seed": 7,
	})
	if rec.Body.String() != first {
		t.Fatalf("sampling must be deterministic:\n%s\n%s", first, rec.Body.String())
	}
	// 23. Coverage gaps.
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/requirements", map[string]interface{}{
		"id": "REQ-1", "implemented_rule": "block sanctioned", "service": "payments",
		"test_ref": "t-1", "monitor_ref": "m-1",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("requirement = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/requirements", map[string]interface{}{
		"id": "REQ-1", "implemented_rule": "x", "service": "y",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate requirement = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/compliance-ops/coverage", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("REQ-1")) {
		t.Fatalf("coverage = %d (%s)", rec.Code, rec.Body.String())
	}
	// 24. Control runs with a failing control.
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/control-tests", map[string]interface{}{
		"id": "c-1", "name": "restricted blocked", "scenario": "restricted_customer", "expected": "BLOCK",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("control = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/control-tests", map[string]interface{}{
		"id": "c-2", "name": "wrong", "scenario": "clean", "expected": "BLOCK",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("control 2 = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/control-runs", map[string]interface{}{})
	if rec.Code != http.StatusCreated || !bytes.Contains(rec.Body.Bytes(), []byte("FAIL")) {
		t.Fatalf("control run must FAIL = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/compliance-ops/control-tests", map[string]interface{}{
		"id": "c-3", "name": "bad", "scenario": "bogus", "expected": "BLOCK",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad scenario = %d, want 400", rec.Code)
	}
}
