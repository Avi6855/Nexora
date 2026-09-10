// Package datainfra implements Nexora's data-platform health intelligence:
// Cassandra partition/tombstone pressure and Kafka hotspot/lag analysis.
//
// The existing shared/cassandra HotPartitionDetector tracks TRAFFIC skew.
// This package adds the storage-side view that traffic alone cannot see:
//
//   - partition SIZE skew (a 500MB partition next to 2MB siblings is a design
//     defect even at low traffic),
//   - tombstone pressure (deletes accumulate as tombstones; high density
//     degrades reads long before alerts fire),
//   - Kafka partition hotspots and consumer-group lag diagnosis with
//     actionable remediation.
package datainfra

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Cassandra partition size skew ───────────────────────────────────────────

// PartitionSize is one partition's storage footprint.
type PartitionSize struct {
	Keyspace     string    `json:"keyspace"`
	Table        string    `json:"table"`
	PartitionKey string    `json:"partition_key"`
	Bytes        int64     `json:"bytes"`
	CellCount    int64     `json:"cell_count"`
	SampledAt    time.Time `json:"sampled_at"`
}

// SizeSkewReport summarises size skew for a table.
type SizeSkewReport struct {
	Keyspace    string          `json:"keyspace"`
	Table       string          `json:"table"`
	Partitions  int             `json:"partitions"`
	TotalBytes  int64           `json:"total_bytes"`
	MeanBytes   int64           `json:"mean_bytes"`
	P50Bytes    int64           `json:"p50_bytes"`
	P99Bytes    int64           `json:"p99_bytes"`
	MaxBytes    int64           `json:"max_bytes"`
	SkewFactor  float64         `json:"skew_factor"` // max / p50
	IsSkewed    bool            `json:"is_skewed"`
	Offenders   []PartitionSize `json:"offenders"`
	Remediation string          `json:"remediation"`
}

// SizeSkewThresholds encode when a table is declared skewed. Cassandra's
// widely-cited guidance caps healthy partitions around 100MB / 100k cells.
type SizeSkewThresholds struct {
	MaxHealthyBytes int64 // per-partition warning threshold
	MaxHealthyCells int64
	SkewFactor      float64 // max/p50 beyond which the DESIGN is suspect
}

func DefaultSizeSkewThresholds() SizeSkewThresholds {
	return SizeSkewThresholds{MaxHealthyBytes: 100 << 20, MaxHealthyCells: 100_000, SkewFactor: 10.0}
}

// SizeSkewDetector analyses partition size distributions.
type SizeSkewDetector struct {
	th SizeSkewThresholds
}

func NewSizeSkewDetector(th SizeSkewThresholds) *SizeSkewDetector {
	if th.MaxHealthyBytes <= 0 {
		th = DefaultSizeSkewThresholds()
	}
	return &SizeSkewDetector{th: th}
}

// Detect computes the skew report for one table's partitions.
func (d *SizeSkewDetector) Detect(keyspace, table string, parts []PartitionSize) *SizeSkewReport {
	rep := &SizeSkewReport{Keyspace: keyspace, Table: table, Partitions: len(parts)}
	if len(parts) == 0 {
		rep.Remediation = "no partitions sampled"
		return rep
	}
	sorted := append([]PartitionSize(nil), parts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Bytes < sorted[j].Bytes })

	var total int64
	for _, p := range sorted {
		total += p.Bytes
	}
	rep.TotalBytes = total
	rep.MeanBytes = total / int64(len(sorted))
	rep.P50Bytes = percentile(sorted, 0.50)
	rep.P99Bytes = percentile(sorted, 0.99)
	rep.MaxBytes = sorted[len(sorted)-1].Bytes

	if rep.P50Bytes > 0 {
		rep.SkewFactor = float64(rep.MaxBytes) / float64(rep.P50Bytes)
	}
	rep.IsSkewed = rep.SkewFactor >= d.th.SkewFactor ||
		rep.MaxBytes > d.th.MaxHealthyBytes

	for _, p := range sorted {
		if p.Bytes > d.th.MaxHealthyBytes || p.CellCount > d.th.MaxHealthyCells {
			rep.Offenders = append(rep.Offenders, p)
		}
	}
	rep.Remediation = d.remediation(rep)
	return rep
}

