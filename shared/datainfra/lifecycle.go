// Package datainfra — storage lifecycle intelligence (part 2).
//
//  19. Hot/Cold tiering: keep 0–90 days on fast storage, migrate older data
//     by access pattern, not by age alone — a 6-month-old partition that is
//     still queried hourly is hot, whatever the calendar says.
//  21. Adaptive bucketing: choose partition granularity (customer, +month,
//     +day) from measured workload so partitions stay large enough to be
//     efficient and small enough not to skew.
//  22. Repair orchestration: anti-entropy repairs are scheduled, throttled and
//     verified so production traffic is never starved.
//  26. Retention cost optimisation: retention is chosen from replay
//     frequency, storage cost and compliance floors — not arbitrary days.
package datainfra

import (
	"fmt"
	"sort"
	"time"
)

// ── 19. Hot / cold tiering ──────────────────────────────────────────────────

// TierPolicy expresses age bands, but access overrides them.
type TierPolicy struct {
	HotUntil  time.Duration // e.g. 90 days
	WarmUntil time.Duration // e.g. 365 days
	// MinQueriesPerDay keeps still-queried old data hot.
	MinQueriesPerDayToStayHot float64
}

type Tier string

const (
	TierHot  Tier = "HOT"
	TierWarm Tier = "WARM"
	TierCold Tier = "COLD"
)

// TierDecision is the tier for one data segment.
type TierDecision struct {
	Tier        Tier    `json:"tier"`
	AgeDays     float64 `json:"age_days"`
	QueriesDay  float64 `json:"queries_per_day"`
	Explanation string  `json:"explanation"`
}

func DecideTier(age time.Duration, queriesPerDay float64, p TierPolicy) TierDecision {
	days := age.Hours() / 24
	// Access-based override FIRST: a hot old partition stays hot.
	if queriesPerDay >= p.MinQueriesPerDayToStayHot {
		return TierDecision{Tier: TierHot, AgeDays: days, QueriesDay: queriesPerDay,
			Explanation: fmt.Sprintf("%.0f q/day ≥ %.0f keeps data hot despite age", queriesPerDay, p.MinQueriesPerDayToStayHot)}
	}
	switch {
	case age <= p.HotUntil:
		return TierDecision{Tier: TierHot, AgeDays: days, QueriesDay: queriesPerDay,
			Explanation: fmt.Sprintf("age %.0fd ≤ hot window", days)}
	case age <= p.WarmUntil:
		return TierDecision{Tier: TierWarm, AgeDays: days, QueriesDay: queriesPerDay,
			Explanation: fmt.Sprintf("age %.0fd in warm window and only %.1f q/day", days, queriesPerDay)}
	default:
		return TierDecision{Tier: TierCold, AgeDays: days, QueriesDay: queriesPerDay,
			Explanation: fmt.Sprintf("age %.0fd exceeds warm window", days)}
	}
}

// ── 21. Adaptive bucketing ──────────────────────────────────────────────────

// Granularity is the partition key strategy for one entity's event stream.
type Granularity string

const (
	BucketCustomer Granularity = "CUSTOMER"       // partition = customer_id
	BucketMonthly  Granularity = "CUSTOMER+MONTH" // + yyyy-mm
	BucketDaily    Granularity = "CUSTOMER+DAY"   // + yyyy-mm-dd
)

// WorkloadProfile is measured reality for one entity.
type WorkloadProfile struct {
	EntityID             string
	RowsTotal            int64
	RowsPerDay           int64 // steady-state write rate
	RetentionDays        int   // how long rows live
	HotReadsDays         int   // reads concentrated in the most recent N days
	MaxPartitionTargetMB int64 // healthy partition size target
	// AvgRowBytes estimate.
	AvgRowBytes int64
}

// BucketingPlan is the chosen strategy with the arithmetic shown.
type BucketingPlan struct {
	EntityID    string      `json:"entity_id"`
	Granularity Granularity `json:"granularity"`
	// ProjectedPartitionMB for the worst-case (largest live) partition.
	ProjectedPartitionMB int64  `json:"projected_partition_mb"`
	Reasoning            string `json:"reasoning"`
}

