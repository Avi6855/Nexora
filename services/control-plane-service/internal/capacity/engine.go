package capacity

import (
	"sync"
	"time"
)

type MetricType string

const (
	MetricCPU            MetricType = "cpu"
	MetricMemory         MetricType = "memory"
	MetricRequestRate    MetricType = "request_rate"
	MetricPaymentRate    MetricType = "payment_rate"
	MetricKafkaLag       MetricType = "kafka_lag"
	MetricCassandraLatency MetricType = "cassandra_latency"
)

type MetricValue struct {
	Type      MetricType `json:"type"`
	Value     float64    `json:"value"`
	Unit      string     `json:"unit"`
	Timestamp time.Time  `json:"timestamp"`
}

type CapacityEstimate struct {
	CurrentUtilization float64 `json:"current_utilization"`
	SafeCapacity       float64 `json:"safe_capacity"`
	Bottleneck         string  `json:"bottleneck"`
	Headroom           float64 `json:"headroom"`
}

type ScalingRecommendation struct {
	Service    string  `json:"service"`
	Metric     string  `json:"metric"`
	Current    float64 `json:"current"`
	Target     float64 `json:"target"`
	Action     string  `json:"action"`
	Instances  int     `json:"instances"`
	Priority   string  `json:"priority"`
	Message    string  `json:"message"`
}

type MetricsCollector interface {
	CollectCPU() (float64, error)
	CollectMemory() (float64, error)
	CollectRequestRate() (float64, error)
	CollectPaymentRate() (float64, error)
	CollectKafkaLag() (float64, error)
	CollectCassandraLatency() (float64, error)
}

type CapacityEngine struct {
	metrics    MetricsCollector
	history    []MetricValue
	recommendations []ScalingRecommendation
	mu         sync.RWMutex
	thresholds *CapacityThresholds
}

type CapacityThresholds struct {
	CPUHigh             float64
	MemoryHigh          float64
	RequestRateHigh     float64
	PaymentRateHigh     float64
	KafkaLagHigh        float64
	CassandraLatencyHigh float64
}

func DefaultCapacityThresholds() *CapacityThresholds {
	return &CapacityThresholds{
		CPUHigh:              75.0,
		MemoryHigh:           80.0,
		RequestRateHigh:      1000.0,
		PaymentRateHigh:      500.0,
		KafkaLagHigh:         10000.0,
		CassandraLatencyHigh: 500.0,
	}
}

func NewCapacityEngine(metrics MetricsCollector) *CapacityEngine {
	return &CapacityEngine{
		metrics:        metrics,
		history:        make([]MetricValue, 0),
		recommendations: make([]ScalingRecommendation, 0),
		thresholds:     DefaultCapacityThresholds(),
	}
}

func NewCapacityEngineWithThresholds(metrics MetricsCollector, thresholds *CapacityThresholds) *CapacityEngine {
	return &CapacityEngine{
		metrics:        metrics,
		history:        make([]MetricValue, 0),
		recommendations: make([]ScalingRecommendation, 0),
		thresholds:     thresholds,
	}
}

func (e *CapacityEngine) CollectMetrics() ([]MetricValue, error) {
	metrics := make([]MetricValue, 0)
	now := time.Now().UTC()

	cpu, err := e.metrics.CollectCPU()
	if err == nil {
		metrics = append(metrics, MetricValue{
			Type:      MetricCPU,
			Value:     cpu,
			Unit:      "percent",
			Timestamp: now,
		})
	}

	mem, err := e.metrics.CollectMemory()
	if err == nil {
		metrics = append(metrics, MetricValue{
			Type:      MetricMemory,
			Value:     mem,
			Unit:      "percent",
			Timestamp: now,
		})
	}

	reqRate, err := e.metrics.CollectRequestRate()
	if err == nil {
		metrics = append(metrics, MetricValue{
			Type:      MetricRequestRate,
			Value:     reqRate,
			Unit:      "rps",
			Timestamp: now,
		})
	}

	payRate, err := e.metrics.CollectPaymentRate()
	if err == nil {
		metrics = append(metrics, MetricValue{
			Type:      MetricPaymentRate,
			Value:     payRate,
			Unit:      "tps",
			Timestamp: now,
		})
	}

	kafkaLag, err := e.metrics.CollectKafkaLag()
	if err == nil {
		metrics = append(metrics, MetricValue{
			Type:      MetricKafkaLag,
			Value:     kafkaLag,
			Unit:      "messages",
			Timestamp: now,
		})
	}

	cassLat, err := e.metrics.CollectCassandraLatency()
	if err == nil {
		metrics = append(metrics, MetricValue{
			Type:      MetricCassandraLatency,
			Value:     cassLat,
			Unit:      "ms",
			Timestamp: now,
		})
	}

	e.mu.Lock()
	e.history = append(e.history, metrics...)
	if len(e.history) > 1000 {
		e.history = e.history[len(e.history)-1000:]
	}
	e.mu.Unlock()

	return metrics, nil
}

