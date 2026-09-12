package dataline

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	shared "github.com/nexora/nexora/shared/dataline"
)

func TestServiceLineage(t *testing.T) {
	svc := NewService()
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	if _, err := svc.RecordNode(shared.Node{ID: "src", Kind: "SOURCE", ValueID: "v", At: at}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if _, err := svc.RecordNode(shared.Node{ID: "ui", Kind: "UI", ValueID: "v", At: at.Add(time.Hour)}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if err := svc.RecordEdge("src", "ui"); err != nil {
		t.Fatalf("RecordEdge: %v", err)
	}
	chain, err := svc.Explain("v")
	if err != nil || len(chain) != 2 || chain[0].ID != "src" {
		t.Fatalf("Explain = %+v %v", chain, err)
	}
	if _, err := svc.Explain("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestServicePrivacy(t *testing.T) {
	svc := NewService()
	tok, err := svc.Tokenise("fraud", "name", "Ada")
	if err != nil || tok == "" {
		t.Fatalf("Tokenise: %q %v", tok, err)
	}
	n, err := svc.CohortCount("fraud", "")
	if err != nil || n != 1 {
		t.Fatalf("CohortCount = %d (%v)", n, err)
	}
	if err := svc.RotatePurpose("fraud"); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	n, _ = svc.CohortCount("fraud", "")
	if n != 0 {
		t.Fatalf("post-rotate count = %d, want 0", n)
	}
	if len(svc.AuditLog()) == 0 {
		t.Fatal("audit log should not be empty")
	}
}

func TestServiceRetention(t *testing.T) {
	svc := NewService()
	if err := svc.PutPolicy(shared.Policy{MetadataYears: 7, TelemetryDays: 30, EvidenceYears: 7, AttachmentsDays: 90}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	if _, err := svc.Classify("t1", "telemetry", now.Add(-31*24*time.Hour)); err != nil {
		t.Fatalf("Classify: %v", err)
	}
	due, err := svc.DueTransitions(now)
	if err != nil || len(due) != 1 {
		t.Fatalf("Due = %+v %v", due, err)
	}
	h, err := svc.PlaceHold("t1", "legal")
	if err != nil {
		t.Fatalf("PlaceHold: %v", err)
	}
	if _, err := svc.ApplyTransition("t1", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("held apply should conflict, got %v", err)
	}
	if err := svc.ReleaseHold(h.ID); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	if _, err := svc.ApplyTransition("t1", now); err != nil {
		t.Fatalf("ApplyTransition: %v", err)
	}
}

func newTestRouter() *mux.Router {
	r := mux.NewRouter()
	RegisterDataLineRoutes(r, NewService())
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

func TestHTTPLineageFlow(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/data-lineage/nodes", map[string]string{"id": "src", "kind": "SOURCE", "value_id": "v1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("node = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/data-lineage/nodes", map[string]string{"id": "api", "kind": "API", "value_id": "v1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("node = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/data-lineage/nodes", map[string]string{"id": "src", "kind": "SOURCE", "value_id": "v1"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate node = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/data-lineage/edges", map[string]string{"from": "src", "to": "api"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("edge = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/data-lineage/explain?value=v1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("explain = %d (%s)", rec.Code, rec.Body.String())
	}
	var chain []shared.Node
	if err := json.NewDecoder(rec.Body).Decode(&chain); err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("chain len = %d, want 2", len(chain))
	}
	rec = doRequest(t, r, "GET", "/v1/data-lineage/explain?value=missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing explain = %d, want 404", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/data-lineage/explain", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no value = %d, want 400", rec.Code)
	}
}

func TestHTTPPrivacyFlow(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/privacy/tokenise", map[string]string{"purpose": "fraud", "field": "name", "value": "Ada"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("tokenise = %d (%s)", rec.Code, rec.Body.String())
	}
	var tokResp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&tokResp); err != nil || tokResp["token"] == "" {
		t.Fatalf("bad token response: %v %s", err, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/privacy/tokenise", map[string]string{"purpose": "fraud", "field": "email", "value": "a@b.c"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad field = %d, want 400", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/privacy/cohorts/count", map[string]string{"purpose": "fraud"})
	if rec.Code != http.StatusOK {
		t.Fatalf("cohort = %d (%s)", rec.Code, rec.Body.String())
	}
	var count map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&count); err != nil {
		t.Fatal(err)
	}
	if count["count"].(float64) != 1 {
		t.Fatalf("count = %v, want 1", count)
	}
	rec = doRequest(t, r, "GET", "/v1/privacy/audit", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit = %d", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/privacy/rotate", map[string]string{"purpose": "fraud"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/privacy/cohorts/count", map[string]string{"purpose": "fraud"})
	var after map[string]interface{}
	_ = json.NewDecoder(rec.Body).Decode(&after)
	if after["count"].(float64) != 0 {
		t.Fatalf("post-rotate count = %v, want 0", after)
	}
}

func TestHTTPRetentionFlow(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, "PUT", "/v1/retention/policies", map[string]int{
		"metadata_years": 7, "telemetry_days": 30, "evidence_years": 7, "attachments_days": 90,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("policy = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "PUT", "/v1/retention/policies", map[string]int{"metadata_years": 0})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad policy = %d, want 400", rec.Code)
	}
	old := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC).Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	rec = doRequest(t, r, "POST", "/v1/retention/classify", map[string]string{"id": "t1", "category": "telemetry", "created_at": old})
	if rec.Code != http.StatusCreated {
		t.Fatalf("classify = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/retention/classify", map[string]string{"id": "t1", "category": "telemetry"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate classify = %d, want 409", rec.Code)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	rec = doRequest(t, r, "GET", "/v1/retention/due?now="+now, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("due = %d (%s)", rec.Code, rec.Body.String())
	}
	var due []shared.DueTransition
	if err := json.NewDecoder(rec.Body).Decode(&due); err != nil || len(due) != 1 {
		t.Fatalf("due = %+v %v", due, err)
	}
	rec = doRequest(t, r, "POST", "/v1/retention/holds", map[string]string{"item_id": "t1", "reason": "FCA inquiry"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("hold = %d (%s)", rec.Code, rec.Body.String())
	}
	var hold shared.Hold
	if err := json.NewDecoder(rec.Body).Decode(&hold); err != nil || hold.ID == "" {
		t.Fatalf("bad hold: %v", err)
	}
	rec = doRequest(t, r, "POST", "/v1/retention/transitions", map[string]string{"id": "t1", "now": now})
	if rec.Code != http.StatusConflict {
		t.Fatalf("held transition = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "DELETE", "/v1/retention/holds/"+hold.ID, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("release = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/retention/transitions", map[string]string{"id": "t1", "now": now})
	if rec.Code != http.StatusOK {
		t.Fatalf("transition = %d (%s)", rec.Code, rec.Body.String())
	}
}
