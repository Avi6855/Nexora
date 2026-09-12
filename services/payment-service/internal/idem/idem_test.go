package idem

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func testService() *Service {
	return NewService(time.Hour, zerolog.Nop())
}

func TestServiceExecuteAndReplay(t *testing.T) {
	s := testService()
	calls := 0
	resp, replayed, err := s.Execute("k1", "POST", "/v1/payments", []byte("b1"), func() ([]byte, error) {
		calls++
		return []byte("r1"), nil
	})
	if err != nil || replayed || string(resp) != "r1" {
		t.Fatalf("execute: %v %v %s", replayed, err, resp)
	}
	resp, replayed, err = s.Execute("k1", "POST", "/v1/payments", []byte("b1"), func() ([]byte, error) {
		calls++
		return []byte("r2"), nil
	})
	if err != nil || !replayed || string(resp) != "r1" {
		t.Fatalf("expected replay r1, got %v %v %s", replayed, err, resp)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func testRouter(s *Service) *mux.Router {
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)
	return r
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := testRouter(s)

	body := map[string]string{
		"key":             "pay-1",
		"method":          "POST",
		"path":            "/v1/payments",
		"body_base64":     base64.StdEncoding.EncodeToString([]byte(`{"amount":100}`)),
		"response_base64": base64.StdEncoding.EncodeToString([]byte(`{"id":"p1"}`)),
	}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/v1/idem/execute", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute: %d %s", rec.Code, rec.Body.String())
	}
	var first struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	if first.Replayed {
		t.Fatal("first execute must not be a replay")
	}

	// Replay the same key+body.
	req = httptest.NewRequest("POST", "/v1/idem/execute", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var second struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&second); err != nil {
		t.Fatal(err)
	}
	if !second.Replayed {
		t.Fatal("second execute must replay")
	}

	// Fetch the stored request.
	req = httptest.NewRequest("GET", "/v1/idem/requests/pay-1", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}

	// Unknown key → 404.
	req = httptest.NewRequest("GET", "/v1/idem/requests/missing", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}

	// Sweep.
	req = httptest.NewRequest("POST", "/v1/idem/sweep", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sweep: %d", rec.Code)
	}
}