func (e *CapacityEngine) EstimateCapacity() *CapacityEstimate {
	e.mu.RLock()
	defer e.mu.RUnlock()

	estimate := &CapacityEstimate{
		CurrentUtilization: 0,
		SafeCapacity:       100,
		Bottleneck:         "none",
		Headroom:           100,
	}

	var latestCPU, latestMem, latestReq, latestPay, latestKafka, latestCass float64

	for i := len(e.history) - 1; i >= 0; i-- {
		m := e.history[i]
		switch m.Type {
		case MetricCPU:
			if latestCPU == 0 {
				latestCPU = m.Value
			}
		case MetricMemory:
			if latestMem == 0 {
				latestMem = m.Value
			}
		case MetricRequestRate:
			if latestReq == 0 {
				latestReq = m.Value
			}
		case MetricPaymentRate:
			if latestPay == 0 {
				latestPay = m.Value
			}
		case MetricKafkaLag:
			if latestKafka == 0 {
				latestKafka = m.Value
			}
		case MetricCassandraLatency:
			if latestCass == 0 {
				latestCass = m.Value
			}
		}
	}

	cpuUtil := latestCPU
	memUtil := latestMem
	reqUtil := (latestReq / e.thresholds.RequestRateHigh) * 100
	payUtil := (latestPay / e.thresholds.PaymentRateHigh) * 100
	kafkaUtil := (latestKafka / e.thresholds.KafkaLagHigh) * 100
	cassUtil := (latestCass / e.thresholds.CassandraLatencyHigh) * 100

	maxUtil := cpuUtil
	bottleneck := "CPU"

	if memUtil > maxUtil {
		maxUtil = memUtil
		bottleneck = "Memory"
	}
	if reqUtil > maxUtil {
		maxUtil = reqUtil
		bottleneck = "Request Rate"
	}
	if payUtil > maxUtil {
		maxUtil = payUtil
		bottleneck = "Payment Rate"
	}
	if kafkaUtil > maxUtil {
		maxUtil = kafkaUtil
		bottleneck = "Kafka Lag"
	}
	if cassUtil > maxUtil {
		maxUtil = cassUtil
		bottleneck = "Cassandra Latency"
	}

	estimate.CurrentUtilization = maxUtil
	estimate.Bottleneck = bottleneck
	estimate.SafeCapacity = 100
	estimate.Headroom = 100 - maxUtil

	return estimate
}

func (e *CapacityEngine) Recommend() []ScalingRecommendation {
	e.mu.Lock()
	e.recommendations = make([]ScalingRecommendation, 0)
	e.mu.Unlock()

	metrics := e.CollectMetrics()

	e.mu.Lock()
	for _, m := range metrics {
		rec := e.evaluateMetric(m)
		if rec != nil {
			e.recommendations = append(e.recommendations, *rec)
		}
	}

	result := make([]ScalingRecommendation, len(e.recommendations))
	copy(result, e.recommendations)
	e.mu.Unlock()
	return result
}

func (e *CapacityEngine) CollectMetricsUnsafe() []MetricValue {
	metrics := make([]MetricValue, 0)
	now := time.Now().UTC()

	cpu, err := e.metrics.CollectCPU()
	if err == nil {
		metrics = append(metrics, MetricValue{Type: MetricCPU, Value: cpu, Unit: "percent", Timestamp: now})
	}

	mem, err := e.metrics.CollectMemory()
	if err == nil {
		metrics = append(metrics, MetricValue{Type: MetricMemory, Value: mem, Unit: "percent", Timestamp: now})
	}

	reqRate, err := e.metrics.CollectRequestRate()
	if err == nil {
		metrics = append(metrics, MetricValue{Type: MetricRequestRate, Value: reqRate, Unit: "rps", Timestamp: now})
	}

	payRate, err := e.metrics.CollectPaymentRate()
	if err == nil {
		metrics = append(metrics, MetricValue{Type: MetricPaymentRate, Value: payRate, Unit: "tps", Timestamp: now})
	}

	kafkaLag, err := e.metrics.CollectKafkaLag()
	if err == nil {
		metrics = append(metrics, MetricValue{Type: MetricKafkaLag, Value: kafkaLag, Unit: "messages", Timestamp: now})
	}

	cassLat, err := e.metrics.CollectCassandraLatency()
	if err == nil {
		metrics = append(metrics, MetricValue{Type: MetricCassandraLatency, Value: cassLat, Unit: "ms", Timestamp: now})
	}

	return metrics
}

