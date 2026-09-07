package telemetry

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Counter struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  int64             `json:"value"`
	mu     sync.Mutex
}

type Histogram struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Values []float64         `json:"-"`
	Count  int64             `json:"count"`
	Sum    float64           `json:"sum"`
	Min    float64           `json:"min"`
	Max    float64           `json:"max"`
	mu     sync.Mutex
}

type Gauge struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Value  float64           `json:"value"`
	mu     sync.Mutex
}

type MetricsCollector struct {
	requestCounter     *Counter
	errorCounter       *Counter
	paymentSuccessRate *Gauge
	unknownRate        *Gauge
	kafkaLag           *Gauge
	cassandraLatency   *Histogram
	latencyHistogram   *Histogram
	counters           map[string]*Counter
	histograms         map[string]*Histogram
	gauges             map[string]*Gauge
	mu                 sync.RWMutex
}

func NewMetricsCollector() *MetricsCollector {
	c := &MetricsCollector{
		requestCounter: &Counter{
			Name:   "nexora_requests_total",
			Labels: make(map[string]string),
		},
		errorCounter: &Counter{
			Name:   "nexora_errors_total",
			Labels: make(map[string]string),
		},
		paymentSuccessRate: &Gauge{
			Name:   "nexora_payment_success_rate",
			Labels: make(map[string]string),
		},
		unknownRate: &Gauge{
			Name:   "nexora_unknown_rate",
			Labels: make(map[string]string),
		},
		kafkaLag: &Gauge{
			Name:   "nexora_kafka_lag",
			Labels: make(map[string]string),
		},
		cassandraLatency: &Histogram{
			Name:   "nexora_cassandra_latency_ms",
			Labels: make(map[string]string),
			Values: make([]float64, 0),
			Min:    float64(^uint64(0) >> 1),
		},
		latencyHistogram: &Histogram{
			Name:   "nexora_request_latency_ms",
			Labels: make(map[string]string),
			Values: make([]float64, 0),
			Min:    float64(^uint64(0) >> 1),
		},
		counters:   make(map[string]*Counter),
		histograms: make(map[string]*Histogram),
		gauges:     make(map[string]*Gauge),
	}
	return c
}

func (c *MetricsCollector) IncrRequestCounter(labels map[string]string) {
	c.requestCounter.mu.Lock()
	defer c.requestCounter.mu.Unlock()
	c.requestCounter.Value++
}

func (c *MetricsCollector) IncrErrorCounter(labels map[string]string) {
	c.errorCounter.mu.Lock()
	defer c.errorCounter.mu.Unlock()
	c.errorCounter.Value++
}

func (c *MetricsCollector) ObserveLatency(name string, value float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	hist, ok := c.histograms[name]
	if !ok {
		hist = &Histogram{
			Name:   name,
			Labels: labels,
			Values: make([]float64, 0),
			Min:    float64(^uint64(0) >> 1),
		}
		c.histograms[name] = hist
	}

	hist.mu.Lock()
	defer hist.mu.Unlock()

	hist.Values = append(hist.Values, value)
	hist.Count++
	hist.Sum += value

	if value < hist.Min {
		hist.Min = value
	}
	if value > hist.Max {
		hist.Max = value
	}
}

func (c *MetricsCollector) SetGauge(name string, value float64, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	gauge, ok := c.gauges[name]
	if !ok {
		gauge = &Gauge{
			Name:   name,
			Labels: labels,
		}
		c.gauges[name] = gauge
	}

	gauge.mu.Lock()
	defer gauge.mu.Unlock()
	gauge.Value = value
}

func (c *MetricsCollector) SetPaymentSuccessRate(rate float64) {
	c.paymentSuccessRate.mu.Lock()
	defer c.paymentSuccessRate.mu.Unlock()
	c.paymentSuccessRate.Value = rate
}

func (c *MetricsCollector) SetUnknownRate(rate float64) {
	c.unknownRate.mu.Lock()
	defer c.unknownRate.mu.Unlock()
	c.unknownRate.Value = rate
}

func (c *MetricsCollector) SetKafkaLag(lag float64) {
	c.kafkaLag.mu.Lock()
	defer c.kafkaLag.mu.Unlock()
	c.kafkaLag.Value = lag
}

func (c *MetricsCollector) ObserveCassandraLatency(latencyMs float64) {
	c.cassandraLatency.mu.Lock()
	defer c.cassandraLatency.mu.Unlock()

	c.cassandraLatency.Values = append(c.cassandraLatency.Values, latencyMs)
	c.cassandraLatency.Count++
	c.cassandraLatency.Sum += latencyMs

	if latencyMs < c.cassandraLatency.Min {
		c.cassandraLatency.Min = latencyMs
	}
	if latencyMs > c.cassandraLatency.Max {
		c.cassandraLatency.Max = latencyMs
	}
}

func (c *MetricsCollector) RegisterCounter(name string, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters[name] = &Counter{
		Name:   name,
		Labels: labels,
	}
}

func (c *MetricsCollector) RegisterHistogram(name string, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.histograms[name] = &Histogram{
		Name:   name,
		Labels: labels,
		Values: make([]float64, 0),
		Min:    float64(^uint64(0) >> 1),
	}
}