// remediation picks the strategy from the skew signature:
//   - few huge partitions + low p50 → a few unbounded keys (per-merchant
//     giants) → bucket by time.
//   - uniformly large partitions → table-wide design issue → composite key.
//   - max within limits but high skew factor → borderline, monitor.
func (d *SizeSkewDetector) remediation(rep *SizeSkewReport) string {
	switch {
	case len(rep.Offenders) > 0 && rep.P99Bytes <= d.th.MaxHealthyBytes:
		return "few unbounded partition keys: add time bucketing (e.g. customer_id + month) to bound partition growth"
	case rep.P99Bytes > d.th.MaxHealthyBytes:
		return "table-wide oversized partitions: redesign primary key with a composite/bucketed key before the table grows further"
	case rep.IsSkewed:
		return "borderline skew: monitor; consider bucketing the top keys proactively"
	default:
		return "healthy"
	}
}

func percentile(sorted []PartitionSize, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)-1))
	return sorted[idx].Bytes
}

// ── Tombstone pressure ──────────────────────────────────────────────────────

// TableTombstoneStats are the per-table signals (from sstable metadata /
// nodetool tablestats equivalents).
type TableTombstoneStats struct {
	Keyspace           string        `json:"keyspace"`
	Table              string        `json:"table"`
	TombstonesTotal    int64         `json:"tombstones_total"`
	LiveCellsTotal     int64         `json:"live_cells_total"`
	DropTombstoneRatio float64       `json:"drop_tombstone_ratio"` // coordinator-dropped reads due to tombstones
	ReadLatencyP99     time.Duration `json:"read_latency_p99"`
	SampledAt          time.Time     `json:"sampled_at"`
}

// TombstoneDensity is tombstones / (tombstones + live cells) for the table.
func (s TableTombstoneStats) TombstoneDensity() float64 {
	denom := s.TombstonesTotal + s.LiveCellsTotal
	if denom == 0 {
		return 0
	}
	return float64(s.TombstonesTotal) / float64(denom)
}

// TombstonePressure is the classification for one table.
type TombstonePressure struct {
	Keyspace string  `json:"keyspace"`
	Table    string  `json:"table"`
	Density  float64 `json:"density"` // 0..1
	Level    string  `json:"level"`   // HEALTHY / ELEVATED / CRITICAL
	Advice   string  `json:"advice"`
}

// Tombstone thresholds. >20% density is widely treated as dangerous; the
// coordinator starts dropping partitions from reads (tombstone_fail_threshold)
// when a single read encounters too many.
const (
	tombstoneElevated = 0.05 // 5%
	tombstoneCritical = 0.20 // 20%
)

// AssessTombstones classifies a table.
func AssessTombstones(s TableTombstoneStats) TombstonePressure {
	p := TombstonePressure{Keyspace: s.Keyspace, Table: s.Table, Density: s.TombstoneDensity()}
	switch {
	case p.Density >= tombstoneCritical:
		p.Level = "CRITICAL"
		p.Advice = "approaching unsafe tombstone density: reads will start dropping partitions (tombstone_fail_threshold). Trigger a major compaction window, then fix the delete pattern (TTL-based expiry or partition-scoped deletes)"
	case p.Density >= tombstoneElevated:
		p.Level = "ELEVATED"
		p.Advice = "tombstone density rising: review delete/ TTL patterns and compaction strategy (DTCS/ TWCS for time-series)"
	default:
		p.Level = "HEALTHY"
		p.Advice = "healthy"
	}
	return p
}

// ── Kafka partition hotspots ────────────────────────────────────────────────

// PartitionLoad is one partition's share of traffic.
type PartitionLoad struct {
	Topic     string  `json:"topic"`
	Partition int     `json:"partition"`
	Bytes     int64   `json:"bytes"`
	SharePct  float64 `json:"share_pct"` // filled by the detector
}