func (e *CapacityEngine) evaluateMetric(m MetricValue) *ScalingRecommendation {
	switch m.Type {
	case MetricCPU:
		if m.Value > e.thresholds.CPUHigh {
			instances := int(m.Value/e.thresholds.CPUHigh) + 1
			return &ScalingRecommendation{
				Service:   "all-services",
				Metric:    "cpu",
				Current:   m.Value,
				Target:    e.thresholds.CPUHigh,
				Action:    "SCALE_UP",
				Instances: instances,
				Priority:  "HIGH",
				Message:   "CPU utilization exceeded threshold, scale up recommended",
			}
		}
	case MetricMemory:
		if m.Value > e.thresholds.MemoryHigh {
			instances := int(m.Value/e.thresholds.MemoryHigh) + 1
			return &ScalingRecommendation{
				Service:   "all-services",
				Metric:    "memory",
				Current:   m.Value,
				Target:    e.thresholds.MemoryHigh,
				Action:    "SCALE_UP",
				Instances: instances,
				Priority:  "HIGH",
				Message:   "Memory utilization exceeded threshold, scale up recommended",
			}
		}
	case MetricRequestRate:
		if m.Value > e.thresholds.RequestRateHigh {
			instances := int(m.Value/e.thresholds.RequestRateHigh) + 1
			return &ScalingRecommendation{
				Service:   "all-services",
				Metric:    "request_rate",
				Current:   m.Value,
				Target:    e.thresholds.RequestRateHigh,
				Action:    "SCALE_UP",
				Instances: instances,
				Priority:  "MEDIUM",
				Message:   "Request rate exceeded threshold, scale up recommended",
			}
		}
	case MetricPaymentRate:
		if m.Value > e.thresholds.PaymentRateHigh {
			instances := int(m.Value/e.thresholds.PaymentRateHigh) + 1
			return &ScalingRecommendation{
				Service:   "payment-service",
				Metric:    "payment_rate",
				Current:   m.Value,
				Target:    e.thresholds.PaymentRateHigh,
				Action:    "SCALE_UP",
				Instances: instances,
				Priority:  "CRITICAL",
				Message:   "Payment rate exceeded threshold, immediate scaling required",
			}
		}
	case MetricKafkaLag:
		if m.Value > e.thresholds.KafkaLagHigh {
			return &ScalingRecommendation{
				Service:   "kafka-consumers",
				Metric:    "kafka_lag",
				Current:   m.Value,
				Target:    e.thresholds.KafkaLagHigh,
				Action:    "SCALE_UP",
				Instances: 2,
				Priority:  "HIGH",
				Message:   "Kafka lag exceeded threshold, increase consumer instances",
			}
		}
	case MetricCassandraLatency:
		if m.Value > e.thresholds.CassandraLatencyHigh {
			return &ScalingRecommendation{
				Service:   "cassandra",
				Metric:    "cassandra_latency",
				Current:   m.Value,
				Target:    e.thresholds.CassandraLatencyHigh,
				Action:    "SCALE_UP",
				Instances: 2,
				Priority:  "HIGH",
				Message:   "Cassandra latency exceeded threshold, scale up cluster",
			}
		}
	}

	return nil
}

func (e *CapacityEngine) GetHistory() []MetricValue {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]MetricValue, len(e.history))
	copy(result, e.history)
	return result
}

func (e *CapacityEngine) GetRecommendations() []ScalingRecommendation {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]ScalingRecommendation, len(e.recommendations))
	copy(result, e.recommendations)
	return result
}

type DummyMetricsCollector struct{}

func NewDummyMetricsCollector() *DummyMetricsCollector {
	return &DummyMetricsCollector{}
}

func (d *DummyMetricsCollector) CollectCPU() (float64, error) {
	return 45.0, nil
}

func (d *DummyMetricsCollector) CollectMemory() (float64, error) {
	return 60.0, nil
}

func (d *DummyMetricsCollector) CollectRequestRate() (float64, error) {
	return 500.0, nil
}

func (d *DummyMetricsCollector) CollectPaymentRate() (float64, error) {
	return 200.0, nil
}

func (d *DummyMetricsCollector) CollectKafkaLag() (float64, error) {
	return 500.0, nil
}

func (d *DummyMetricsCollector) CollectCassandraLatency() (float64, error) {
	return 50.0, nil
}
