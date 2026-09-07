package telemetry

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// prom.go is a small, dependency-free Prometheus text-format exporter used to
// expose /metrics from every service. It implements the subset of the
// Prometheus data model we need (counters and cumulative histograms with
// label sets) and renders the text exposition format so the Prometheus server
// (and Grafana) can scrape it. Keeping this in shared/telemetry avoids adding
// client_golang to every service module while staying format-compatible.

// defaultBuckets mirrors the Prometheus client_golang default latency buckets
// expressed in seconds.
var defaultBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Registry owns every metric a process exposes. Safe for concurrent use.
type Registry struct {
	mu         sync.Mutex
	counters   map[string]*CounterVec
	histograms map[string]*HistogramVec
}

func NewRegistry() *Registry {
	return &Registry{
		counters:   make(map[string]*CounterVec),
		histograms: make(map[string]*HistogramVec),
	}
}

// PromCounter registers (or returns the existing) counter family with the given
// label names and help text.
func (r *Registry) Counter(name, help string, labelNames ...string) *CounterVec {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.counters[name]; ok {
		return v
	}
	v := &CounterVec{name: name, help: help, labelNames: append([]string(nil), labelNames...), series: make(map[string]*counterSeries)}
	r.counters[name] = v
	return v
}

// PromHistogram registers (or returns the existing) histogram family with
// Prometheus default latency buckets (seconds) and the given label names.
func (r *Registry) Histogram(name, help string, labelNames ...string) *HistogramVec {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.histograms[name]; ok {
		return v
	}
	v := &HistogramVec{name: name, help: help, labelNames: append([]string(nil), labelNames...), buckets: defaultBuckets, series: make(map[string]*histogramSeries)}
	r.histograms[name] = v
	return v
}

// WritePrometheus renders the registry in Prometheus text exposition format.
func (r *Registry) WritePrometheus(w io.Writer) {
	r.mu.Lock()
	cs := make([]*CounterVec, 0, len(r.counters))
	for _, c := range r.counters {
		cs = append(cs, c)
	}
	hs := make([]*HistogramVec, 0, len(r.histograms))
	for _, h := range r.histograms {
		hs = append(hs, h)
	}
	r.mu.Unlock()

	sort.Slice(cs, func(i, j int) bool { return cs[i].name < cs[j].name })
	sort.Slice(hs, func(i, j int) bool { return hs[i].name < hs[j].name })
	for _, c := range cs {
		c.write(w)
	}
	for _, h := range hs {
		h.write(w)
	}
}

func canonicalKey(names, vals []string) string {
	idx := make([]int, len(names))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return names[idx[a]] < names[idx[b]] })
	var sb strings.Builder
	for _, i := range idx {
		sb.WriteString(names[i])
		sb.WriteByte('=')
		sb.WriteString(vals[i])
		sb.WriteByte(',')
	}
	return sb.String()
}

// writeSeriesLine writes `name{labels} value\n`. labelExtras appends
// extra name/value pairs (used for histogram `le` bounds).
func writeSeriesLine(w io.Writer, name string, names, vals []string, value float64) {
	io.WriteString(w, name)
	if len(names) > 0 {
		io.WriteString(w, "{")
		for i, n := range names {
			if i > 0 {
				io.WriteString(w, ",")
			}
			fmt.Fprintf(w, "%s=%q", n, escapeLabel(vals[i]))
		}
		io.WriteString(w, "}")
	}
	fmt.Fprintf(w, " %s\n", strconv.FormatFloat(value, 'g', -1, 64))
}

func escapeLabel(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "\n", "\n")
	v = strings.ReplaceAll(v, "\"", "\\\"")
	return v
}

// ── Counters ───────────────────────────────────────────────────────────────

type counterSeries struct{ value float64 }

type CounterVec struct {
	mu         sync.Mutex
	name, help string
	labelNames []string
	series     map[string]*counterSeries
}

// With returns a handle to the series for this label set (created on first
// use — Prometheus semantics: a series exists once observed).
func (v *CounterVec) With(labels map[string]string) *PromCounter {
	names, vals := orderedLabels(v.labelNames, labels)
	return &PromCounter{vec: v, key: canonicalKey(names, vals), names: names, vals: vals}
}

