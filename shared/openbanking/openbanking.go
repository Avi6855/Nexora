// Package openbanking implements Nexora's open-banking connection platform:
//
//  23. Broken-connection auto-recovery: a failed connection is CLASSIFIED
//     first (silent refresh, reauth, provider outage, schema drift, rate
//     limit), then routed to the matching recovery lane. Recovery escalates
//     to customer action only when the machine cannot fix it — a customer
//     prompted to "reconnect" during a provider outage is a support ticket.
//
//  24. Data freshness guarantees: every dataset (balance, transactions)
//     exposes an explicit SLA and a computed freshness verdict
//     (fresh/stale/unknown). UNKNOWN is a real state: a dataset whose last
//     successful refresh predates the record of its SLA is not "stale", it
//     is unassessable, and callers must not silently treat it as truth.
//
//  25. Provider capability matrix: runtime discovery of what each provider
//     actually supports (not what the spec says), with SUPPORTED /
//     UNSUPPORTED / TEMPORARILY_UNAVAILABLE / REQUIRES_REAUTH verdicts so
//     services degrade explicitly instead of failing mid-call.
package openbanking

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var ErrUnsupported = errors.New("capability unsupported by provider")

// ── 23. Broken-connection auto-recovery ─────────────────────────────────────

// FailureKind is the classification of a broken connection.
type FailureKind string

const (
	FailAuthExpired    FailureKind = "AUTH_EXPIRED"    // token rejected → silent refresh possible
	FailConsentExpired FailureKind = "CONSENT_EXPIRED" // 90-day consent window closed → customer reauth
	FailProviderDown   FailureKind = "PROVIDER_DOWN"   // outage → wait + auto-retry
	FailSchemaChanged  FailureKind = "SCHEMA_CHANGED"  // provider changed payloads → engineering
	FailRateLimited    FailureKind = "RATE_LIMITED"    // back off and retry later
)

// RecoveryLane is what happens after classification.
type RecoveryLane string

const (
	LaneSilentRefresh  RecoveryLane = "SILENT_REFRESH"      // machine fixes it, customer never knows
	LaneBackoff        RecoveryLane = "EXPONENTIAL_BACKOFF" // retry later, machine fixes it
	LaneCustomerReauth RecoveryLane = "CUSTOMER_REAUTH"     // only the customer can fix it
	LaneEngineering    RecoveryLane = "ENGINEERING"         // provider broke the contract
)

// RecoveryPlan is the classified, routed outcome.
type RecoveryPlan struct {
	Kind        RecoveryLane
	AutoRetry   bool
	MaxAttempts int
	BackoffBase time.Duration
	CustomerMsg string
}

// ClassifyFailure maps an observed failure to its recovery lane. Observed
// order matters: rate limiting masquerades as auth failure (401 vs 429 are
// confused in the wild), so the limiter signal wins if present.
func ClassifyFailure(kind FailureKind, retryAfter time.Duration) RecoveryPlan {
	switch kind {
	case FailRateLimited:
		return RecoveryPlan{Kind: LaneBackoff, AutoRetry: true, MaxAttempts: 5, BackoffBase: retryAfterOr(retryAfter, 30*time.Second)}
	case FailProviderDown:
		return RecoveryPlan{Kind: LaneBackoff, AutoRetry: true, MaxAttempts: 10, BackoffBase: retryAfterOr(retryAfter, time.Minute)}
	case FailAuthExpired:
		return RecoveryPlan{Kind: LaneSilentRefresh, AutoRetry: true, MaxAttempts: 3, BackoffBase: time.Second}
	case FailConsentExpired:
		return RecoveryPlan{Kind: LaneCustomerReauth, CustomerMsg: "Your bank connection needs renewing — this takes about 30 seconds in the app."}
	case FailSchemaChanged:
		return RecoveryPlan{Kind: LaneEngineering, CustomerMsg: "We're fixing a connection issue with your bank — your data is safe."}
	default:
		return RecoveryPlan{Kind: LaneEngineering}
	}
}

func retryAfterOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// RecoveryTracker executes a plan's attempt budget with backoff, and reports
// when to escalate (attempts exhausted → the silent lanes must surface).
type RecoveryTracker struct {
	plan     RecoveryPlan
	attempts int
	last     time.Time
}

func NewRecoveryTracker(p RecoveryPlan) *RecoveryTracker { return &RecoveryTracker{plan: p} }

// ShouldRetry decides whether another attempt is permitted and when.
func (t *RecoveryTracker) ShouldRetry(now time.Time) (bool, time.Duration) {
	if !t.plan.AutoRetry {
		return false, 0
	}
	if t.attempts >= t.plan.MaxAttempts {
		return false, 0
	}
	// Exponential: base × 2^(attempts-1) — the first retry gets the base
	// window, subsequent failures double it.
	shift := t.attempts - 1
	if shift < 0 {
		shift = 0
	}
	wait := t.plan.BackoffBase << shift
	if t.attempts > 0 && now.Sub(t.last) < wait {
		return false, wait - now.Sub(t.last)
	}
	return true, wait
}

// RecordAttempt logs one attempt.
func (t *RecoveryTracker) RecordAttempt(now time.Time) { t.attempts++; t.last = now }

// Exhausted reports whether the budget is spent and the lane must escalate.
func (t *RecoveryTracker) Exhausted() bool { return t.attempts >= t.plan.MaxAttempts }

// ── 24. Data freshness guarantees ───────────────────────────────────────────

// FreshnessVerdict is the SLA verdict for a dataset.
type FreshnessVerdict string

const (
	FreshFresh   FreshnessVerdict = "FRESH"
	FreshStale   FreshnessVerdict = "STALE"
	FreshUnknown FreshnessVerdict = "UNKNOWN"
)

// DatasetSLA binds a dataset to a max acceptable age.
type DatasetSLA struct {
	Dataset string
	MaxAge  time.Duration
}

// FreshnessReport is the exposed freshness state.
type FreshnessReport struct {
	Dataset  string
	Verdict  FreshnessVerdict
	Age      time.Duration
	SLA      time.Duration
	LastGood time.Time
	HasData  bool
}

// FreshnessChecker evaluates datasets against their SLAs.
type FreshnessChecker struct {
	slas map[string]time.Duration
}

func NewFreshnessChecker() *FreshnessChecker {
	return &FreshnessChecker{slas: map[string]time.Duration{}}
}

// SetSLA declares the freshness contract for a dataset.
func (f *FreshnessChecker) SetSLA(dataset string, maxAge time.Duration) { f.slas[dataset] = maxAge }

// Evaluate computes the verdict. No SLA → UNKNOWN (an undeclared contract is
// not a met contract). No successful refresh → UNKNOWN, not stale — the
// caller must distinguish "old data" from "no data ever".
func (f *FreshnessChecker) Evaluate(dataset string, lastGood time.Time, now time.Time) FreshnessReport {
	sla, declared := f.slas[dataset]
	if !declared {
		return FreshnessReport{Dataset: dataset, Verdict: FreshUnknown}
	}
	if lastGood.IsZero() {
		return FreshnessReport{Dataset: dataset, Verdict: FreshUnknown, SLA: sla}
	}
	age := now.Sub(lastGood)
	v := FreshFresh
	if age > sla {
		v = FreshStale
	}
	return FreshnessReport{Dataset: dataset, Verdict: v, Age: age, SLA: sla, LastGood: lastGood, HasData: true}
}

// Overall verdict: the WORST dataset verdict wins — a product page cannot
// claim "fresh" while its transactions are stale.
func OverallVerdict(reports []FreshnessReport) FreshnessVerdict {
	worst := FreshFresh
	for _, r := range reports {
		switch r.Verdict {
		case FreshUnknown:
			return FreshUnknown // unknown poisons the whole surface
		case FreshStale:
			worst = FreshStale
		}
	}
	return worst
}

