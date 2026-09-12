package multicurrency

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

func mustNow() time.Time { return time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC) }

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

func snapshotFor(t *testing.T, h *Handlers) string {
	t.Helper()
	rec := doRequest(h, "POST", "/v1/multi-currency/fx/snapshots", map[string]interface{}{
		"rates": map[string]float64{"EURGBP": 0.85, "USDGBP": 0.79, "GBPGBP": 1},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("snapshot status = %d (%s)", rec.Code, rec.Body.String())
	}
	var out map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if out["snapshot_id"] == "" {
		t.Fatalf("missing snapshot_id: %v", out)
	}
	return out["snapshot_id"]
}

func TestHTTPCreditDebitConvertValuation(t *testing.T) {
	h := newTestHandlers()
	rec := doRequest(h, "POST", "/v1/multi-currency/balances/credit", map[string]interface{}{
		"account": "mc-http-1", "currency": "EUR", "amount_minor": 10000,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("credit status = %d (%s)", rec.Code, rec.Body.String())
	}
	snap := snapshotFor(t, h)
	rec = doRequest(h, "POST", "/v1/multi-currency/convert", map[string]interface{}{
		"account": "mc-http-1", "from": "EUR", "to": "GBP",
		"amount_minor": 10000, "snapshot_id": snap, "spread_bps": 100,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("convert status = %d (%s)", rec.Code, rec.Body.String())
	}
	var conv map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&conv); err != nil {
		t.Fatalf("decode convert: %v", err)
	}
	if conv["converted_minor"] != float64(8415) {
		t.Fatalf("converted = %v, want 8415", conv["converted_minor"])
	}
	rec = doRequest(h, "GET", "/v1/multi-currency/valuation?account=mc-http-1&base=GBP&snapshot_id="+snap, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("valuation status = %d (%s)", rec.Code, rec.Body.String())
	}
	// Overdraft → 409.
	rec = doRequest(h, "POST", "/v1/multi-currency/balances/debit", map[string]interface{}{
		"account": "mc-http-1", "currency": "EUR", "amount_minor": 99999,
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("overdraft status = %d, want 409", rec.Code)
	}
	// Unknown snapshot → 404.
	rec = doRequest(h, "GET", "/v1/multi-currency/valuation?account=mc-http-1&base=GBP&snapshot_id=nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown snapshot status = %d, want 404", rec.Code)
	}
	// Missing params → 400.
	rec = doRequest(h, "GET", "/v1/multi-currency/valuation?account=mc-http-1&base=GBP", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing snapshot status = %d, want 400", rec.Code)
	}
}

func TestServiceSpreadAndPinnedValuation(t *testing.T) {
	svc := NewService(zerolog.Nop())
	if err := svc.Credit("mc-svc-1", "EUR", 1000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := svc.Credit("mc-svc-1", "GBP", 1000); err != nil {
		t.Fatalf("credit gbp: %v", err)
	}
	snap, err := svc.SnapshotRates(map[string]float64{"EURGBP": 0.85, "GBPGBP": 1}, mustNow())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	v1, err := svc.ValuateTotal("mc-svc-1", "GBP", snap)
	if err != nil {
		t.Fatalf("valuate: %v", err)
	}
	if v1 != 1850 {
		t.Fatalf("valuation = %d, want 1850", v1)
	}
	snap2, err := svc.SnapshotRates(map[string]float64{"EURGBP": 0.5, "GBPGBP": 1}, mustNow())
	if err != nil {
		t.Fatalf("snapshot 2: %v", err)
	}
	again, err := svc.ValuateTotal("mc-svc-1", "GBP", snap)
	if err != nil {
		t.Fatalf("valuate pinned: %v", err)
	}
	if again != v1 {
		t.Fatalf("pinned valuation moved: %d vs %d", again, v1)
	}
	v2, err := svc.ValuateTotal("mc-svc-1", "GBP", snap2)
	if err != nil {
		t.Fatalf("valuate 2: %v", err)
	}
	if v2 != 1500 {
		t.Fatalf("new valuation = %d, want 1500", v2)
	}
	if got := len(svc.Audit("mc-svc-1")); got != 0 {
		t.Fatalf("audit without conversions = %d, want 0", got)
	}
}