func (c *MetricsCollector) RegisterGauge(name string, labels map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gauges[name] = &Gauge{
		Name:   name,
		Labels: labels,
	}
}

func (c *MetricsCollector) IncrCounter(name string, labels map[string]string) {
	c.mu.RLock()
	counter, ok := c.counters[name]
	c.mu.RUnlock()

	if !ok {
		c.RegisterCounter(name, labels)
		c.mu.RLock()
		counter = c.counters[name]
		c.mu.RUnlock()
	}

	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.Value++
}

func (c *MetricsCollector) GetCounter(name string) *Counter {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.counters[name]
}

func (c *MetricsCollector) GetHistogram(name string) *Histogram {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.histograms[name]
}

func (c *MetricsCollector) GetGauge(name string) *Gauge {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.gauges[name]
}

func (c *MetricsCollector) GetRequestCount() int64 {
	c.requestCounter.mu.Lock()
	defer c.requestCounter.mu.Unlock()
	return c.requestCounter.Value
}

func (c *MetricsCollector) GetErrorCount() int64 {
	c.errorCounter.mu.Lock()
	defer c.errorCounter.mu.Unlock()
	return c.errorCounter.Value
}

func (c *MetricsCollector) ToPrometheusFormat() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	output := ""

	c.requestCounter.mu.Lock()
	output += fmt.Sprintf("# HELP %s Total number of requests\n", c.requestCounter.Name)
	output += fmt.Sprintf("# TYPE %s counter\n", c.requestCounter.Name)
	output += fmt.Sprintf("%s %d\n", c.requestCounter.Name, c.requestCounter.Value)
	c.requestCounter.mu.Unlock()

	c.errorCounter.mu.Lock()
	output += fmt.Sprintf("# HELP %s Total number of errors\n", c.errorCounter.Name)
	output += fmt.Sprintf("# TYPE %s counter\n", c.errorCounter.Name)
	output += fmt.Sprintf("%s %d\n", c.errorCounter.Name, c.errorCounter.Value)
	c.errorCounter.mu.Unlock()

	c.paymentSuccessRate.mu.Lock()
	output += fmt.Sprintf("# HELP %s Payment success rate\n", c.paymentSuccessRate.Name)
	output += fmt.Sprintf("# TYPE %s gauge\n", c.paymentSuccessRate.Name)
	output += fmt.Sprintf("%s %f\n", c.paymentSuccessRate.Name, c.paymentSuccessRate.Value)
	c.paymentSuccessRate.mu.Unlock()

	c.unknownRate.mu.Lock()
	output += fmt.Sprintf("# HELP %s Unknown state rate\n", c.unknownRate.Name)
	output += fmt.Sprintf("# TYPE %s gauge\n", c.unknownRate.Name)
	output += fmt.Sprintf("%s %f\n", c.unknownRate.Name, c.unknownRate.Value)
	c.unknownRate.mu.Unlock()

	c.kafkaLag.mu.Lock()
	output += fmt.Sprintf("# HELP %s Kafka consumer lag\n", c.kafkaLag.Name)
	output += fmt.Sprintf("# TYPE %s gauge\n", c.kafkaLag.Name)
	output += fmt.Sprintf("%s %f\n", c.kafkaLag.Name, c.kafkaLag.Value)
	c.kafkaLag.mu.Unlock()

	for _, hist := range c.histograms {
		hist.mu.Lock()
		output += fmt.Sprintf("# HELP %s %s\n", hist.Name, hist.Name)
		output += fmt.Sprintf("# TYPE %s histogram\n", hist.Name)
		output += fmt.Sprintf("%s_count %d\n", hist.Name, hist.Count)
		output += fmt.Sprintf("%s_sum %f\n", hist.Name, hist.Sum)
		output += fmt.Sprintf("%s{quantile=\"0.5\"} %f\n", hist.Name, hist.Min)
		output += fmt.Sprintf("%s{quantile=\"0.99\"} %f\n", hist.Name, hist.Max)
		hist.mu.Unlock()
	}

	for _, gauge := range c.gauges {
		gauge.mu.Lock()
		output += fmt.Sprintf("# HELP %s %s\n", gauge.Name, gauge.Name)
		output += fmt.Sprintf("# TYPE %s gauge\n", gauge.Name)
		output += fmt.Sprintf("%s %f\n", gauge.Name, gauge.Value)
		gauge.mu.Unlock()
	}

	return output
}

func (c *MetricsCollector) StartMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, c.ToPrometheusFormat())
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		}
	}()

	return server
}

func (c *MetricsCollector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.requestCounter.Value = 0
	c.errorCounter.Value = 0
	c.paymentSuccessRate.Value = 0
	c.unknownRate.Value = 0
	c.kafkaLag.Value = 0
	c.cassandraLatency.Values = make([]float64, 0)
	c.cassandraLatency.Count = 0
	c.cassandraLatency.Sum = 0
	c.cassandraLatency.Min = float64(^uint64(0) >> 1)
	c.cassandraLatency.Max = 0
	c.counters = make(map[string]*Counter)
	c.histograms = make(map[string]*Histogram)
	c.gauges = make(map[string]*Gauge)
}

func NowUnixNano() int64 {
	return time.Now().UnixNano()
}
