package keysec

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
	RegisterKeysecRoutes(r, NewService(zerolog.Nop()))
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

func createActiveKey(t *testing.T, r *mux.Router, service, typ string) string {
	t.Helper()
	rec := doRequest(t, r, "POST", "/v1/keysec/keys", map[string]string{"service": service, "type": typ})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	var k map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &k); err != nil {
		t.Fatal(err)
	}
	id, _ := k["id"].(string)
	rec = doRequest(t, r, "POST", "/v1/keysec/keys/"+id+"/activate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate = %d (%s)", rec.Code, rec.Body.String())
	}
	return id
}

func TestHTTPFlow(t *testing.T) {
	r := newTestRouter()
	// 25. Lifecycle: create -> activate -> rotate -> revoke -> destroy.
	id := createActiveKey(t, r, "payments", "encryption")
	rec := doRequest(t, r, "GET", "/v1/keysec/services/payments/active-key", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(id)) {
		t.Fatalf("active = %d (%s)", rec.Code, rec.Body.String())
	}
	// Newer CREATED key must not hijack the active pointer.
	rec = doRequest(t, r, "POST", "/v1/keysec/keys", map[string]string{"service": "payments", "type": "encryption"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("second create = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/keysec/services/payments/active-key", nil)
	if !bytes.Contains(rec.Body.Bytes(), []byte(id)) {
		t.Fatalf("active must stay explicit, got %s", rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/keysec/keys/"+id+"/rotate", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate = %d (%s)", rec.Code, rec.Body.String())
	}
	var rotated map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	nextID, _ := rotated["id"].(string)
	rec = doRequest(t, r, "POST", "/v1/keysec/keys/"+id+"/rotate", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("rotate non-active = %d, want 409", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/keysec/keys/"+nextID+"/revoke", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/keysec/keys/"+nextID+"/destroy", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("destroy = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/keysec/services/missing/active-key", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing active = %d, want 404", rec.Code)
	}
	rec = doRequest(t, r, "POST", "/v1/keysec/keys", map[string]string{"service": "x", "type": "bogus"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type = %d, want 400", rec.Code)
	}
	// 26. Usage anomaly.
	kid := createActiveKey(t, r, "ledger", "signing")
	for i := 1; i <= 7; i++ {
		day := "2026-09-0" + string(rune('0'+i))
		rec = doRequest(t, r, "POST", "/v1/keysec/usage/observe", map[string]interface{}{
			"key_id": kid, "day": day, "count": 100,
			"service": "ledger", "region": "eu-west", "operation": "sign",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("observe = %d (%s)", rec.Code, rec.Body.String())
		}
	}
	rec = doRequest(t, r, "POST", "/v1/keysec/usage/observe", map[string]interface{}{
		"key_id": kid, "day": "2026-09-08", "count": 500,
		"service": "ledger", "region": "eu-west", "operation": "sign",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("spike observe = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "GET", "/v1/keysec/usage/anomalies", nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(kid)) {
		t.Fatalf("anomalies = %d (%s)", rec.Code, rec.Body.String())
	}
	// 27. Secret scanner.
	rec = doRequest(t, r, "POST", "/v1/keysec/scan", map[string]string{
		"text": "add key\n+ api_key = \"nxr-staged-key-9f2k7qz4tm8x1a\"\n",
	})
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("BLOCK")) {
		t.Fatalf("scan block = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, r, "POST", "/v1/keysec/scan", map[string]string{
		"text": "update example fixtures, no secrets",
	})
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("ALLOW")) {
		t.Fatalf("scan allow = %d (%s)", rec.Code, rec.Body.String())
	}
	// 28. Policy sim.
	rec = doRequest(t, r, "POST", "/v1/keysec/policy-sim/run", map[string]interface{}{
		"policy": map[string]interface{}{"name": "strict", "block_new_region": true, "challenge_new_device": true},
		"traffic": []map[string]interface{}{
			{}, {"new_device": true}, {"new_region": true},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("sim = %d (%s)", rec.Code, rec.Body.String())
	}
	var sim map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &sim); err != nil {
		t.Fatal(err)
	}
	if sim["blocked"] != float64(1) || sim["challenged"] != float64(1) || sim["allowed"] != float64(1) {
		t.Fatalf("sim counts: %+v", sim)
	}
}
