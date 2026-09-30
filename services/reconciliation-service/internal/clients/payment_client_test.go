package clients

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The write-back is the difference between a resolved case and a resolved
// payment, so what actually goes over the wire is worth asserting: the
// credential, the path, and the body.

func TestGetPaymentSendsTheInternalCredential(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")

	var gotToken, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Internal-Token")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"payment_id": "p-1",
			"state":      "UNKNOWN",
			"amount":     1234,
			"currency":   "GBP",
		})
	}))
	defer server.Close()

	t.Setenv("PAYMENT_SERVICE_URL", server.URL)
	client := NewPaymentClient()

	snapshot, err := client.GetPayment(context.Background(), "p-1")
	if err != nil {
		t.Fatalf("GetPayment: %v", err)
	}
	if gotPath != "/v1/payments/p-1" {
		t.Errorf("path = %s, want /v1/payments/p-1", gotPath)
	}
	if gotToken != "test-internal-token" {
		t.Errorf("X-Internal-Token = %q, want the service-to-service secret", gotToken)
	}
	if snapshot.State != "UNKNOWN" || snapshot.Amount != 1234 {
		t.Errorf("snapshot = %+v, want the state and amount from the response", snapshot)
	}
}

func TestResolveUnknownPostsTheOutcomeWithTheCredential(t *testing.T) {
	t.Setenv("INTERNAL_TOKEN", "test-internal-token")

	var (
		gotPath   string
		gotToken  string
		gotBody   map[string]string
		gotMethod string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Internal-Token")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"payment_id":"p-2","state":"FAILED"}`))
	}))
	defer server.Close()

	t.Setenv("PAYMENT_SERVICE_URL", server.URL)
	client := NewPaymentClient()

	if err := client.ResolveUnknown(context.Background(), "p-2", ResolutionOutcomeFailed, "rail rejected"); err != nil {
		t.Fatalf("ResolveUnknown: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/v1/payments/p-2/resolve-unknown" {
		t.Errorf("path = %s, want /v1/payments/p-2/resolve-unknown", gotPath)
	}
	if gotToken != "test-internal-token" {
		t.Errorf("X-Internal-Token = %q, want the service-to-service secret", gotToken)
	}
	if gotBody["outcome"] != ResolutionOutcomeFailed {
		t.Errorf("outcome = %q, want %s", gotBody["outcome"], ResolutionOutcomeFailed)
	}
	if gotBody["reason"] != "rail rejected" {
		t.Errorf("reason = %q, want it passed through", gotBody["reason"])
	}
	if gotBody["resolved_by"] != "reconciliation-service" {
		t.Errorf("resolved_by = %q, want reconciliation-service", gotBody["resolved_by"])
	}
}

// A write-back that failed must be reported as failed. Swallowing it would let
// the case be closed while the payment stays UNKNOWN with the customer's money
// still held.
func TestWriteBackFailuresAreReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer server.Close()

	t.Setenv("PAYMENT_SERVICE_URL", server.URL)
	client := NewPaymentClient()

	if err := client.ResolveUnknown(context.Background(), "p-3", ResolutionOutcomeConfirmed, ""); err == nil {
		t.Fatal("a 500 from payment-service was reported as success")
	}
	if _, err := client.GetPayment(context.Background(), "p-3"); err == nil {
		t.Fatal("a 500 from payment-service was reported as a readable payment")
	}
}

// An undefined outcome is a caller bug. It must be refused before any request is
// made, because the request it would send could conclude a payment with money
// that never moved.
func TestUndefinedOutcomeIsRefusedWithoutCallingOut(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("PAYMENT_SERVICE_URL", server.URL)
	client := NewPaymentClient()

	if err := client.ResolveUnknown(context.Background(), "p-4", "MAYBE", "no idea"); err == nil {
		t.Fatal("an undefined outcome was accepted")
	}
	if called {
		t.Fatal("an undefined outcome still reached payment-service")
	}
}
