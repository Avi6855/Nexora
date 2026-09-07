package telemetry

import (
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// HTTPMetrics instruments a service's HTTP router with Prometheus request
// counters and latency histograms. Every service calls
//
//	reg := telemetry.NewRegistry()
//	router.HandleFunc("/metrics", telemetry.MetricsHandler(reg))
//	router.Use(telemetry.HTTPMetrics(reg, "payment-service"))
//
// Routes are sanitised before they become label values (UUIDs and long
// numeric ids collapse to :id/:n) so label cardinality stays bounded.

var (
	uuidPattern   = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	digitsPattern = regexp.MustCompile(`^[0-9]+$`)
)

// SanitizeRoute collapses dynamic path segments into stable placeholders so
// every request maps to a bounded set of label values.
func SanitizeRoute(path string) string {
	parts := splitPath(path)
	for i, p := range parts {
		switch {
		case uuidPattern.MatchString(p):
			parts[i] = ":id"
		case digitsPattern.MatchString(p):
			parts[i] = ":n"
		}
	}
	return joinPath(parts)
}

func splitPath(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			if i > start {
				out = append(out, p[start:i])
			}
			start = i + 1
		}
	}
	if start < len(p) {
		out = append(out, p[start:])
	}
	return out
}

func joinPath(parts []string) string {
	if len(parts) == 0 {
		return "/"
	}
	s := ""
	for _, p := range parts {
		s += "/" + p
	}
	return s
}

// statusWriter records the response status so the middleware can dimension
// request counters by outcome. It preserves http.Flusher so Server-Sent
// Events (the notification /v1/stream endpoint) keep working through the
// middleware.
type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.status = http.StatusOK
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// HTTPMetrics returns middleware that records every request (except /metrics
// itself) into the registry. Labels: service, method, route, status.
func HTTPMetrics(reg *Registry, service string) func(http.Handler) http.Handler {
	requests := reg.Counter("http_requests_total", "Total HTTP requests handled.", "service", "method", "route", "status")
	duration := reg.Histogram("http_request_duration_seconds", "HTTP request latency in seconds.", "service", "method", "route")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			route := SanitizeRoute(r.URL.Path)
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			elapsed := time.Since(start).Seconds()

			duration.With(map[string]string{
				"service": service,
				"method":  r.Method,
				"route":   route,
			}).Observe(elapsed)

			requests.With(map[string]string{
				"service": service,
				"method":  r.Method,
				"route":   route,
				"status":  strconv.Itoa(sw.status),
			}).Inc()
		})
	}
}

// MetricsHandler serves the registry in Prometheus text format on /metrics.
func MetricsHandler(reg *Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		reg.WritePrometheus(w)
	}
}