// PlanBucketing picks granularity: prefer the COARSEST bucket whose worst
// live partition stays within target. Coarse partitions keep reads cheap
// (one partition per query); we split only when size forces it.
func PlanBucketing(w WorkloadProfile) BucketingPlan {
	liveRows := w.RowsPerDay * int64(w.RetentionDays)
	if liveRows < 1 {
		liveRows = w.RowsTotal // no measured rate; fall back to totals
	}
	hotRows := liveRows
	if w.HotReadsDays > 0 && w.HotReadsDays < w.RetentionDays {
		hotRows = w.RowsPerDay * int64(w.HotReadsDays)
	}
	rowMB := float64(w.AvgRowBytes) / (1024 * 1024)
	if rowMB <= 0 {
		rowMB = 0.0001
	}
	partMB := func(rows int64, buckets int64) int64 {
		if buckets < 1 {
			buckets = 1
		}
		per := rows / buckets
		return int64(float64(per)*rowMB + 0.5)
	}
	target := w.MaxPartitionTargetMB
	if target <= 0 {
		target = 100 // MB, sane default
	}
	switch {
	case partMB(liveRows, 1) <= target:
		return BucketingPlan{EntityID: w.EntityID, Granularity: BucketCustomer,
			ProjectedPartitionMB: partMB(liveRows, 1),
			Reasoning:            fmt.Sprintf("whole-retention data %.0f MB within %d MB target — one partition per query is cheapest", float64(liveRows)*rowMB, target)}
	case partMB(hotRows, 30) <= target:
		return BucketingPlan{EntityID: w.EntityID, Granularity: BucketMonthly,
			ProjectedPartitionMB: partMB(hotRows, 30),
			Reasoning:            fmt.Sprintf("customer partition would be %.0f MB; monthly buckets keep hot-window partitions ≈ %.0f MB", float64(liveRows)*rowMB, float64(hotRows/30)*rowMB)}
	default:
		days := int64(w.HotReadsDays)
		if days < 1 {
			days = 1
		}
		perDay := partMB(hotRows, days) // one bucket per hot DAY
		return BucketingPlan{EntityID: w.EntityID, Granularity: BucketDaily,
			ProjectedPartitionMB: perDay,
			Reasoning:            fmt.Sprintf("monthly buckets would still be %.0f MB; daily buckets cap partitions at ≈ %.0f MB", float64(hotRows/30)*rowMB, float64(perDay))}
	}
}

// ── 22. Repair orchestration ────────────────────────────────────────────────

// RepairJob is one anti-entropy repair over a token range.
type RepairJob struct {
	Cluster   string    `json:"cluster"`
	Keyspace  string    `json:"keyspace"`
	Table     string    `json:"table"`
	Node      string    `json:"node"`
	Scheduled time.Time `json:"scheduled"`
}

// RepairWindow defines when production impact is acceptable.
type RepairWindow struct {
	// Start/End in local clock hours, e.g. 1..5 → 01:00–05:00.
	StartHour, EndHour int
	// MaxConcurrentRepairs protects compaction/IO headroom.
	MaxConcurrentRepairs int
	// ThrottleFraction of node IO available to repair (0..1).
	ThrottleFraction float64
}

// RepairPlan schedules jobs inside windows, capped at concurrency, spreading
// nodes across nights so no node is repaired two nights running.
type RepairPlan struct {
	Scheduled []RepairJob `json:"scheduled"`
	Deferred  []RepairJob `json:"deferred,omitempty"`
	Reasons   []string    `json:"reasons,omitempty"`
}

// PlanRepairs slots jobs into the next len(days) eligible windows.
func PlanRepairs(jobs []RepairJob, days []time.Time, w RepairWindow) RepairPlan {
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].Scheduled.Before(jobs[j].Scheduled) })
	plan := RepairPlan{}
	perDay := make([]int, len(days))
	lastRepaired := map[string]time.Time{}
	done := map[int]bool{} // jobs already scheduled — a job never runs twice
	deferredReason := map[int]string{}
	for _, day := range days {
		slot := time.Date(day.Year(), day.Month(), day.Day(), w.StartHour, 0, 0, 0, day.Location())
		for i, job := range jobs {
			if done[i] || perDay[dayIndexOf(day, days)] >= w.MaxConcurrentRepairs {
				continue
			}
			if last, ok := lastRepaired[job.Node]; ok && day.Sub(last) < 24*time.Hour {
				deferredReason[i] = fmt.Sprintf("%s repaired within 24h — deferred", job.Node)
				continue
			}
			j := job
			j.Scheduled = slot
			plan.Scheduled = append(plan.Scheduled, j)
			perDay[dayIndexOf(day, days)]++
			lastRepaired[job.Node] = day
			done[i] = true
		}
	}
	// A job that never got a slot in any window is genuinely deferred.
	for i, job := range jobs {
		if !done[i] {
			plan.Deferred = append(plan.Deferred, job)
			plan.Reasons = append(plan.Reasons, deferredReason[i])
		}
	}
	return plan
}

