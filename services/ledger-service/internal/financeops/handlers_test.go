package financeops

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func testRouter() *mux.Router {
	svc := NewService(zerolog.Nop())
	h := NewHandlers(svc, zerolog.Nop())
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	return router
}

func doRequest(t *testing.T, router *mux.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestRoutesAdjustments(t *testing.T) {
	router := testRouter()
	rec := doRequest(t, router, "POST", "/v1/finance-ops/adjustments",
		`{"debit_account":"expense:ops","credit_account":"cash","amount":5000,"period":"2026-09","reason":"fix centre"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	var adj map[string]interface{}
	if err := json.Unmarshal(created["adjustment"], &adj); err != nil {
		t.Fatal(err)
	}
	id := adj["id"].(string)

	rec = doRequest(t, router, "POST", "/v1/finance-ops/adjustments/"+id+"/approve", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("approve status %d: %s", rec.Code, rec.Body.String())
	}
	var approved map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved["balanced"] != true {
		t.Fatalf("must verify balanced: %v", approved)
	}
	rec = doRequest(t, router, "GET", "/v1/finance-ops/adjustments/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status %d", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/finance-ops/adjustments/"+id+"/approve", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("double approve status %d, want 409", rec.Code)
	}
	if rec := doRequest(t, router, "GET", "/v1/finance-ops/adjustments/missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown get status %d, want 404", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/finance-ops/adjustments", `{"reason":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad create status %d, want 400", rec.Code)
	}
}

func TestRoutesBackdatedClosePeriods(t *testing.T) {
	router := testRouter()

	rec := doRequest(t, router, "POST", "/v1/finance-ops/backdated-events",
		`{"account":"a1","amount":1000,"effective_at":"2026-09-01T12:00:00Z"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("backdated status %d: %s", rec.Code, rec.Body.String())
	}
	var ev map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&ev); err != nil {
		t.Fatal(err)
	}
	if ev["effective_at"] == nil || ev["recorded_at"] == nil || ev["processed_at"] == nil {
		t.Fatalf("triple must be present: %v", ev)
	}
	rec = doRequest(t, router, "GET", "/v1/finance-ops/balances/as-of?account=a1&as_of=2026-09-05T00:00:00Z", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("as-of status %d", rec.Code)
	}
	var bal map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&bal); err != nil {
		t.Fatal(err)
	}
	if bal["balance"] != float64(1000) {
		t.Fatalf("as-of balance must be 1000: %v", bal)
	}
	if rec := doRequest(t, router, "GET", "/v1/finance-ops/balances/as-of?account=a1", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing as_of status %d, want 400", rec.Code)
	}

	rec = doRequest(t, router, "POST", "/v1/finance-ops/close/check",
		`{"transactions_complete":true,"settlements_complete":true,"no_open_exceptions":true,"recon_clean":true}`)
	var closeRes map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&closeRes); err != nil {
		t.Fatal(err)
	}
	if closeRes["verdict"] != "CLOSE" {
		t.Fatalf("must CLOSE: %v", closeRes)
	}
	rec = doRequest(t, router, "POST", "/v1/finance-ops/close/check", `{"transactions_complete":true}`)
	if err := json.NewDecoder(rec.Body).Decode(&closeRes); err != nil {
		t.Fatal(err)
	}
	if closeRes["verdict"] != "BLOCK" {
		t.Fatalf("must BLOCK: %v", closeRes)
	}

	if rec := doRequest(t, router, "POST", "/v1/finance-ops/periods/lock", `{"period":"2026-09"}`); rec.Code != http.StatusOK {
		t.Fatalf("lock status %d", rec.Code)
	}
	rec = doRequest(t, router, "POST", "/v1/finance-ops/periods/corrections",
		`{"period":"2026-09","debit_account":"expense:ops","credit_account":"cash","amount":250}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("correction status %d: %s", rec.Code, rec.Body.String())
	}
	var corr map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&corr); err != nil {
		t.Fatal(err)
	}
	if corr["redirected"] != true || corr["actual_period"] != "2026-10" {
		t.Fatalf("locked correction must redirect to 2026-10: %v", corr)
	}
}

func TestRoutesPostingRulesSubledgers(t *testing.T) {
	router := testRouter()

	rec := doRequest(t, router, "PUT", "/v1/finance-ops/posting-rules",
		`{"type":"card_settlement","debit_account":"clearing:card","credit_account":"cash"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put status %d: %s", rec.Code, rec.Body.String())
	}
	var r1 map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&r1); err != nil {
		t.Fatal(err)
	}
	if r1["version"] != float64(1) {
		t.Fatalf("v1 wrong: %v", r1)
	}
	rec = doRequest(t, router, "PUT", "/v1/finance-ops/posting-rules",
		`{"type":"card_settlement","debit_account":"clearing:v2","credit_account":"cash"}`)
	var r2 map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&r2); err != nil {
		t.Fatal(err)
	}
	if r2["version"] != float64(2) {
		t.Fatalf("v2 wrong: %v", r2)
	}
	rec = doRequest(t, router, "GET", "/v1/finance-ops/posting-rules/resolve?type=card_settlement&version=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status %d", rec.Code)
	}
	var resolved map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved["debit_account"] != "clearing:card" {
		t.Fatalf("resolve v1 wrong: %v", resolved)
	}
	rec = doRequest(t, router, "POST", "/v1/finance-ops/posting-rules/card_settlement/rollback", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, router, "POST", "/v1/finance-ops/posting-rules/card_settlement/rollback", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("double rollback status %d, want 409", rec.Code)
	}
	if rec := doRequest(t, router, "GET", "/v1/finance-ops/posting-rules/resolve?type=missing", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown resolve status %d, want 404", rec.Code)
	}

	rec = doRequest(t, router, "POST", "/v1/finance-ops/subledgers/post",
		`{"domain":"card","debit_account":"clearing:card","credit_account":"cash","amount":1000}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("subledger post status %d: %s", rec.Code, rec.Body.String())
	}
	doRequest(t, router, "POST", "/v1/finance-ops/subledgers/post",
		`{"domain":"loans","debit_account":"loans:recv","credit_account":"cash","amount":2000}`)
	rec = doRequest(t, router, "GET", "/v1/finance-ops/subledgers/trial?domain=card", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("trial status %d", rec.Code)
	}
	var trial map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&trial); err != nil {
		t.Fatal(err)
	}
	if trial["debits"] != float64(1000) || trial["balanced"] != true {
		t.Fatalf("card trial wrong: %v", trial)
	}
	rec = doRequest(t, router, "GET", "/v1/finance-ops/consolidated", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("consolidated status %d", rec.Code)
	}
	var view map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view["debits"] != float64(3000) || view["balanced"] != true {
		t.Fatalf("consolidated wrong: %v", view)
	}
	if rec := doRequest(t, router, "POST", "/v1/finance-ops/subledgers/post",
		`{"domain":"nope","debit_account":"a","credit_account":"b","amount":10}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad domain status %d, want 400", rec.Code)
	}
}
