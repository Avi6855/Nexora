package ledgerguard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/ledgerguard"
	"github.com/rs/zerolog"
)

func newTestHandlers() *Handlers {
	return NewHandlers(NewService(), zerolog.Nop())
}

func balancedEntries() []ledgerguard.Entry {
	return []ledgerguard.Entry{
		{Account: "cash", Class: ledgerguard.ClassAsset, Debit: 5000},
		{Account: "deposits", Class: ledgerguard.ClassLiability, Credit: 5000},
	}
}

func TestServiceBalancedPostPasses(t *testing.T) {
	svc := NewService()
	res, err := svc.Post("svc-j1", balancedEntries())
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	if res.Status != ledgerguard.CheckPass {
		t.Fatalf("expected PASS, got %s", res.Status)
	}
}

func TestServiceViolationFreezesAndBlocks(t *testing.T) {
	svc := NewService()
	bad := []ledgerguard.Entry{
		{Account: "cash", Class: ledgerguard.ClassAsset, Debit: 5000},
		{Account: "deposits", Class: ledgerguard.ClassLiability, Credit: 4000},
	}
	if _, err := svc.Post("svc-j2", bad); err == nil {
		t.Fatal("expected violation")
	}
	if _, err := svc.Post("svc-j2", balancedEntries()); err == nil {
		t.Fatal("frozen journal must block mutation")
	}
	incidents := svc.Incidents()
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}
	if err := svc.ClearIncident(incidents[0].ID); err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if _, err := svc.Post("svc-j2", balancedEntries()); err != nil {
		t.Fatalf("post after clear failed: %v", err)
	}
}

func TestServiceReservationLifecycle(t *testing.T) {
	svc := NewService()
	svc.Fund("acct-svc", 10000)
	if _, err := svc.Authorize("r-svc-1", "acct-svc", 4000, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("authorize failed: %v", err)
	}
	if _, err := svc.Authorize("r-svc-1", "acct-svc", 100, time.Now().UTC().Add(time.Hour)); err == nil {
		t.Fatal("duplicate reservation ID must be rejected")
	}
	if _, err := svc.TopUp("r-svc-1", 1000); err != nil {
		t.Fatalf("topup failed: %v", err)
	}
	c, err := svc.Capture("r-svc-1", 2000)
	if err != nil {
		t.Fatalf("partial capture failed: %v", err)
	}
	if c.Captured != 2000 {
		t.Fatalf("captured must be 2000, got %d", c.Captured)
	}
	if _, err := svc.Reverse("r-svc-1"); err != nil {
		t.Fatalf("reverse failed: %v", err)
	}
	b := svc.Balances("acct-svc")
	if b.Reserved != 0 {
		t.Fatalf("reserved must be 0 after reverse, got %+v", b)
	}
}

func doRequest(h *Handlers, method, path string, body interface{}, vars map[string]string, query string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path+query, &buf)
	if vars != nil {
		req = mux.SetURLVars(req, vars)
	}
	rec := httptest.NewRecorder()
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	// Serve via the router so path matching (incl. sweep ordering) is real.
	router.ServeHTTP(rec, req)
	return rec
}

func TestHTTPPostingsAndFreezeRoutes(t *testing.T) {
	h := newTestHandlers()

	rec := doRequest(h, "POST", "/v1/ledger-guard/postings", map[string]interface{}{
		"journal_id": "http-j1",
		"entries":    balancedEntries(),
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("postings status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// Unbalanced posting → 409 + freeze.
	rec = doRequest(h, "POST", "/v1/ledger-guard/postings", map[string]interface{}{
		"journal_id": "http-j2",
		"entries": []ledgerguard.Entry{
			{Account: "cash", Class: ledgerguard.ClassAsset, Debit: 100},
			{Account: "deposits", Class: ledgerguard.ClassLiability, Credit: 90},
		},
	}, nil, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("violation status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}

	// Frozen journal blocks further postings → 409.
	rec = doRequest(h, "POST", "/v1/ledger-guard/postings", map[string]interface{}{
		"journal_id": "http-j2",
		"entries":    balancedEntries(),
	}, nil, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("frozen status = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}

	// Incidents lists the ALERT.
	rec = doRequest(h, "GET", "/v1/ledger-guard/incidents", nil, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("incidents status = %d", rec.Code)
	}
	var incidents []ledgerguard.Incident
	if err := json.NewDecoder(rec.Body).Decode(&incidents); err != nil {
		t.Fatalf("decode incidents: %v", err)
	}
	if len(incidents) != 1 {
		t.Fatalf("expected 1 incident, got %d", len(incidents))
	}

	// Clear re-opens the journal.
	rec = doRequest(h, "POST", "/v1/ledger-guard/incidents/"+incidents[0].ID+"/clear", nil,
		map[string]string{"id": incidents[0].ID}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("clear status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = doRequest(h, "POST", "/v1/ledger-guard/postings", map[string]interface{}{
		"journal_id": "http-j2",
		"entries":    balancedEntries(),
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("post after clear status = %d (%s)", rec.Code, rec.Body.String())
	}

	// Bad body → 400.
	rec = doRequest(h, "POST", "/v1/ledger-guard/postings", map[string]interface{}{}, nil, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad body status = %d, want 400", rec.Code)
	}
}

func TestHTTPReservationRoutes(t *testing.T) {
	h := newTestHandlers()

	// Fund first so authorisation has available funds.
	rec := doRequest(h, "POST", "/v1/ledger-guard/balances/fund", map[string]interface{}{
		"account": "http-acct", "balance": 10000,
	}, nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("fund status = %d (%s)", rec.Code, rec.Body.String())
	}

	rec = doRequest(h, "POST", "/v1/ledger-guard/reservations", map[string]interface{}{
		"id": "http-r1", "account": "http-acct", "amount": 3000, "ttl_seconds": 3600,
	}, nil, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("authorize status = %d (%s)", rec.Code, rec.Body.String())
	}

	// Duplicate ID → 409.
	rec = doRequest(h, "POST", "/v1/ledger-guard/reservations", map[string]interface{}{
		"id": "http-r1", "account": "http-acct", "amount": 100, "ttl_seconds": 3600,
	}, nil, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}

	// Partial capture.
	rec = doRequest(h, "POST", "/v1/ledger-guard/reservations/http-r1/capture", map[string]interface{}{
		"amount": 1000,
	}, map[string]string{"id": "http-r1"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("capture status = %d (%s)", rec.Code, rec.Body.String())
	}

	// Balances math: ledger 9000, reserved 2000, available 7000.
	rec = doRequest(h, "GET", "/v1/ledger-guard/balances", nil, nil, "?account=http-acct")
	if rec.Code != http.StatusOK {
		t.Fatalf("balances status = %d", rec.Code)
	}
	var b ledgerguard.Balance
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatalf("decode balances: %v", err)
	}
	if b.Ledger != 9000 || b.Reserved != 2000 || b.Available != 7000 {
		t.Fatalf("bad balances: %+v", b)
	}

	// Unknown reservation → 404.
	rec = doRequest(h, "POST", "/v1/ledger-guard/reservations/missing/capture", map[string]interface{}{
		"amount": 10,
	}, map[string]string{"id": "missing"}, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing capture status = %d, want 404", rec.Code)
	}

	// Missing account param → 400.
	rec = doRequest(h, "GET", "/v1/ledger-guard/balances", nil, nil, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing account status = %d, want 400", rec.Code)
	}
}
