package household

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	sharedhh "github.com/nexora/nexora/shared/household"
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

func addBill(t *testing.T, h *Handlers) string {
	t.Helper()
	rec := doRequest(h, "POST", "/v1/household/bills", map[string]interface{}{
		"provider": "Octopus", "amount_minor": 12000, "cadence": "MONTHLY",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add bill status = %d (%s)", rec.Code, rec.Body.String())
	}
	var b sharedhh.Bill
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatalf("decode bill: %v", err)
	}
	return b.ID
}

func TestHTTPBillsCompareAction(t *testing.T) {
	h := newTestHandlers()
	id := addBill(t, h)
	rec := doRequest(h, "POST", "/v1/household/bills/compare", map[string]interface{}{
		"bill_id": id,
		"market": []map[string]interface{}{
			{"provider": "Cheap", "amount_minor": 9000, "cadence": "MONTHLY"},
			{"provider": "Pricey", "amount_minor": 15000, "cadence": "MONTHLY"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("compare status = %d", rec.Code)
	}
	var cheaper []sharedhh.MarketPlan
	if err := json.NewDecoder(rec.Body).Decode(&cheaper); err != nil {
		t.Fatalf("decode compare: %v", err)
	}
	if len(cheaper) != 1 || cheaper[0].Provider != "Cheap" {
		t.Fatalf("cheaper wrong: %+v", cheaper)
	}
	for _, action := range []string{"cancel", "switch", "renew", "ignore"} {
		rec = doRequest(h, "POST", "/v1/household/bills/"+id+"/action", map[string]interface{}{"action": action})
		if rec.Code != http.StatusOK {
			t.Fatalf("action %s status = %d (%s)", action, rec.Code, rec.Body.String())
		}
	}
	rec = doRequest(h, "POST", "/v1/household/bills/"+id+"/action", map[string]interface{}{"action": "nope"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad action status = %d, want 400", rec.Code)
	}
	rec = doRequest(h, "POST", "/v1/household/bills/compare", map[string]interface{}{"bill_id": "missing", "market": []interface{}{}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing bill status = %d, want 404", rec.Code)
	}
}

func TestHTTPMandatesMigrateVerify(t *testing.T) {
	h := newTestHandlers()
	rec := doRequest(h, "POST", "/v1/household/mandates", map[string]interface{}{
		"merchant": "Netflix", "reference": "DD-1", "amount_minor": 1599,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mandate status = %d (%s)", rec.Code, rec.Body.String())
	}
	var m sharedhh.Mandate
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode mandate: %v", err)
	}
	rec = doRequest(h, "POST", "/v1/household/mandates/"+m.ID+"/migrate", map[string]interface{}{"target_account": "acct-new"})
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(h, "POST", "/v1/household/mandates/"+m.ID+"/verify-first-collection", map[string]interface{}{})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d (%s)", rec.Code, rec.Body.String())
	}
	var verified sharedhh.Mandate
	if err := json.NewDecoder(rec.Body).Decode(&verified); err != nil {
		t.Fatalf("decode verified: %v", err)
	}
	if !verified.FirstCollectionVerified {
		t.Fatalf("must be verified: %+v", verified)
	}
	rec = doRequest(h, "POST", "/v1/household/mandates/nope/migrate", map[string]interface{}{"target_account": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing mandate status = %d, want 404", rec.Code)
	}
}

func TestHTTPClosureScan(t *testing.T) {
	h := newTestHandlers()
	rec := doRequest(h, "POST", "/v1/household/closure/scan", map[string]interface{}{"account_id": "a-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("scan status = %d", rec.Code)
	}
	var safe sharedhh.ClosureResult
	if err := json.NewDecoder(rec.Body).Decode(&safe); err != nil {
		t.Fatalf("decode safe: %v", err)
	}
	if safe.Verdict != "SAFE" {
		t.Fatalf("empty must be SAFE: %+v", safe)
	}
	rec = doRequest(h, "POST", "/v1/household/closure/scan", map[string]interface{}{
		"account_id": "a-2", "direct_debits": 1, "balance_minor": 100,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("scan 2 status = %d", rec.Code)
	}
	var unsafe sharedhh.ClosureResult
	if err := json.NewDecoder(rec.Body).Decode(&unsafe); err != nil {
		t.Fatalf("decode unsafe: %v", err)
	}
	if unsafe.Verdict != "UNSAFE" || len(unsafe.Blockers) != 2 {
		t.Fatalf("must be UNSAFE with 2 blockers: %+v", unsafe)
	}
}
