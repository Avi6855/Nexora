package eventgov

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	shared "github.com/nexora/nexora/shared/eventgov"
)

func TestServiceCaptureAndRun(t *testing.T) {
	svc := NewService()
	env := shared.Envelope{Type: "payment.created", PayloadHash: "abc", SchemaVersion: 1}
	if err := svc.Capture("checkout", env); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if err := svc.Capture("checkout", shared.Envelope{Type: "fraud.checked", PayloadHash: "def"}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	run, err := svc.StartRun("checkout", 10)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.Delivered != 20 {
		t.Fatalf("delivered = %d, want 20", run.Delivered)
	}
	if _, err := svc.StartRun("checkout", 7); err == nil {
		t.Fatal("bad multiplier should fail")
	}
	if _, err := svc.StartRun("missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestServiceSchemaFlow(t *testing.T) {
	svc := NewService()
	fields := []shared.Field{{Name: "id", Type: "string", Required: true}}
	if _, err := svc.RegisterSchema("payments", 1, fields); err != nil {
		t.Fatalf("RegisterSchema: %v", err)
	}
	if _, err := svc.RegisterSchema("payments", 1, fields); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
	v2 := append(fields, shared.Field{Name: "note", Type: "string"})
	if _, err := svc.RegisterSchema("payments", 2, v2); err != nil {
		t.Fatalf("RegisterSchema v2: %v", err)
	}
	res, err := svc.CheckCompatibility("payments", 1, 2, shared.ModeBackward)
	if err != nil || !res.Compatible {
		t.Fatalf("backward should pass: %+v %v", res, err)
	}
	if err := svc.DeprecateField("payments", 2, "note"); err != nil {
		t.Fatalf("DeprecateField: %v", err)
	}
	plan, err := svc.MigrationPlan("payments", 1, 2)
	if err != nil || !plan.Safe {
		t.Fatalf("plan should be SAFE: %+v %v", plan, err)
	}
}

func newTestRouter() (*mux.Router, *Service) {
	svc := NewService()
	r := mux.NewRouter()
	RegisterEventGovRoutes(r, svc)
	return r, svc
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

func TestHTTPReplayFlow(t *testing.T) {
	r, _ := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/event-gov/capture", map[string]interface{}{
		"scenario": "checkout", "type": "payment.created", "payload_hash": "h1", "schema_version": 1,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("capture status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/event-gov/capture", map[string]interface{}{
		"scenario": "checkout", "type": "fraud.checked", "payload_hash": "h2",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("capture status = %d (%s)", rec.Code, rec.Body.String())
	}
	// Bad capture → 400.
	rec = doRequest(t, r, "POST", "/v1/event-gov/capture", map[string]interface{}{"scenario": "checkout"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad capture status = %d, want 400", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/event-gov/runs", map[string]interface{}{"scenario": "checkout", "multiplier": 10})
	if rec.Code != http.StatusCreated {
		t.Fatalf("start run status = %d (%s)", rec.Code, rec.Body.String())
	}
	var run shared.Run
	if err := json.NewDecoder(rec.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	if run.Delivered != 20 {
		t.Fatalf("delivered = %d, want 20", run.Delivered)
	}
	rec = doRequest(t, r, "GET", "/v1/event-gov/runs/"+run.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get run status = %d", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/event-gov/runs/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing run status = %d, want 404", rec.Code)
	}
	// Second run for diffing.
	_ = doRequest(t, r, "POST", "/v1/event-gov/capture", map[string]interface{}{
		"scenario": "canary", "type": "payment.created", "payload_hash": "h3",
	})
	rec2 := doRequest(t, r, "POST", "/v1/event-gov/runs", map[string]interface{}{"scenario": "canary", "multiplier": 1})
	var run2 shared.Run
	_ = json.NewDecoder(rec2.Body).Decode(&run2)
	rec = doRequest(t, r, "GET", "/v1/event-gov/runs/diff?base="+run.ID+"&candidate="+run2.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("diff status = %d (%s)", rec.Code, rec.Body.String())
	}
	var diff shared.RunDiff
	if err := json.NewDecoder(rec.Body).Decode(&diff); err != nil {
		t.Fatal(err)
	}
	if len(diff.Removed) == 0 {
		t.Fatalf("expected removals in diff: %+v", diff)
	}
	rec = doRequest(t, r, "GET", "/v1/event-gov/runs/diff?base="+run.ID, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("diff without candidate = %d, want 400", rec.Code)
	}
}

func TestHTTPSchemaFlow(t *testing.T) {
	r, _ := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/event-gov/schemas", map[string]interface{}{
		"topic": "payments", "version": 1,
		"fields": []map[string]interface{}{{"name": "id", "type": "string", "required": true}},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/event-gov/schemas", map[string]interface{}{
		"topic": "payments", "version": 1,
		"fields": []map[string]interface{}{{"name": "id", "type": "string", "required": true}},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/event-gov/schemas", map[string]interface{}{
		"topic": "payments", "version": 2,
		"fields": []map[string]interface{}{
			{"name": "id", "type": "string", "required": true},
			{"name": "note", "type": "string", "required": false},
		},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register v2 = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/event-gov/schemas/check", map[string]interface{}{
		"topic": "payments", "old_version": 1, "new_version": 2, "mode": "BACKWARD",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("check = %d (%s)", rec.Code, rec.Body.String())
	}
	var cres shared.CompatibilityResult
	if err := json.NewDecoder(rec.Body).Decode(&cres); err != nil {
		t.Fatal(err)
	}
	if !cres.Compatible {
		t.Fatalf("should be compatible: %+v", cres)
	}
	rec = doRequest(t, r, "POST", "/v1/event-gov/schemas/deprecate", map[string]interface{}{
		"topic": "payments", "version": 2, "field": "note",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("deprecate = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/event-gov/schemas/migration-plan?topic=payments&from=1&to=2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("migration-plan = %d (%s)", rec.Code, rec.Body.String())
	}
	var plan shared.MigrationPlan
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Safe || plan.Verdict != "SAFE" {
		t.Fatalf("plan should be SAFE: %+v", plan)
	}
	rec = doRequest(t, r, "GET", "/v1/event-gov/schemas/migration-plan?topic=payments&from=1", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad plan query = %d, want 400", rec.Code)
	}
}
