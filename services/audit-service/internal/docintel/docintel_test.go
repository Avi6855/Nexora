package docintel

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
	RegisterDocIntelRoutes(r, NewService(zerolog.Nop()))
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

func TestHTTPIngestClassifySearchTotals(t *testing.T) {
	r := newTestRouter()
	rec := doRequest(t, r, "POST", "/v1/documents/ingest", map[string]string{
		"kind": "STATEMENT", "filename": "halifax.txt",
		"content_text": "Merchant: Halifax\nMortgage interest statement interest earned £500.00 on 2024-06-12",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("ingest = %d (%s)", rec.Code, rec.Body.String())
	}
	var doc map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&doc); err != nil || doc["id"] == nil {
		t.Fatalf("bad ingest response: %v %s", err, rec.Body.String())
	}
	id := doc["id"].(string)

	rec = doRequest(t, r, "GET", "/v1/documents/"+id+"/classification", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("classification = %d (%s)", rec.Code, rec.Body.String())
	}
	var class map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&class); err != nil || class["kind"] != "INTEREST" {
		t.Fatalf("classification = %v %s", class, rec.Body.String())
	}

	rec = doRequest(t, r, "POST", "/v1/documents/search", map[string]interface{}{
		"text": "mortgage interest", "min_amount_minor": 10000,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("search = %d (%s)", rec.Code, rec.Body.String())
	}
	var res []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil || len(res) != 1 {
		t.Fatalf("search results = %v (%s)", res, rec.Body.String())
	}

	rec = doRequest(t, r, "GET", "/v1/documents/totals", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("totals = %d", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/documents/totals?kind=INTEREST", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("totals kind = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/documents/missing/classification", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing classification = %d, want 404", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/documents/ingest", map[string]string{"kind": "BOGUS", "filename": "x", "content_text": "hi"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind = %d, want 400", rec.Code)
	}
}
