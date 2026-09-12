package rollout

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func testService() *Service {
	return NewService(zerolog.Nop())
}

func TestCreateAndAdvanceStages(t *testing.T) {
	s := testService()
	f, err := s.CreateFlag("new-checkout", Targeting{Country: "UK"}, Thresholds{MaxErrorRate: 5})
	if err != nil {
		t.Fatal(err)
	}
	if f.StageLabel != "10-users" {
		t.Fatalf("expected 10-users, got %s", f.StageLabel)
	}
	for _, want := range []string{"1%", "5%", "25%", "50%", "100%"} {
		f, err = s.Advance(f.ID)
		if err != nil {
			t.Fatal(err)
		}
		if f.StageLabel != want {
			t.Fatalf("expected %s, got %s", want, f.StageLabel)
		}
	}
	if _, err := s.Advance(f.ID); err == nil {
		t.Fatal("expected error advancing past 100%")
	}
	if len(f.Audit) != 6 {
		t.Fatalf("expected 6 audit entries, got %d", len(f.Audit))
	}
}

func TestTargetingRules(t *testing.T) {
	s := testService()
	f, err := s.CreateFlag("uk-only", Targeting{Country: "UK", AccountType: "personal", AppVersionGte: "2.1"}, Thresholds{})
	if err != nil {
		t.Fatal(err)
	}
	// Advance to 100% so cohort never blocks.
	for i := 0; i < 5; i++ {
		f, _ = s.Advance(f.ID)
	}
	ok, _, _ := s.Evaluate(f.ID, "user-1", map[string]string{"country": "US", "account_type": "personal", "app_version": "3.0"})
	if ok {
		t.Fatal("US user must not match UK targeting")
	}
	ok, _, _ = s.Evaluate(f.ID, "user-1", map[string]string{"country": "UK", "account_type": "personal", "app_version": "2.0"})
	if ok {
		t.Fatal("old app version must not match app_version_gte=2.1")
	}
	ok, _, _ = s.Evaluate(f.ID, "user-1", map[string]string{"country": "UK", "account_type": "personal", "app_version": "2.1"})
	if !ok {
		t.Fatal("matching user must be admitted at 100%")
	}
}

func TestAutoRollbackOnMetrics(t *testing.T) {
	s := testService()
	f, err := s.CreateFlag("risky", Targeting{}, Thresholds{MaxErrorRate: 5, MaxLatencyP99Ms: 2000, MaxPaymentFailureRate: 2})
	if err != nil {
		t.Fatal(err)
	}
	f, err = s.ReportMetrics(f.ID, Metrics{ErrorRate: 9, LatencyP99Ms: 100, PaymentFailureRate: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	if !f.RolledBack || f.Enabled {
		t.Fatalf("expected auto-rollback, got %+v", f)
	}
	if len(f.Audit) != 2 {
		t.Fatalf("expected audit of creation + rollback, got %d", len(f.Audit))
	}
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)

	raw, _ := json.Marshal(map[string]interface{}{
		"key":        "flag-1",
		"targeting":  map[string]string{"country": "UK"},
		"thresholds": map[string]float64{"max_error_rate": 5},
	})
	req := httptest.NewRequest("POST", "/v1/rollout/flags", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var created Flag
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// Duplicate → 409.
	req = httptest.NewRequest("POST", "/v1/rollout/flags", bytes.NewReader(raw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 duplicate, got %d", rec.Code)
	}

	req = httptest.NewRequest("POST", "/v1/rollout/flags/"+created.ID.String()+"/advance", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("advance: %d %s", rec.Code, rec.Body.String())
	}

	mraw, _ := json.Marshal(Metrics{ErrorRate: 50})
	req = httptest.NewRequest("POST", "/v1/rollout/flags/"+created.ID.String()+"/metrics", bytes.NewReader(mraw))
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics: %d", rec.Code)
	}
	var after Flag
	_ = json.NewDecoder(rec.Body).Decode(&after)
	if !after.RolledBack {
		t.Fatal("expected auto-rollback after breaching error rate")
	}

	req = httptest.NewRequest("GET", "/v1/rollout/flags/"+created.ID.String(), nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d", rec.Code)
	}

	req = httptest.NewRequest("GET", "/v1/rollout/flags/00000000-0000-0000-0000-000000000000", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