func (v *CounterVec) write(w io.Writer) {
	v.mu.Lock()
	keys := make([]string, 0, len(v.series))
	snap := make(map[string]float64, len(v.series))
	for k, s := range v.series {
		keys = append(keys, k)
		snap[k] = s.value
	}
	v.mu.Unlock()
	sort.Strings(keys)

	fmt.Fprintf(w, "# HELP %s %s\n", v.name, v.help)
	fmt.Fprintf(w, "# TYPE %s counter\n", v.name)
	for _, k := range keys {
		names, vals := parseKey(k)
		writeSeriesLine(w, v.name, names, vals, snap[k])
	}
}

type PromCounter struct {
	vec   *CounterVec
	key   string
	names []string
	vals  []string
}

func (c *PromCounter) Inc() { c.Add(1) }

func (c *PromCounter) Add(d float64) {
	c.vec.mu.Lock()
	defer c.vec.mu.Unlock()
	s, ok := c.vec.series[c.key]
	if !ok {
		s = &counterSeries{}
		c.vec.series[c.key] = s
	}
	s.value += d
}

// ── Histograms ─────────────────────────────────────────────────────────────

type histogramSeries struct {
	count   int64
	sum     float64
	buckets []int64 // cumulative counts per bucket upper bound
}

type HistogramVec struct {
	mu         sync.Mutex
	name, help string
	labelNames []string
	buckets    []float64
	series     map[string]*histogramSeries
}

func (v *HistogramVec) With(labels map[string]string) *PromHistogram {
	names, vals := orderedLabels(v.labelNames, labels)
	return &PromHistogram{vec: v, key: canonicalKey(names, vals), names: names, vals: vals}
}

func (v *HistogramVec) write(w io.Writer) {
	v.mu.Lock()
	keys := make([]string, 0, len(v.series))
	snap := make(map[string]*histogramSeries, len(v.series))
	for k, s := range v.series {
		keys = append(keys, k)
		cp := *s
		cp.buckets = append([]int64(nil), s.buckets...)
		snap[k] = &cp
	}
	v.mu.Unlock()
	sort.Strings(keys)

	fmt.Fprintf(w, "# HELP %s %s\n", v.name, v.help)
	fmt.Fprintf(w, "# TYPE %s histogram\n", v.name)
	for _, k := range keys {
		names, vals := parseKey(k)
		s := snap[k]
		for i, le := range v.buckets {
			writeSeriesLine(w, v.name+"_bucket", append(append([]string(nil), names...), "le"),
				append(append([]string(nil), vals...), strconv.FormatFloat(le, 'g', -1, 64)),
				float64(s.buckets[i]))
		}
		writeSeriesLine(w, v.name+"_sum", names, vals, s.sum)
		writeSeriesLine(w, v.name+"_count", names, vals, float64(s.count))
	}
}

type PromHistogram struct {
	vec   *HistogramVec
	key   string
	names []string
	vals  []string
}

// Observe records one sample (seconds) into the cumulative buckets.
func (h *PromHistogram) Observe(seconds float64) {
	h.vec.mu.Lock()
	defer h.vec.mu.Unlock()
	s, ok := h.vec.series[h.key]
	if !ok {
		s = &histogramSeries{buckets: make([]int64, len(h.vec.buckets))}
		h.vec.series[h.key] = s
	}
	s.count++
	s.sum += seconds
	for i, le := range h.vec.buckets {
		if seconds <= le {
			s.buckets[i]++
		}
	}
}

func orderedLabels(names []string, labels map[string]string) ([]string, []string) {
	vals := make([]string, len(names))
	for i, n := range names {
		vals[i] = labels[n]
	}
	return names, vals
}

func parseKey(k string) ([]string, []string) {
	parts := strings.Split(strings.TrimSuffix(k, ","), ",")
	names := make([]string, 0, len(parts))
	vals := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		eq := strings.IndexByte(p, '=')
		names = append(names, p[:eq])
		vals = append(vals, p[eq+1:])
	}
	return names, vals
}