func dayIndexOf(d time.Time, days []time.Time) int {
	for i := range days {
		if days[i].Equal(d) {
			return i
		}
	}
	return 0
}

// ── 26. Retention cost optimisation ─────────────────────────────────────────

// RetentionRequirement is a hard compliance floor (e.g. FCA 5 years).
type RetentionRequirement struct {
	FloorDays int    `json:"floor_days"`
	Authority string `json:"authority"` // who mandates it
}

// TopicUsage is measured behaviour of one topic.
type TopicUsage struct {
	Topic            string
	CurrentRetention time.Duration
	GBPerDay         float64
	CostPerGBMonth   float64 // £
	// ReplaysPerMonth: how often consumers actually re-read old data.
	ReplaysPerMonth int
	// MaxConsumerLagHours: consumers may fall behind by this much; retention
	// must never be shorter, or a lagging consumer loses data.
	MaxConsumerLagHours float64
	// Compliance floors that apply.
	Floors []RetentionRequirement
}

// RetentionRecommendation is the optimised setting with the trade-off shown.
type RetentionRecommendation struct {
	Topic          string        `json:"topic"`
	Current        time.Duration `json:"current"`
	Recommended    time.Duration `json:"recommended"`
	SavingGBPMonth float64       `json:"saving_gbp_month"`
	Reasoning      string        `json:"reasoning"`
}

// OptimiseRetention picks the shortest retention that satisfies: the binding
// compliance floor, worst-case consumer lag, and observed replay depth.
func OptimiseRetention(u TopicUsage) RetentionRecommendation {
	floorDays := 0
	floorBy := "none"
	for _, f := range u.Floors {
		if f.FloorDays > floorDays {
			floorDays, floorBy = f.FloorDays, f.Authority
		}
	}
	floor := time.Duration(floorDays) * 24 * time.Hour
	lag := time.Duration(u.MaxConsumerLagHours * float64(time.Hour))
	replay := time.Duration(u.ReplaysPerMonth*31/30) * 24 * time.Hour // observed replay depth ≈ monthly span

	recommended := floor
	reasons := []string{}
	binding := "compliance floor " + floorBy
	if lag > recommended {
		recommended, binding = lag, "max consumer lag"
	}
	if replay > recommended {
		recommended, binding = replay, "observed replay depth"
	}
	if floor > 0 || binding != "none" {
		reasons = append(reasons, fmt.Sprintf("binding constraint: %s", binding))
	}
	if recommended >= u.CurrentRetention {
		reasons = append(reasons, fmt.Sprintf("current retention (%.0fd) is BELOW the binding constraint — increase, do not shrink",
			u.CurrentRetention.Hours()/24))
		return RetentionRecommendation{Topic: u.Topic, Current: u.CurrentRetention,
			Recommended: recommended, SavingGBPMonth: 0,
			Reasoning: join(reasons)}
	}
	gb := u.GBPerDay * u.CurrentRetention.Hours() / 24
	gbAfter := u.GBPerDay * recommended.Hours() / 24
	saving := (gb - gbAfter) * u.CostPerGBMonth
	reasons = append(reasons, fmt.Sprintf("%.0f GB → %.0f GB at £%.2f/GB/month", gb, gbAfter, u.CostPerGBMonth))
	return RetentionRecommendation{Topic: u.Topic, Current: u.CurrentRetention,
		Recommended: recommended, SavingGBPMonth: round2(saving), Reasoning: join(reasons)}
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += "; "
		}
		out += s
	}
	return out
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
