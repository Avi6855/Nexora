package paycycle

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

func testRouter() (*mux.Router, *Service) {
	svc := testService()
	h := NewHandlers(svc, zerolog.Nop())
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	return router, svc
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

func TestRoutesETAQuote(t *testing.T) {
	router, _ := testRouter()

	rec := doRequest(t, router, "POST", "/v1/pay-cycle/eta/quote",
		`{"rail":"CARD_SETTLEMENT","amount_minor":1999,"now":"2026-09-06T12:00:00Z"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("quote status %d: %s", rec.Code, rec.Body.String())
	}
	var quote map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&quote); err != nil {
		t.Fatal(err)
	}
	if quote["late"] != true {
		t.Fatalf("Sunday card quote must be late: %v", quote)
	}
	if reason, _ := quote["late_reason"].(string); !strings.Contains(reason, "Sunday") {
		t.Fatalf("reason must cite Sunday, got %q", reason)
	}

	// Missing rail -> 400; unknown rail -> 400; bad now -> 400.
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/eta/quote", `{"amount_minor":10}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing rail status %d, want 400", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/eta/quote", `{"rail":"NOPE","amount_minor":10}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown rail status %d, want 400", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/eta/quote", `{"rail":"BACS","amount_minor":10,"now":"soon"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad now status %d, want 400", rec.Code)
	}
}

func TestRoutesBeneficiaries(t *testing.T) {
	router, _ := testRouter()

	rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries",
		`{"owner_id":"o1","name":"Landlord","sort_code":"040004","account_number":"12345678"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("created beneficiary must have id: %v", created)
	}

	// Duplicate -> 409.
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries",
		`{"name":"Dupe","sort_code":"04-00-04","account_number":"12345678"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status %d, want 409", rec.Code)
	}
	// Missing name -> 400.
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries",
		`{"sort_code":"040004","account_number":"99999999"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing name status %d, want 400", rec.Code)
	}

	// Verify -> 200; unknown -> 404.
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries/"+id+"/verify", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("verify status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries/ben-missing/verify", `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("verify unknown status %d, want 404", rec.Code)
	}

	// Record payment -> 200 (first payment promotes to TRUSTED).
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries/"+id+"/payments", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("payment status %d: %s", rec.Code, rec.Body.String())
	}

	// Change details -> changed=true; immediate payment now 409 (cooling).
	rec = doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries/"+id+"/details",
		`{"sort_code":"040005","account_number":"87654321"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("details status %d: %s", rec.Code, rec.Body.String())
	}
	var changed map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&changed); err != nil {
		t.Fatal(err)
	}
	if changed["changed"] != true {
		t.Fatalf("details must report changed: %v", changed)
	}
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries/"+id+"/payments", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("cooling payment status %d, want 409", rec.Code)
	}

	// Dormant from RISK_INCREASED -> 409.
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/beneficiaries/"+id+"/dormant", `{}`); rec.Code != http.StatusConflict {
		t.Fatalf("dormant-from-risk status %d, want 409", rec.Code)
	}

	// Delete -> 200; delete again -> 409.
	if rec := doRequest(t, router, "DELETE", "/v1/pay-cycle/beneficiaries/"+id, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, router, "DELETE", "/v1/pay-cycle/beneficiaries/"+id, ""); rec.Code != http.StatusConflict {
		t.Fatalf("double delete status %d, want 409", rec.Code)
	}
}

func TestRoutesApprovals(t *testing.T) {
	router, _ := testRouter()

	rec := doRequest(t, router, "POST", "/v1/pay-cycle/approval-rules",
		`{"id":"big","min_amount_minor":100000,"decision":"REQUIRE_APPROVAL"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add rule status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/approval-rules",
		`{"id":"big","decision":"BLOCK"}`); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate rule status %d, want 409", rec.Code)
	}
	if rec := doRequest(t, router, "POST", "/v1/pay-cycle/approval-rules",
		`{"id":"bad","decision":"MAYBE"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad decision status %d, want 400", rec.Code)
	}

	rec = doRequest(t, router, "POST", "/v1/pay-cycle/payments/evaluate",
		`{"amount_minor":150000,"country":"GB"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("evaluate status %d: %s", rec.Code, rec.Body.String())
	}
	var eval map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&eval); err != nil {
		t.Fatal(err)
	}
	if eval["decision"] != "REQUIRE_APPROVAL" || eval["rule_id"] != "big" {
		t.Fatalf("evaluate wrong: %v", eval)
	}

	rec = doRequest(t, router, "POST", "/v1/pay-cycle/approvals",
		`{"amount_minor":150000,"country":"GB","ttl_seconds":3600}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("request approval status %d: %s", rec.Code, rec.Body.String())
	}
	var approval map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&approval); err != nil {
		t.Fatal(err)
	}
	approvalID, _ := approval["id"].(string)
	if approvalID == "" {
		t.Fatalf("approval must have id: %v", approval)
	}

	// Decide + fetch round-trip.
	decidePath := "/v1/pay-cycle/approvals/" + approvalID + "/decide"
	if rec := doRequest(t, router, "POST", decidePath, `{"approve":true}`); rec.Code != http.StatusOK {
		t.Fatalf("decide status %d: %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, router, "GET", "/v1/pay-cycle/approvals/"+approvalID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get approval status %d: %s", rec.Code, rec.Body.String())
	}
	var fetched map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&fetched); err != nil {
		t.Fatal(err)
	}
	if fetched["status"] != "APPROVED" {
		t.Fatalf("status %v, want APPROVED", fetched)
	}
	// Double decide -> 409; unknown -> 404.
	if rec := doRequest(t, router, "POST", decidePath, `{"approve":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("double decide status %d, want 409", rec.Code)
	}
	if rec := doRequest(t, router, "GET", "/v1/pay-cycle/approvals/apr-999999", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown approval status %d, want 404", rec.Code)
	}
}
