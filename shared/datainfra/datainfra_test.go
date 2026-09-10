package datainfra

import (
	"strings"
	"testing"
	"time"
)

// ── Cassandra size skew ─────────────────────────────────────────────────────

func TestSizeSkewFewGiantPartitions(t *testing.T) {
	d := NewSizeSkewDetector(DefaultSizeSkewThresholds())
	parts := []PartitionSize{}
	// 200 normal small partitions.
	for i := 0; i < 200; i++ {
		parts = append(parts, PartitionSize{Keyspace: "nexora", Table: "transactions",
			PartitionKey: string(rune('a'+i%26)) + itoa(i), Bytes: int64(2<<20 + i%100)})
	}
	// One 500MB monster (a merchant giant).
	parts = append(parts, PartitionSize{Keyspace: "nexora", Table: "transactions",
		PartitionKey: "merchant-giant", Bytes: 500 << 20})

	rep := d.Detect("nexora", "transactions", parts)
	if !rep.IsSkewed {
		t.Fatal("500MB vs 2MB siblings must be skewed")
	}
	if len(rep.Offenders) != 1 {
		t.Fatalf("one offender expected, got %d", len(rep.Offenders))
	}
	if !strings.Contains(rep.Remediation, "time bucketing") {
		t.Fatalf("few-giants signature must recommend time bucketing: %s", rep.Remediation)
	}
	if rep.P99Bytes > rep.MaxBytes || rep.P50Bytes <= 0 {
		t.Fatalf("percentiles wrong: p50=%d p99=%d max=%d", rep.P50Bytes, rep.P99Bytes, rep.MaxBytes)
	}
}

func TestSizeSkewTableWide(t *testing.T) {
	d := NewSizeSkewDetector(DefaultSizeSkewThresholds())
	parts := []PartitionSize{}
	// EVERY partition oversized: table-wide design defect.
	for i := 0; i < 50; i++ {
		parts = append(parts, PartitionSize{PartitionKey: itoa(i), Bytes: 300 << 20})
	}
	rep := d.Detect("nexora", "bad_design", parts)
	if !strings.Contains(rep.Remediation, "table-wide") || !strings.Contains(rep.Remediation, "composite") {
		t.Fatalf("table-wide signature must recommend composite key: %s", rep.Remediation)
	}
}

func TestSizeSkewHealthy(t *testing.T) {
	d := NewSizeSkewDetector(DefaultSizeSkewThresholds())
	parts := []PartitionSize{}
	for i := 0; i < 100; i++ {
		parts = append(parts, PartitionSize{PartitionKey: itoa(i), Bytes: int64(1<<20 + i)})
	}
	rep := d.Detect("nexora", "ok", parts)
	if rep.IsSkewed || rep.Remediation != "healthy" {
		t.Fatalf("uniform small partitions must be healthy: %+v", rep)
	}
}

func TestSizeSkewEmpty(t *testing.T) {
	d := NewSizeSkewDetector(DefaultSizeSkewThresholds())
	rep := d.Detect("k", "t", nil)
	if rep.Partitions != 0 {
		t.Fatal("empty sample must not crash")
	}
}

// ── Tombstone pressure ──────────────────────────────────────────────────────

func TestTombstonePressureLevels(t *testing.T) {
	cases := []struct {
		tomb, live int64
		want       string
	}{
		{1000, 100_000, "HEALTHY"},
		{10_000, 100_000, "ELEVATED"}, // ~9%
		{30_000, 100_000, "CRITICAL"}, // ~23%
	}
	for _, tc := range cases {
		p := AssessTombstones(TableTombstoneStats{Keyspace: "nexora", Table: "t",
			TombstonesTotal: tc.tomb, LiveCellsTotal: tc.live})
		if p.Level != tc.want {
			t.Errorf("tomb=%d live=%d → %s (%.2f), want %s", tc.tomb, tc.live, p.Level, p.Density, tc.want)
		}
	}
	critical := AssessTombstones(TableTombstoneStats{TombstonesTotal: 30_000, LiveCellsTotal: 100_000})
	if !strings.Contains(critical.Advice, "tombstone_fail_threshold") {
		t.Fatalf("critical advice must mention coordinator read drops: %s", critical.Advice)
	}
}

// ── Kafka hotspots ──────────────────────────────────────────────────────────

func TestKafkaHotspotDetected(t *testing.T) {
	d := NewHotspotDetector()
	loads := []PartitionLoad{
		{Topic: "nexora.payment.created", Partition: 0, Bytes: 820},
		{Topic: "nexora.payment.created", Partition: 1, Bytes: 30},
		{Topic: "nexora.payment.created", Partition: 2, Bytes: 20},
		{Topic: "nexora.payment.created", Partition: 3, Bytes: 130},
	}
	rep := d.Detect("nexora.payment.created", loads)
	if !rep.IsHot || rep.Hottest != 0 {
		t.Fatalf("82 percent on partition 0 must be a hotspot: %+v", rep)
	}
	if rep.HottestShare < 80 || rep.HottestShare > 83 {
		t.Fatalf("share = %.1f%%", rep.HottestShare)
	}
	if len(rep.Remediation) == 0 || !strings.Contains(rep.Remediation[0], "key") {
		t.Fatalf("remediation must discuss keying: %v", rep.Remediation)
	}
}