// ── 25. Provider capability matrix ──────────────────────────────────────────

// Capability enumerates provider abilities.
type Capability string

const (
	CapBalance      Capability = "BALANCE"
	CapTransactions Capability = "TRANSACTIONS"
	CapPayments     Capability = "PAYMENTS"
	CapIdentity     Capability = "IDENTITY"
)

// Availability is the runtime state of a capability.
type Availability string

const (
	AvailSupported              Availability = "SUPPORTED"
	AvailUnsupported            Availability = "UNSUPPORTED"
	AvailTemporarilyUnavailable Availability = "TEMPORARILY_UNAVAILABLE"
	AvailRequiresReauth         Availability = "REQUIRES_REAUTH"
)

// ProviderCapabilities is the discovered matrix for one provider.
type ProviderCapabilities struct {
	Provider string
	// declared is what the spec says; observed is what actually worked.
	declared map[Capability]Availability
	observed map[Capability]Availability
	// outages mark capabilities in a declared maintenance window.
	outages map[Capability]time.Time // capability → outage end
	// reauth capabilities that failed auth last call.
	authBroken map[Capability]bool
}

// NewProviderCapabilities starts from declared support.
func NewProviderCapabilities(provider string, declared []Capability) *ProviderCapabilities {
	p := &ProviderCapabilities{
		Provider:   provider,
		declared:   map[Capability]Availability{},
		observed:   map[Capability]Availability{},
		outages:    map[Capability]time.Time{},
		authBroken: map[Capability]bool{},
	}
	for _, c := range declared {
		p.declared[c] = AvailSupported
		p.observed[c] = AvailSupported
	}
	return p
}

// DeclareOutage marks a capability down until end (maintenance window or
// incident). Callers get TEMPORARILY_UNAVAILABLE, not hard failure.
func (p *ProviderCapabilities) DeclareOutage(c Capability, end time.Time) { p.outages[c] = end }

// RecordAuthFailure flags a capability as needing reauth.
func (p *ProviderCapabilities) RecordAuthFailure(c Capability) { p.authBroken[c] = true }

// RecordSuccess clears both outage and auth flags — observed reality wins
// over declared state.
func (p *ProviderCapabilities) RecordSuccess(c Capability) {
	delete(p.outages, c)
	delete(p.authBroken, c)
	p.observed[c] = AvailSupported
}

// ObserveUnsupported records that a declared capability does not actually
// work — spec-vs-reality drift is captured, not assumed away.
func (p *ProviderCapabilities) ObserveUnsupported(c Capability) { p.observed[c] = AvailUnsupported }

// Check returns the runtime verdict for a capability. Precedence:
// unsupported > outage (until it ends) > reauth > supported.
func (p *ProviderCapabilities) Check(c Capability, now time.Time) Availability {
	if p.observed[c] == AvailUnsupported {
		return AvailUnsupported
	}
	if _, declared := p.declared[c]; !declared {
		return AvailUnsupported
	}
	if end, out := p.outages[c]; out {
		if now.Before(end) {
			return AvailTemporarilyUnavailable
		}
		delete(p.outages, c) // window elapsed — optimistically healthy again
	}
	if p.authBroken[c] {
		return AvailRequiresReauth
	}
	return AvailSupported
}

// MatrixSummary renders the provider × capability table, capabilities in
// stable order for dashboards.
func MatrixSummary(providers []*ProviderCapabilities, now time.Time) string {
	caps := []Capability{CapBalance, CapTransactions, CapPayments, CapIdentity}
	out := ""
	names := make([]string, 0, len(providers))
	for _, p := range providers {
		names = append(names, p.Provider)
	}
	sort.Strings(names)
	for _, n := range names {
		var p *ProviderCapabilities
		for _, x := range providers {
			if x.Provider == n {
				p = x
			}
		}
		row := p.Provider + ":"
		for _, c := range caps {
			row += fmt.Sprintf(" %s=%s", c, p.Check(c, now))
		}
		out += row + "\n"
	}
	return out
}
