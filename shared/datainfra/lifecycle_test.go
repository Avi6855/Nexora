package datainfra

import (
	"strings"
	"testing"
	"time"
)

func TestTierAccessOverride(t *testing.T) {
	p := TierPolicy{HotUntil: 90 * 24 * time.Hour, WarmUntil: 365 * 24 * time.Hour,
		MinQueriesPerDayToStayHot: 50}
	// 6 months old but still queried heavily → HOT.
	d := DecideTier(180*24*time.Hour, 120, p)
	if d.Tier != TierHot {
		t.Fatalf("access must override age: %+v", d)
	}
	// Same age, idle → WARM.
	d = DecideTier(180*24*time.Hour, 0.1, p)
	if d.Tier != TierWarm {
		t.Fatalf("idle aged data must be warm: %+v", d)
	}
	// Older than warm window, idle → COLD.
	d = DecideTier(500*24*time.Hour, 0.1, p)
	if d.Tier != TierCold {
		t.Fatalf("very old idle data must be cold: %+v", d)
	}
}

func TestPlanBucketingCoarsestFirst(t *testing.T) {
	// Small tenant: whole retention fits → CUSTOMER.
	small := WorkloadProfile{EntityID: "small", RowsPerDay: 200, RetentionDays: 365,
		HotReadsDays: 30, MaxPartitionTargetMB: 100, AvgRowBytes: 500}
	p := PlanBucketing(small)
	if p.Granularity != BucketCustomer {
		t.Fatalf("small tenant should keep customer-level partitions: %+v", p)
	}

	// Huge tenant: customer-level far exceeds target → monthly.
	huge := WorkloadProfile{EntityID: "huge", RowsPerDay: 60_000, RetentionDays: 730,
		HotReadsDays: 90, MaxPartitionTargetMB: 100, AvgRowBytes: 500}
	p = PlanBucketing(huge)
	if p.Granularity != BucketMonthly {
		t.Fatalf("expected monthly buckets: %+v", p)
	}
	if p.ProjectedPartitionMB > huge.MaxPartitionTargetMB {
		t.Fatalf("projection exceeds target: %+v", p)
	}

	// Even monthly splits exceed target → daily.
	huger := WorkloadProfile{EntityID: "huger", RowsPerDay: 500_000, RetentionDays: 730,
		HotReadsDays: 90, MaxPartitionTargetMB: 100, AvgRowBytes: 500}
	p = PlanBucketing(huger)
	if p.Granularity != BucketDaily {
		t.Fatalf("expected daily buckets: %+v", p)
	}
	if p.ProjectedPartitionMB != 238 { // 500k rows × 500B = 238 MB/day
		t.Fatalf("daily projection %d, want 238", p.ProjectedPartitionMB)
	}
}

func TestPlanRepairsWindowAndConcurrency(t *testing.T) {
	jobs := []RepairJob{
		{Cluster: "c1", Keyspace: "k", Table: "t1", Node: "nodeA"},
		{Cluster: "c1", Keyspace: "k", Table: "t2", Node: "nodeA"},
		{Cluster: "c1", Keyspace: "k", Table: "t3", Node: "nodeB"},
		{Cluster: "c1", Keyspace: "k", Table: "t4", Node: "nodeC"},
	}
	w := RepairWindow{StartHour: 1, EndHour: 5, MaxConcurrentRepairs: 2}
	days := []time.Time{
		time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
	}
	plan := PlanRepairs(jobs, days, w)

	if len(plan.Scheduled) != 4 {
		t.Fatalf("all jobs should schedule within windows: %+v", plan)
	}
	// Concurrency cap per day: at most 2 on day one.
	day1 := 0
	for _, j := range plan.Scheduled {
		if j.Scheduled.Day() == 8 {
			day1++
		}
		if j.Scheduled.Hour() != 1 {
			t.Fatalf("jobs must start inside the repair window: %+v", j)
		}
	}
	if day1 != 2 {
		t.Fatalf("concurrency cap 2/day violated: %d on day 1", day1)
	}
	// nodeA's second table can't run the same night as its first (24h rule)
	// but must land the NEXT night — never lost, never same-day.
	var nodeASecond *RepairJob
	for i := range plan.Scheduled {
		j := &plan.Scheduled[i]
		if j.Node == "nodeA" && j.Table == "t2" {
			nodeASecond = j
		}
	}
	if nodeASecond == nil || nodeASecond.Scheduled.Day() != 9 {
		t.Fatalf("nodeA's second repair must land on day 2: %+v", plan.Scheduled)
	}
	if len(plan.Scheduled)+len(plan.Deferred) != len(jobs) {
		t.Fatal("every job must be scheduled or deferred exactly once")
	}
}

func TestOptimiseRetentionBindingConstraints(t *testing.T) {
	// Compliance floor binds: 30 days set, FCA needs 5 years.
	u := TopicUsage{Topic: "payments", CurrentRetention: 30 * 24 * time.Hour,
		GBPerDay: 100, CostPerGBMonth: 0.02,
		Floors: []RetentionRequirement{{FloorDays: 1826, Authority: "FCA"}}}
	r := OptimiseRetention(u)
	if r.Recommended != 1826*24*time.Hour {
		t.Fatalf("floor must raise retention to the floor: %+v", r)
	}
	if !strings.Contains(r.Reasoning, "FCA") {
		t.Fatalf("reasoning must name the authority: %s", r.Reasoning)
	}

	// Consumer lag binds above floor.
	u2 := TopicUsage{Topic: "clicks", CurrentRetention: 90 * 24 * time.Hour,
		GBPerDay: 10, CostPerGBMonth: 0.02,
		MaxConsumerLagHours: 72, ReplaysPerMonth: 2}
	r2 := OptimiseRetention(u2)
	if r2.Recommended != 72*time.Hour {
		t.Fatalf("lag should bind at 72h: %+v", r2)
	}
	if r2.SavingGBPMonth <= 0 {
		t.Fatalf("shrinking 90d→3d must save money: %+v", r2)
	}

	// Replays bind above lag.
	u3 := TopicUsage{Topic: "events", CurrentRetention: 60 * 24 * time.Hour,
		GBPerDay: 5, CostPerGBMonth: 0.02,
		MaxConsumerLagHours: 24, ReplaysPerMonth: 20}
	r3 := OptimiseRetention(u3)
	if r3.Recommended != 20*31/30*24*time.Hour {
		t.Fatalf("replay depth should bind: %+v", r3)
	}

	// Already minimal → no change, no saving.
	u4 := TopicUsage{Topic: "tiny", CurrentRetention: 72 * time.Hour,
		GBPerDay: 1, CostPerGBMonth: 0.02, MaxConsumerLagHours: 72}
	r4 := OptimiseRetention(u4)
	if r4.Recommended != u4.CurrentRetention || r4.SavingGBPMonth != 0 {
		t.Fatalf("minimal retention must be left alone: %+v", r4)
	}
}
