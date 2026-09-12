package shed

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/shedding"
)

func testService() *Service {
	return NewService(shared.DefaultAdaptiveConfig(), zerolog.Nop())
}

func TestServiceEscalationAndRecovery(t *testing.T) {
	s := testService()
	if got := s.Tier(); got != shared.TierNormal {
		t.Fatalf("expected NORMAL, got %s", got)
	}
	s.Observe(shared.Sample{LatencyP99Ms: 1500})
	if got := s.Tier(); got != shared.TierCritical {
		t.Fatalf("expected CRITICAL, got %s", got)
	}
	d := s.Decide(shared.ClassAnalytics, shared.Sample{LatencyP99Ms: 1500})
	if d.Action != shared.ActionDrop {
		t.Fatalf("expected analytics drop, got %s", d.Action)
	}
	d = s.Decide(shared.ClassCritical, shared.Sample{LatencyP99Ms: 1500})
	if d.Action != shared.ActionAllow {
		t.Fatalf("expected critical allow, got %s", d.Action)
	}
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)

	post := func(path string, body interface{}) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := post("/v1/shed/samples", shared.Sample{LatencyP99Ms: 1500})
	if rec.Code != http.StatusOK {
		t.Fatalf("samples: %d %s", rec.Code, rec.Body.String())
	}
	rec = post("/v1/shed/decide", map[string]interface{}{
		"class":  "analytics",
		"sample": shared.Sample{LatencyP99Ms: 1500},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("decide: %d %s", rec.Code, rec.Body.String())
	}
	var d shared.Decision
	if err := json.NewDecoder(rec.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Action != shared.ActionDrop {
		t.Fatalf("expected drop, got %s", d.Action)
	}

	req := httptest.NewRequest("GET", "/v1/shed/tier", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("tier: %d", rec.Code)
	}

	rec = post("/v1/shed/policy", shared.AdaptiveConfig{QueueCap: 5})
	if rec.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", rec.Code, rec.Body.String())
	}
	var cfg shared.AdaptiveConfig
	if err := json.NewDecoder(rec.Body).Decode(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.QueueCap != 5 {
		t.Fatalf("expected queue cap 5, got %d", cfg.QueueCap)
	}
}
