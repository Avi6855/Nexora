package taxpack

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func newTestRouter() *mux.Router {
	r := mux.NewRouter()
	RegisterTaxPackRoutes(r, NewService(zerolog.Nop()))
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
	rec := doRequest(t, r, "POST", "/v1/tax/entries", map[string]interface{}{
		"category": "INTEREST", "amount_minor": 10000, "date": "2024-06-01", "doc_id": "doc-1",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("entry = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/tax/entries", map[string]interface{}{
		"category": "BOGUS", "amount_minor": 100, "date": "2024-06-01",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad category = %d, want 400", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/tax/packs/build", map[string]int{"year": 2024})
	if rec.Code != http.StatusCreated {
		t.Fatalf("build = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/tax/packs/2024.json", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("json = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/tax/packs/2024.csv", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "INTEREST") {
		t.Fatalf("csv = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/tax/packs/2024/manifest", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("manifest = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/tax/packs/2024/finalize", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("finalize = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/tax/packs/2024/finalize", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("double finalize = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "GET", "/v1/tax/packs/2099.json", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing pack = %d, want 404", rec.Code)
	}
}
