package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusExposition(t *testing.T) {
	reg := NewRegistry()
	c := reg.Counter("http_requests_total", "Total HTTP requests.", "service", "route")
	c.With(map[string]string{"service": "card-service", "route": "/authorize"}).Inc()
	c.With(map[string]string{"service": "card-service", "route": "/authorize"}).Inc()
	c.With(map[string]string{"service": "ledger-service", "route": "/reserve"}).Inc()

	h := reg.Histogram("http_request_duration_seconds", "Latency.", "service", "route")
	h.With(map[string]string{"service": "card-service", "route": "/authorize"}).Observe(0.032)
	h.With(map[string]string{"service": "card-service", "route": "/authorize"}).Observe(0.101)

	var sb strings.Builder
	reg.WritePrometheus(&sb)
	out := sb.String()

	for _, want := range []string{
		"# HELP http_requests_total Total HTTP requests.",
		"# TYPE http_requests_total counter",
		`http_requests_total{route="/authorize",service="card-service"} 2`,
		`http_requests_total{route="/reserve",service="ledger-service"} 1`,
		"# TYPE http_request_duration_seconds histogram",
		`http_request_duration_seconds_bucket{route="/authorize",service="card-service",le="0.025"} 0`,
		`http_request_duration_seconds_bucket{route="/authorize",service="card-service",le="0.05"} 1`,
		`http_request_duration_seconds_bucket{route="/authorize",service="card-service",le="0.25"} 2`,
		`http_request_duration_seconds_sum{route="/authorize",service="card-service"} 0.133`,
		`http_request_duration_seconds_count{route="/authorize",service="card-service"} 2`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestSanitizeRoute(t *testing.T) {
	cases := map[string]string{
		"/v1/cards/83c89f77-edf2-43d7-9e3c-7b38c99ad562/authorizations/64aa0dcd-3296-46d0-ae56-ac1064df9ad9/capture": "/v1/cards/:id/authorizations/:id/capture",
		"/v1/accounts/12345/balance": "/v1/accounts/:n/balance",
		"/v1/payments":               "/v1/payments",
	}
	for in, want := range cases {
		if got := SanitizeRoute(in); got != want {
			t.Errorf("SanitizeRoute(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetricsMiddleware(t *testing.T) {
	reg := NewRegistry()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h := HTTPMetrics(reg, "payment-service")(inner)

	req := httptest.NewRequest(http.MethodPost, "http://x/v1/payments/5ed606e7-f9d9-4f1b-9543-ffc3c0579a86/authorize", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var sb strings.Builder
	reg.WritePrometheus(&sb)
	out := sb.String()
	if !strings.Contains(out, `http_requests_total{method="POST",route="/v1/payments/:id/authorize",service="payment-service",status="201"} 1`) {
		t.Errorf("middleware did not record request:\n%s", out)
	}
	if !strings.Contains(out, `http_request_duration_seconds_count{method="POST",route="/v1/payments/:id/authorize",service="payment-service"} 1`) {
		t.Errorf("middleware did not record latency:\n%s", out)
	}
}
