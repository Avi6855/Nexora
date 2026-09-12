package moneymove

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

func testRouter() (*Service, *mux.Router) {
	svc := NewService(zerolog.Nop())
	h := NewHandlers(svc, zerolog.Nop())
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	return svc, router
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

func TestRoutesIntentsAndDivergence(t *testing.T) {
	_, router := testRouter()
	rec := doRequest(t, router, "POST", "/v1/money-move/intents",
		`{"customer_statement":"pay landlord","amount":50000,"payee":"landlord","steps":["debit:a","credit:landlord"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("record status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	intent := created["intent"].(map[string]interface{})
	id := intent["id"].(string)

	// Matching execution: not diverged.
	rec = doRequest(t, router, "POST", "/v1/money-move/intents/"+id+"/execute", `{"legs":["debit:a","credit:landlord"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute status %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, router, "GET", "/v1/money-move/intents/"+id+"/divergence", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("divergence status %d", rec.Code)
	}
	var div map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&div); err != nil {
		t.Fatal(err)
	}
	if div["diverged"] != false {
		t.Fatalf("must not diverge: %v", div)
	}

	// Diverging execution.
	rec = doRequest(t, router, "POST", "/v1/money-move/intents/"+id+"/execute", `{"legs":["debit:a","credit:stranger"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("diverging execute status %d", rec.Code)
	}
	rec = doRequest(t, router, "GET", "/v1/money-move/intents/"+id+"/divergence", "")
	var div2 map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&div2); err != nil {
		t.Fatal(err)
	}
	if div2["diverged"] != true || div2["reason"] == "" {
		t.Fatalf("must diverge with reason: %v", div2)
	}

	if rec := doRequest(t, router, "GET", "/v1/money-move/intents/missing/divergence", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown divergence status %d, want 404", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/money-move/intents", `{"amount":0}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad intent status %d, want 400", rec.Code)
	}
}

func TestRoutesInstructionsVersioning(t *testing.T) {
	_, router := testRouter()
	rec := doRequest(t, router, "POST", "/v1/money-move/instructions", `{"amount":1000,"payee":"alice"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", rec.Code, rec.Body.String())
	}
	var ins map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&ins); err != nil {
		t.Fatal(err)
	}
	id := ins["id"].(string)

	rec = doRequest(t, router, "PUT", "/v1/money-move/instructions/"+id,
		`{"expected_version":1,"amount":2000,"payee":"bob"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", rec.Code, rec.Body.String())
	}
	// Stale version conflicts.
	if rec := doRequest(t, router, "PUT", "/v1/money-move/instructions/"+id,
		`{"expected_version":1,"amount":3000,"payee":"carol"}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale update status %d, want 409", rec.Code)
	}
	rec = doRequest(t, router, "GET", "/v1/money-move/instructions/"+id+"/history", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("history status %d", rec.Code)
	}
	var history []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0]["version"] != float64(1) || history[1]["version"] != float64(2) {
		t.Fatalf("history must be v1..v2: %v", history)
	}
	if rec := doRequest(t, router, "GET", "/v1/money-move/instructions/missing/history", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown history status %d, want 404", rec.Code)
	}
}

func TestRoutesPreconditionsReservationsLeasesOverdraft(t *testing.T) {
	svc, router := testRouter()

	// Preconditions: PASS and FAIL.
	rec := doRequest(t, router, "POST", "/v1/money-move/preconditions/evaluate",
		`{"payment":{"amount":100,"account_id":"a","recipient":"bob","currency":"GBP"},`+
			`"state":{"balance":500,"account_active":true,"recipient_valid":true,"supported_currencies":["GBP"]}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("evaluate status %d: %s", rec.Code, rec.Body.String())
	}
	var eval map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&eval); err != nil {
		t.Fatal(err)
	}
	if eval["verdict"] != "PASS" {
		t.Fatalf("must PASS: %v", eval)
	}
	rec = doRequest(t, router, "POST", "/v1/money-move/preconditions/evaluate",
		`{"payment":{"amount":9999,"account_id":"a","recipient":"","currency":"XXX"},`+
			`"state":{"balance":10,"account_active":false,"recipient_valid":false,"legal_blocked":true}}`)
	var fail map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&fail); err != nil {
		t.Fatal(err)
	}
	if fail["verdict"] != "FAIL" {
		t.Fatalf("must FAIL: %v", fail)
	}
	if failed, ok := fail["failed"].([]interface{}); !ok || len(failed) == 0 {
		t.Fatalf("FAIL must name predicates: %v", fail)
	}

	// Reservations: fund via service, then hold/release/consume.
	svc.Fund("acct-9", 500)
	rec = doRequest(t, router, "POST", "/v1/money-move/reservations", `{"account":"acct-9","amount":200}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reserve status %d: %s", rec.Code, rec.Body.String())
	}
	var res map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	rid := res["id"].(string)
	if rec := doRequest(t, router, "POST", "/v1/money-move/reservations", `{"account":"acct-9","amount":400}`); rec.Code != http.StatusConflict {
		t.Fatalf("oversubscribe status %d, want 409", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/money-move/reservations/"+rid+"/release", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("release status %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, router, "POST", "/v1/money-move/reservations", `{"account":"acct-9","amount":200}`)
	var res2 map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&res2); err != nil {
		t.Fatal(err)
	}
	if rec := doRequest(t, router, "POST", fmt.Sprintf("/v1/money-move/reservations/%s/consume", res2["id"]), `{}`); rec.Code != http.StatusOK {
		t.Fatalf("consume status %d", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/money-move/reservations/missing/release", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown release status %d, want 404", rec.Code)
	}

	// Leases: acquire, renew, execute, stale rejected.
	rec = doRequest(t, router, "POST", "/v1/money-move/leases/acquire", `{"resource":"r1","ttl_seconds":60}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("acquire status %d: %s", rec.Code, rec.Body.String())
	}
	var lease map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&lease); err != nil {
		t.Fatal(err)
	}
	lid := lease["id"].(string)
	token := uint64(lease["token"].(float64))
	if rec := doRequest(t, router, "POST", "/v1/money-move/leases/acquire", `{"resource":"r1","ttl_seconds":60}`); rec.Code != http.StatusConflict {
		t.Fatalf("double acquire status %d, want 409", rec.Code)
	}
	rec = doRequest(t, router, "POST", "/v1/money-move/leases/"+lid+"/renew",
		fmt.Sprintf(`{"token":%d,"ttl_seconds":60}`, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("renew status %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, router, "POST", "/v1/money-move/leases/"+lid+"/execute", fmt.Sprintf(`{"token":%d}`, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("execute status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, router, "POST", "/v1/money-move/leases/"+lid+"/execute", `{"token":999}`); rec.Code != http.StatusConflict {
		t.Fatalf("stale execute status %d, want 409", rec.Code)
	}

	// Overdraft.
	rec = doRequest(t, router, "POST", "/v1/money-move/overdraft/decide",
		`{"available":400,"claims":[{"id":"c1","channel":"card","amount":300},{"id":"t1","channel":"transfer","amount":300}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("overdraft status %d: %s", rec.Code, rec.Body.String())
	}
	var decisions []map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&decisions); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("want 2 decisions: %v", decisions)
	}
	if rec := doRequest(t, router, "POST", "/v1/money-move/overdraft/decide",
		`{"available":10,"claims":[{"id":"x","channel":"nope","amount":5}]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad channel status %d, want 400", rec.Code)
	}
}