func TestKafkaEvenDistribution(t *testing.T) {
	d := NewHotspotDetector()
	loads := []PartitionLoad{
		{Partition: 0, Bytes: 250}, {Partition: 1, Bytes: 250},
		{Partition: 2, Bytes: 250}, {Partition: 3, Bytes: 250},
	}
	rep := d.Detect("t", loads)
	if rep.IsHot {
		t.Fatalf("even distribution must not be hot: %+v", rep)
	}
}

// ── Lag advisor ─────────────────────────────────────────────────────────────

func TestLagAdvisorPoisonDetection(t *testing.T) {
	a := NewLagAdvisor()
	v := a.Advise(ConsumerLag{
		Group:         "payments-cg",
		PartitionLags: map[string]int64{"t-0": 5_000, "t-1": 4_000, "t-2": 2_000_000},
		ActiveMembers: 4, Partitions: 3,
		PoisonPartitions: []string{"t-2"},
	})
	if !strings.Contains(v.Diagnosis, "poison") {
		t.Fatalf("poison partitions must dominate diagnosis: %s", v.Diagnosis)
	}
	if !strings.Contains(v.Recommendations[0], "quarantine") {
		t.Fatalf("recommendation must quarantine: %v", v.Recommendations)
	}
}

func TestLagAdvisorConcurrencySizing(t *testing.T) {
	a := NewLagAdvisor()
	v := a.Advise(ConsumerLag{
		Group:         "notifications-cg",
		PartitionLags: map[string]int64{"t-0": 100_000, "t-1": 100_000, "t-2": 100_000, "t-3": 100_000},
		ActiveMembers: 2, Partitions: 4,
	})
	if !strings.Contains(v.Diagnosis, "insufficient concurrency") {
		t.Fatalf("400k lag / 2 members must be a concurrency problem: %s", v.Diagnosis)
	}
	// Recommendation should suggest roughly 400k/50k = 8 consumers.
	if !strings.Contains(v.Recommendations[0], "8") {
		t.Fatalf("sizing recommendation wrong: %v", v.Recommendations)
	}
}

func TestLagAdvisorSkewDetection(t *testing.T) {
	a := NewLagAdvisor()
	v := a.Advise(ConsumerLag{
		Group:         "analytics-cg",
		PartitionLags: map[string]int64{"t-0": 2_000_000, "t-1": 1_000, "t-2": 1_200, "t-3": 900},
		ActiveMembers: 8, Partitions: 4,
	})
	if !strings.Contains(v.Diagnosis, "skewed partition") {
		t.Fatalf("2M vs ~1k median must be skew: %s", v.Diagnosis)
	}
}

func TestLagAdvisorHealthy(t *testing.T) {
	a := NewLagAdvisor()
	v := a.Advise(ConsumerLag{Group: "g", PartitionLags: map[string]int64{"t-0": 0, "t-1": 0}, ActiveMembers: 2})
	if v.Diagnosis != "healthy: no lag" {
		t.Fatalf("zero lag must be healthy: %s", v.Diagnosis)
	}
}

// ── Ownership registry ──────────────────────────────────────────────────────

func TestOwnershipRegistry(t *testing.T) {
	r := NewOwnershipRegistry()
	r.Register(TopicOwnership{Topic: "nexora.payment.created", ProducerTeam: "payments",
		ConsumerTeams: []string{"notifications", "reconciliation", "insights"}, Criticality: "PAYMENT_PATH"})
	r.Register(TopicOwnership{Topic: "nexora.user.registered", ProducerTeam: "identity",
		ConsumerTeams: []string{"notifications"}, Criticality: "IMPORTANT"})
	r.Register(TopicOwnership{Topic: "nexora.analytics.pageview", ProducerTeam: "web",
		ConsumerTeams: []string{"insights"}, Criticality: "DEFERABLE"})

	o, ok := r.Lookup("nexora.payment.created")
	if !ok || o.ProducerTeam != "payments" {
		t.Fatal("lookup failed")
	}
	deps := r.DependentsOf("payments")
	if len(deps) != 3 {
		t.Fatalf("payments team dependents = %v", deps)
	}
	crit := r.CriticalTopics()
	if len(crit) != 1 || crit[0] != "nexora.payment.created" {
		t.Fatalf("critical topics = %v", crit)
	}
}

func itoa(i int) string {
	return fmtInt(i)
}

func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}

var _ = time.Now
