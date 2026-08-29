package cassandra

import (
	"sort"
	"sync"
	"time"
)

type PartitionMetric struct {
	PartitionKey string    `json:"partition_key"`
	ReadCount    int64     `json:"read_count"`
	WriteCount   int64     `json:"write_count"`
	TotalTraffic int64     `json:"total_traffic"`
	AvgLatencyMs float64   `json:"avg_latency_ms"`
	Timestamp    time.Time `json:"timestamp"`
}

type HotPartitionRisk struct {
	PartitionKey   string    `json:"partition_key"`
	Traffic        int64     `json:"traffic"`
	AvgLatencyMs   float64   `json:"avg_latency_ms"`
	RiskLevel      string    `json:"risk_level"`
	Recommendation string    `json:"recommendation"`
	Timestamp      time.Time `json:"timestamp"`
}

type TrafficSkew struct {
	TotalPartitions int                    `json:"total_partitions"`
	TopPartitions   []PartitionMetric      `json:"top_partitions"`
	SkewFactor      float64                `json:"skew_factor"`
	IsSkewed        bool                   `json:"is_skewed"`
}

type HotPartitionDetector struct {
	metrics    map[string]*PartitionMetric
	mu         sync.RWMutex
	thresholds *DetectorThresholds
}

type DetectorThresholds struct {
	HotPartitionTraffic  int64
	SkewFactorThreshold  float64
	HighLatencyMs        float64
}

func DefaultDetectorThresholds() *DetectorThresholds {
	return &DetectorThresholds{
		HotPartitionTraffic: 10000,
		SkewFactorThreshold: 3.0,
		HighLatencyMs:       100.0,
	}
}

func NewHotPartitionDetector() *HotPartitionDetector {
	return &HotPartitionDetector{
		metrics:    make(map[string]*PartitionMetric),
		thresholds: DefaultDetectorThresholds(),
	}
}

func NewHotPartitionDetectorWithThresholds(thresholds *DetectorThresholds) *HotPartitionDetector {
	return &HotPartitionDetector{
		metrics:    make(map[string]*PartitionMetric),
		thresholds: thresholds,
	}
}

func (d *HotPartitionDetector) MonitorPartitionTraffic(partitionKey string, reads, writes int64, latencyMs float64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	metric, ok := d.metrics[partitionKey]
	if !ok {
		metric = &PartitionMetric{
			PartitionKey: partitionKey,
		}
		d.metrics[partitionKey] = metric
	}

	metric.ReadCount += reads
	metric.WriteCount += writes
	metric.TotalTraffic = metric.ReadCount + metric.WriteCount

	if metric.AvgLatencyMs == 0 {
		metric.AvgLatencyMs = latencyMs
	} else {
		metric.AvgLatencyMs = (metric.AvgLatencyMs + latencyMs) / 2
	}

	metric.Timestamp = time.Now().UTC()
}

func (d *HotPartitionDetector) DetectSkew() *TrafficSkew {
	d.mu.RLock()
	defer d.mu.RUnlock()

	skew := &TrafficSkew{
		TotalPartitions: len(d.metrics),
		TopPartitions:   make([]PartitionMetric, 0),
	}

	if len(d.metrics) == 0 {
		return skew
	}

	allMetrics := make([]PartitionMetric, 0, len(d.metrics))
	for _, m := range d.metrics {
		allMetrics = append(allMetrics, *m)
	}

	sort.Slice(allMetrics, func(i, j int) bool {
		return allMetrics[i].TotalTraffic > allMetrics[j].TotalTraffic
	})

	topCount := 5
	if len(allMetrics) < topCount {
		topCount = len(allMetrics)
	}
	skew.TopPartitions = allMetrics[:topCount]

	if len(allMetrics) > 1 {
		var totalTraffic int64
		for _, m := range allMetrics {
			totalTraffic += m.TotalTraffic
		}

		avgTraffic := float64(totalTraffic) / float64(len(allMetrics))

		if avgTraffic > 0 && len(allMetrics) > 0 {
			topTraffic := float64(allMetrics[0].TotalTraffic)
			skew.SkewFactor = topTraffic / avgTraffic
			skew.IsSkewed = skew.SkewFactor > d.thresholds.SkewFactorThreshold
		}
	}

	return skew
}

func (d *HotPartitionDetector) ReportRisk() []*HotPartitionRisk {
	d.mu.RLock()
	defer d.mu.RUnlock()

	risks := make([]*HotPartitionRisk, 0)

	for _, metric := range d.metrics {
		risk := &HotPartitionRisk{
			PartitionKey: metric.PartitionKey,
			Traffic:      metric.TotalTraffic,
			AvgLatencyMs: metric.AvgLatencyMs,
			Timestamp:    time.Now().UTC(),
		}

		if metric.TotalTraffic > d.thresholds.HotPartitionTraffic && metric.AvgLatencyMs > d.thresholds.HighLatencyMs {
			risk.RiskLevel = "CRITICAL"
			risk.Recommendation = "Immediate action: Consider partitioning strategy redesign, add read replicas, or implement caching"
		} else if metric.TotalTraffic > d.thresholds.HotPartitionTraffic {
			risk.RiskLevel = "HIGH"
			risk.Recommendation = "Monitor closely: High traffic partition may cause hotspots, consider adding more partitions"
		} else if metric.AvgLatencyMs > d.thresholds.HighLatencyMs {
			risk.RiskLevel = "MEDIUM"
			risk.Recommendation = "Investigate: High latency detected, check query patterns and index usage"
		} else {
			risk.RiskLevel = "LOW"
			risk.Recommendation = "No action needed"
		}

		if risk.RiskLevel != "LOW" {
			risks = append(risks, risk)
		}
	}

	sort.Slice(risks, func(i, j int) bool {
		return riskPriority(risks[i].RiskLevel) > riskPriority(risks[j].RiskLevel)
	})

	return risks
}

func riskPriority(level string) int {
	switch level {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	default:
		return 0
	}
}

func (d *HotPartitionDetector) GetMetrics() map[string]*PartitionMetric {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]*PartitionMetric)
	for k, v := range d.metrics {
		result[k] = v
	}
	return result
}

func (d *HotPartitionDetector) GetPartitionCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.metrics)
}

func (d *HotPartitionDetector) GetTotalTraffic() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var total int64
	for _, m := range d.metrics {
		total += m.TotalTraffic
	}
	return total
}

func (d *HotPartitionDetector) Reset() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.metrics = make(map[string]*PartitionMetric)
}
