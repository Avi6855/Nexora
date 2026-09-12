package fxtrack

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

func newTestHandlers() *Handlers {
	return NewHandlers(NewService(zerolog.Nop()), zerolog.Nop())
}

func doRequest(h *Handlers, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)
	return rec
}

func TestHTTPStartAdvanceETATimeline(t *testing.T) {
	h := newTestHandlers()
	rec := doRequest(h, "POST", "/v1/fx/transfers", map[string]interface{}{
		"id": "fx-http-1", "corridor": "GBP-EUR", "amount_minor": 12000,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("start status = %d (%s)", rec.Code, rec.Body.String())
	}
	// Duplicate ID → 409.
	rec = doRequest(h, "POST", "/v1/fx/transfers", map[string]interface{}{
		"id": "fx-http-1", "corridor": "GBP-EUR", "amount_minor": 12000,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}
	// Missing corridor → 400.
	rec = doRequest(h, "POST", "/v1/fx/transfers", map[string]interface{}{"id": "fx-bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad start status = %d, want 400", rec.Code)
	}
	rec = doRequest(h, "POST", "/v1/fx/transfers/fx-http-1/advance", map[string]interface{}{})
	if rec.Code != http.StatusOK {
		t.Fatalf("advance status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(h, "GET", "/v1/fx/transfers/fx-http-1/eta", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("eta status = %d (%s)", rec.Code, rec.Body.String())
	}
	var eta map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&eta); err != nil {
		t.Fatalf("decode eta: %v", err)
	}
	if eta["earliest"] == nil || eta["latest"] == nil || eta["delay"] == nil {
		t.Fatalf("eta missing fields: %v", eta)
	}
	rec = doRequest(h, "GET", "/v1/fx/transfers/fx-http-1/timeline", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status = %d", rec.Code)
	}
	var tl []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&tl); err != nil {
		t.Fatalf("decode timeline: %v", err)
	}
	if len(tl) != 2 {
		t.Fatalf("timeline len = %d, want 2", len(tl))
	}
	// Unknown transfer → 404.
	rec = doRequest(h, "GET", "/v1/fx/transfers/nope/timeline", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown timeline status = %d, want 404", rec.Code)
	}
	rec = doRequest(h, "GET", "/v1/fx/transfers/nope/eta", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown eta status = %d, want 404", rec.Code)
	}
}

func TestServiceTimeoutEscalation(t *testing.T) {
	svc := NewService(zerolog.Nop())
	if _, err := svc.StartTransfer("fx-svc-1", "GBP-USD", 500, mustTime(t, "2026-05-01T09:00:00Z")); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	if _, err := svc.Advance("fx-svc-1", mustTime(t, "2026-05-01T09:01:00Z")); err != nil {
		t.Fatalf("advance failed: %v", err)
	}
	info, err := svc.DelayedAt("fx-svc-1", mustTime(t, "2026-05-01T14:00:00Z"))
	if err != nil {
		t.Fatalf("delayedAt failed: %v", err)
	}
	if !info.Delayed || !info.Escalated {
		t.Fatalf("expected delayed+escalated, got %+v", info)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}