// HotspotReport is the topic-level analysis.
type HotspotReport struct {
	Topic        string   `json:"topic"`
	Partitions   int      `json:"partitions"`
	TotalBytes   int64    `json:"total_bytes"`
	Hottest      int      `json:"hottest_partition"`
	HottestShare float64  `json:"hottest_share_pct"`
	IsHot        bool     `json:"is_hot"`
	Diagnosis    string   `json:"diagnosis"`
	Remediation  []string `json:"remediation"`
}

// HotspotDetector finds uneven partition loading.
type HotspotDetector struct {
	// HotSharePct: a single partition holding more than this share is a hotspot.
	HotSharePct float64
}

func NewHotspotDetector() *HotspotDetector { return &HotspotDetector{HotSharePct: 25.0} }

// Detect analyses one topic's partition loads.
func (d *HotspotDetector) Detect(topic string, loads []PartitionLoad) *HotspotReport {
	rep := &HotspotReport{Topic: topic, Partitions: len(loads)}
	if len(loads) == 0 {
		return rep
	}
	var total int64
	hot := loads[0]
	for _, l := range loads {
		total += l.Bytes
		if l.Bytes > hot.Bytes {
			hot = l
		}
	}
	rep.TotalBytes = total
	if total > 0 {
		for i := range loads {
			loads[i].SharePct = float64(loads[i].Bytes) * 100 / float64(total)
		}
		rep.Hottest = hot.Partition
		rep.HottestShare = float64(hot.Bytes) * 100 / float64(total)
	}
	// A partition is a hotspot when it exceeds BOTH the absolute share
	// threshold AND twice the uniform share (100/N) — otherwise a perfectly
	// even 4-partition topic (25% each) would false-positive at threshold 25%.
	uniformShare := 100.0 / float64(rep.Partitions)
	threshold := d.HotSharePct
	if 2*uniformShare > threshold {
		threshold = 2 * uniformShare
	}
	rep.IsHot = rep.HottestShare > threshold
	if rep.IsHot {
		// Distinguish "one giant key" from "uniformly uneven".
		rep.Diagnosis = fmt.Sprintf("partition %d carries %.1f%% of topic traffic", rep.Hottest, rep.HottestShare)
		rep.Remediation = []string{
			"check the keying scheme for that partition's dominant key (high-volume producer or a merchant/customer giant)",
			"add a salting/bucketing suffix to hot keys at the producer",
			"if skew is producer-side, spread the producing service's partitions across key prefixes",
		}
	} else {
		rep.Diagnosis = "traffic evenly distributed"
		rep.Remediation = []string{"no action needed"}
	}
	return rep
}

// ── Consumer group lag diagnosis ────────────────────────────────────────────

// ConsumerLag is one consumer group's position.
type ConsumerLag struct {
	Topic         string           `json:"topic"`
	Group         string           `json:"group"`
	PartitionLags map[string]int64 `json:"partition_lags"` // "topic-7" → lag
	ActiveMembers int              `json:"active_members"`
	Partitions    int              `json:"partitions"`
	// PoisonPartitions: partitions whose lag grows while others drain.
	PoisonPartitions []string `json:"poison_partitions,omitempty"`
}

// LagVerdict is the advisor's output.
type LagVerdict struct {
	Group           string   `json:"group"`
	TotalLag        int64    `json:"total_lag"`
	MaxPartition    string   `json:"max_partition"`
	MaxLag          int64    `json:"max_lag"`
	Diagnosis       string   `json:"diagnosis"`
	Recommendations []string `json:"recommendations"`
}

// LagAdvisor diagnoses consumer-group lag.
type LagAdvisor struct {
	// MaxLagPerMember: healthy per-member lag ceiling for sizing advice.
	MaxLagPerMember int64
	// SkewRatio: max/median partition lag beyond which distribution is suspect.
	SkewRatio float64
}

func NewLagAdvisor() *LagAdvisor {
	return &LagAdvisor{MaxLagPerMember: 50_000, SkewRatio: 10.0}
}

