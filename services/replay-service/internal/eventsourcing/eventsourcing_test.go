package eventsourcing

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
	RegisterEventStoreRoutes(r, NewService(zerolog.Nop()))
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

func appendEvent(t *testing.T, r *mux.Router, agg string, seq int, typ, payload, at string) {
	t.Helper()
	rec := doRequest(t, r, "POST", "/v1/event-store/append", map[string]interface{}{
		"aggregate_id": agg, "expected_seq": seq, "type": typ,
		"payload": json.RawMessage(payload), "at": at,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("append %s seq %d = %d (%s)", typ, seq, rec.Code, rec.Body.String())
	}
}

func TestHTTPAppendProjectConflict(t *testing.T) {
	r := newTestRouter()
	base := "2026-05-01T10:00:00Z"
	appendEvent(t, r, "cust-1", 0, "CustomerCreated", `{"initial_balance":10000}`, base)
	appendEvent(t, r, "cust-1", 1, "CardIssued", `{"card_id":"c1","last4":"1111"}`, base)

	rec := doRequest(t, r, "POST", "/v1/event-store/append", map[string]interface{}{
		"aggregate_id": "cust-1", "expected_seq": 1, "type": "PaymentMade",
		"payload": json.RawMessage(`{"amount":100}`),
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale append = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/event-store/streams/cust-1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("load = %d (%s)", rec.Code, rec.Body.String())
	}
	var stream []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&stream); err != nil || len(stream) != 2 {
		t.Fatalf("stream = %v (%s)", stream, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/event-store/streams/cust-1/project", map[string]int{"to_seq": 1})
	if rec.Code != http.StatusOK {
		t.Fatalf("project = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/event-store/streams/missing", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing stream = %d, want 404", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/event-store/projectors", map[string]interface{}{
		"name": "custom", "event_types": []string{"PaymentMade"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register projector = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/event-store/projectors", map[string]interface{}{
		"name": "custom", "event_types": []string{"PaymentMade"},
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate projector = %d, want 409", rec.Code)
	}
}

func TestHTTPDebugReplay(t *testing.T) {
	r := newTestRouter()
	base := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	appendEvent(t, r, "cust-9", 0, "CustomerCreated", `{"initial_balance":10000}`, base.Format(time.RFC3339))
	appendEvent(t, r, "cust-9", 1, "PaymentMade", `{"amount":1000}`, base.Add(time.Hour).Format(time.RFC3339))
	appendEvent(t, r, "cust-9", 2, "PaymentMade", `{"amount":2000}`, base.Add(2*time.Hour).Format(time.RFC3339))

	rec := doRequest(t, r, "POST", "/v1/event-store/debug-replay", map[string]interface{}{
		"customer_id": "cust-9", "at": base.Add(time.Hour).Format(time.RFC3339),
		"rule_versions": map[string]string{"pricing": "v1", "fraud": "v2"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("debug-replay = %d (%s)", rec.Code, rec.Body.String())
	}
	var res struct {
		CursorSeq      int                    `json:"cursor_seq"`
		RulesHash      string                 `json:"rules_hash"`
		EventsReplayed int                    `json:"events_replayed"`
		State          map[string]interface{} `json:"state"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.CursorSeq != 2 || res.EventsReplayed != 2 || res.RulesHash == "" {
		t.Fatalf("replay = %+v", res)
	}
	if bal, _ := res.State["balance"].(float64); bal != 9000 {
		t.Fatalf("balance = %v, want 9000", res.State["balance"])
	}
	rec = doRequest(t, r, "POST", "/v1/event-store/debug-replay", map[string]interface{}{
		"customer_id": "cust-9", "at": base.Add(time.Hour).Format(time.RFC3339),
		"rule_versions": map[string]string{"pricing": "v9"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown rule = %d, want 400", rec.Code)
	}
}
