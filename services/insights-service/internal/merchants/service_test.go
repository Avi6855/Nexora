package merchants

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	sharedm "github.com/nexora/nexora/shared/merchants"
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

func observe(t *testing.T, h *Handlers, name string, refunded bool) {
	t.Helper()
	rec := doRequest(h, "POST", "/v1/merchants/observe", map[string]interface{}{
		"name": name, "location": "London", "amount_minor": 1000, "refunded": refunded,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("observe %q status = %d (%s)", name, rec.Code, rec.Body.String())
	}
}

func TestHTTPAMZNVariantsOneNode(t *testing.T) {
	h := newTestHandlers()
	for _, v := range []string{"AMZN MKTP UK", "AMAZON.CO.UK", "Amzn Prime"} {
		observe(t, h, v, false)
	}
	rec := doRequest(h, "GET", "/v1/merchants/resolve?name=AMZN", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d (%s)", rec.Code, rec.Body.String())
	}
	var n sharedm.Node
	if err := json.NewDecoder(rec.Body).Decode(&n); err != nil {
		t.Fatalf("decode resolve: %v", err)
	}
	if n.Canonical != "AMAZON" {
		t.Fatalf("canonical = %s, want AMAZON", n.Canonical)
	}
	rec = doRequest(h, "GET", "/v1/merchants/resolve?name=missing-merchant-xyz", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing resolve status = %d, want 404", rec.Code)
	}
	rec = doRequest(h, "GET", "/v1/merchants/resolve", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty resolve status = %d, want 400", rec.Code)
	}
}

func TestHTTPAliasRiskRefundsSubscriptions(t *testing.T) {
	h := newTestHandlers()
	observe(t, h, "Netflix", false)
	observe(t, h, "NETFLIX UK", true)
	observe(t, h, "Netflix", false)
	observe(t, h, "Netflix", true)

	rec := doRequest(h, "POST", "/v1/merchants/aliases", map[string]interface{}{
		"merchant_id": "NETFLIX", "alias": "NFLX",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("alias status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(h, "POST", "/v1/merchants/risk-flags", map[string]interface{}{
		"merchant_id": "NETFLIX", "flag": "HIGH_REFUND_RATE",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("risk status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(h, "GET", "/v1/merchants/NETFLIX/refunds", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refunds status = %d (%s)", rec.Code, rec.Body.String())
	}
	var stats sharedm.RefundStats
	if err := json.NewDecoder(rec.Body).Decode(&stats); err != nil {
		t.Fatalf("decode refunds: %v", err)
	}
	if stats.Total != 4 || stats.Refunded != 2 || stats.RefundRate != 0.5 {
		t.Fatalf("refund stats wrong: %+v", stats)
	}
	rec = doRequest(h, "GET", "/v1/merchants/subscriptions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscriptions status = %d", rec.Code)
	}
	var subs []sharedm.Subscription
	if err := json.NewDecoder(rec.Body).Decode(&subs); err != nil {
		t.Fatalf("decode subs: %v", err)
	}
	if len(subs) != 1 || subs[0].Canonical != "NETFLIX" {
		t.Fatalf("subscriptions wrong: %+v", subs)
	}
	rec = doRequest(h, "GET", "/v1/merchants/NOPE/refunds", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing refunds status = %d, want 404", rec.Code)
	}
}