// Advise produces the diagnosis + recommendations for a lagging group.
func (a *LagAdvisor) Advise(l ConsumerLag) *LagVerdict {
	v := &LagVerdict{Group: l.Group}
	if len(l.PartitionLags) == 0 {
		v.Diagnosis = "no lag data"
		return v
	}
	lags := make([]int64, 0, len(l.PartitionLags))
	names := make([]string, 0, len(l.PartitionLags))
	for name, lag := range l.PartitionLags {
		lags = append(lags, lag)
		names = append(names, name)
		v.TotalLag += lag
	}
	sort.Strings(names)
	sort.Slice(lags, func(i, j int) bool { return lags[i] < lags[j] })

	// Max partition by name (not value) for reporting.
	var maxName string
	var maxLag int64
	for _, n := range names {
		if l.PartitionLags[n] > maxLag {
			maxLag, maxName = l.PartitionLags[n], n
		}
	}
	v.MaxPartition, v.MaxLag = maxName, maxLag

	median := lags[len(lags)/2]
	perMember := v.TotalLag / int64(max(1, l.ActiveMembers))

	// Order matters: poison first (data problem), then SKEW (topology —
	// adding consumers cannot help a single hot partition because Kafka
	// assigns whole partitions), then concurrency (capacity).
	switch {
	case len(l.PoisonPartitions) > 0:
		v.Diagnosis = "poison messages: specific partitions' lag grows while others drain"
		v.Recommendations = []string{
			"quarantine the poison partitions to a DLQ path (shared/retry classification: POISON class)",
			"inspect the failing payload and fix the producer schema",
		}
	case median > 0 && float64(maxLag)/float64(median) > a.SkewRatio:
		v.Diagnosis = fmt.Sprintf("skewed partition lag: %s holds %d vs median %d", maxName, maxLag, median)
		v.Recommendations = []string{
			"check whether the hot partition's key maps to a high-volume producer (see hotspot detector)",
			"consider a dedicated consumer group for the hot partition's traffic class",
		}
	case perMember > a.MaxLagPerMember:
		v.Diagnosis = fmt.Sprintf("insufficient concurrency: %d lag per member across %d members", perMember, l.ActiveMembers)
		v.Recommendations = []string{
			fmt.Sprintf("increase consumers to ~%d (total lag %d / ceiling %d)", (v.TotalLag+a.MaxLagPerMember-1)/a.MaxLagPerMember, v.TotalLag, a.MaxLagPerMember),
			"or raise per-consumer concurrency if handlers are IO-bound",
		}
	case v.TotalLag > 0:
		v.Diagnosis = "moderate lag: keep monitoring"
		v.Recommendations = []string{"no action needed"}
	default:
		v.Diagnosis = "healthy: no lag"
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ── Event topic ownership graph ─────────────────────────────────────────────

// TopicOwnership describes who produces/consumes a topic and how critical it is.
type TopicOwnership struct {
	Topic         string   `json:"topic"`
	ProducerTeam  string   `json:"producer_team"`
	ConsumerTeams []string `json:"consumer_teams"`
	Criticality   string   `json:"criticality"` // PAYMENT_PATH / IMPORTANT / DEFERABLE
	DataClass     string   `json:"data_class"`  // for retention/compliance
}

// OwnershipRegistry answers "who depends on this topic?".
type OwnershipRegistry struct {
	mu      sync.RWMutex
	byTopic map[string]TopicOwnership
}

func NewOwnershipRegistry() *OwnershipRegistry {
	return &OwnershipRegistry{byTopic: map[string]TopicOwnership{}}
}

// Register adds/updates ownership.
func (r *OwnershipRegistry) Register(o TopicOwnership) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byTopic[o.Topic] = o
}

// Lookup returns ownership for a topic.
func (r *OwnershipRegistry) Lookup(topic string) (TopicOwnership, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.byTopic[topic]
	return o, ok
}

// DependentsOf answers "who breaks if this team's service changes?" — the
// reverse index from producer team to consumer teams.
func (r *OwnershipRegistry) DependentsOf(team string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set := map[string]bool{}
	for _, o := range r.byTopic {
		if o.ProducerTeam == team {
			for _, c := range o.ConsumerTeams {
				set[c] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CriticalTopics lists payment-path topics (blast-radius prechecks).
func (r *OwnershipRegistry) CriticalTopics() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []string
	for t, o := range r.byTopic {
		if strings.EqualFold(o.Criticality, "PAYMENT_PATH") {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}
