// Package incidentops implements Nexora's incident-operations platform:
//
//  20. Triage: signals (metric/log/trace/deploy/change) are ingested and
//     correlated by service + time window into incident candidates, with a
//     likely-cause ranking (a recent deploy before an error spike outranks
//     a bare spike) and a severity suggestion.
//
//  21. Impact calculator: an incident's services are mapped onto a provided
//     affected-service ledger snapshot to count customers, transactions
//     and the amount at risk.
//
//  22. Compensation: policy rules (outage-minutes threshold, eligibility,
//     amount) produce awards with duplicate-claim dedup, a fraud flag for
//     accounts that claim repeatedly, and a manual override path.
package incidentops

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

var (
	ErrUnknownCandidate = errors.New("unknown incident candidate")
	ErrUnknownPolicy    = errors.New("unknown compensation policy")
	ErrUnknownAward     = errors.New("unknown compensation award")
	ErrDuplicateClaim   = errors.New("duplicate claim key")
	ErrDuplicatePolicy  = errors.New("policy already exists")
)

// ── 20. Triage ──────────────────────────────────────────────────────────────

// Signal kinds.
const (
	KindMetric = "metric"
	KindLog    = "log"
	KindTrace  = "trace"
	KindDeploy = "deploy"
	KindChange = "change"
)

// Signal is one ingested observation.
type Signal struct {
	ID        string
	Service   string
	Kind      string
	Message   string
	IsError   bool
	Timestamp time.Time
}

// Candidate is one correlated incident candidate.
type Candidate struct {
	ID           string
	Service      string
	SignalIDs    []string
	Count        int
	WindowStart  time.Time
	WindowEnd    time.Time
	ErrorSpike   bool
	RecentDeploy bool
}

// RankedCause is one likely cause with a confidence score.
type RankedCause struct {
	Cause string
	Score float64
}

// TriageResult is the severity + likely-cause assessment.
type TriageResult struct {
	CandidateID  string
	Service      string
	Severity     string
	LikelyCauses []RankedCause
	SignalCount  int
	RecentDeploy bool
	ErrorSpike   bool
}

// Store holds signals, candidates and compensation state.
type Store struct {
	mu         sync.Mutex
	sigSeq     int
	candSeq    int
	signals    []Signal
	candidates map[string]*Candidate

	policies    map[string]*CompPolicy
	awards      map[string]*CompAward
	claimKeys   map[string]string // claim key → award id
	accountUses map[string]int
	awardSeq    int
}

// NewStore builds an empty store.
func NewStore() *Store {
	return &Store{
		candidates:  map[string]*Candidate{},
		policies:    map[string]*CompPolicy{},
		awards:      map[string]*CompAward{},
		claimKeys:   map[string]string{},
		accountUses: map[string]int{},
	}
}

func validKind(k string) bool {
	switch k {
	case KindMetric, KindLog, KindTrace, KindDeploy, KindChange:
		return true
	}
	return false
}

// Ingest records one signal.
func (s *Store) Ingest(service, kind, message string, isError bool, at time.Time) (Signal, error) {
	if service == "" {
		return Signal{}, errors.New("service is required")
	}
	if !validKind(kind) {
		return Signal{}, fmt.Errorf("unknown signal kind %q", kind)
	}
	if at.IsZero() {
		return Signal{}, errors.New("timestamp is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sigSeq++
	sig := Signal{ID: fmt.Sprintf("sig-%d", s.sigSeq), Service: service, Kind: kind, Message: message, IsError: isError, Timestamp: at}
	s.signals = append(s.signals, sig)
	return sig, nil
}

// Correlate groups signals by service inside the trailing window ending at
// now. One candidate per service with at least two signals; singletons are
// noise, not incidents.
func (s *Store) Correlate(window time.Duration, now time.Time) []Candidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	if window <= 0 {
		return nil
	}
	cutoff := now.Add(-window)
	byService := map[string][]Signal{}
	for _, sig := range s.signals {
		if sig.Timestamp.Before(cutoff) || sig.Timestamp.After(now) {
			continue
		}
		byService[sig.Service] = append(byService[sig.Service], sig)
	}
	names := make([]string, 0, len(byService))
	for n := range byService {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []Candidate
	for _, name := range names {
		sigs := byService[name]
		if len(sigs) < 2 {
			continue
		}
		s.candSeq++
		c := &Candidate{ID: fmt.Sprintf("cand-%d", s.candSeq), Service: name, WindowStart: cutoff, WindowEnd: now}
		errCount := 0
		for _, sg := range sigs {
			c.SignalIDs = append(c.SignalIDs, sg.ID)
			if sg.IsError || sg.Kind == KindMetric && isErrorMessage(sg.Message) {
				errCount++
			}
			if sg.Kind == KindDeploy || sg.Kind == KindChange {
				c.RecentDeploy = true
			}
		}
		c.Count = len(sigs)
		c.ErrorSpike = errCount >= 3
		cp := *c
		cp.SignalIDs = append([]string(nil), c.SignalIDs...)
		s.candidates[c.ID] = &cp
		out = append(out, cp)
	}
	return out
}

func isErrorMessage(m string) bool {
	for _, sub := range []string{"error", "5xx", "timeout", "fail"} {
		if len(m) >= len(sub) {
			for i := 0; i+len(sub) <= len(m); i++ {
				match := true
				for j := 0; j < len(sub); j++ {
					a, b := m[i+j], sub[j]
					if a >= 'A' && a <= 'Z' {
						a += 'a' - 'A'
					}
					if a != b {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false
}

// Triage assesses one candidate: severity from the error volume, likely
// causes ranked with deploy recency above a bare error spike.
func (s *Store) Triage(candidateID string) (TriageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.candidates[candidateID]
	if !ok {
		return TriageResult{}, ErrUnknownCandidate
	}
	errorsSeen := 0
	deployMsg := ""
	for _, id := range c.SignalIDs {
		for _, sg := range s.signals {
			if sg.ID != id {
				continue
			}
			if sg.IsError {
				errorsSeen++
			}
			if deployMsg == "" && (sg.Kind == KindDeploy || sg.Kind == KindChange) {
				deployMsg = sg.Message
			}
		}
	}
	res := TriageResult{
		CandidateID: c.ID, Service: c.Service,
		SignalCount: c.Count, RecentDeploy: c.RecentDeploy, ErrorSpike: c.ErrorSpike,
	}
	switch {
	case errorsSeen >= 5:
		res.Severity = "SEV1"
	case errorsSeen >= 3 || c.ErrorSpike:
		res.Severity = "SEV2"
	case errorsSeen >= 1:
		res.Severity = "SEV3"
	default:
		res.Severity = "SEV4"
	}
	if c.RecentDeploy {
		cause := "recent deploy/change in " + c.Service
		if deployMsg != "" {
			cause += " (" + deployMsg + ")"
		}
		res.LikelyCauses = append(res.LikelyCauses, RankedCause{Cause: cause, Score: 0.9})
	}
	if c.ErrorSpike || errorsSeen > 0 {
		res.LikelyCauses = append(res.LikelyCauses, RankedCause{
			Cause: fmt.Sprintf("error spike in %s (%d error signals)", c.Service, errorsSeen), Score: 0.7,
		})
	}
	if len(res.LikelyCauses) == 0 {
		res.LikelyCauses = append(res.LikelyCauses, RankedCause{Cause: "anomalous activity in " + c.Service, Score: 0.4})
	}
	return res, nil
}

// ── 21. Impact calculator ───────────────────────────────────────────────────

// ServiceLedger is one row of the affected-service ledger snapshot.
type ServiceLedger struct {
	Service      string
	Customers    int
	Transactions int
	AmountMinor  int64
}

// Impact is the summed customer/transaction/exposure assessment.
type Impact struct {
	Services          []string
	Customers         int
	Transactions      int
	AmountAtRiskMinor int64
}

// Calculate maps incident services onto the ledger snapshot.
func (s *Store) Calculate(services []string, snapshot []ServiceLedger) Impact {
	byService := map[string]ServiceLedger{}
	for _, row := range snapshot {
		byService[row.Service] = row
	}
	imp := Impact{}
	seen := map[string]bool{}
	for _, name := range services {
		if seen[name] {
			continue
		}
		seen[name] = true
		row, ok := byService[name]
		if !ok {
			continue
		}
		imp.Services = append(imp.Services, name)
		imp.Customers += row.Customers
		imp.Transactions += row.Transactions
		imp.AmountAtRiskMinor += row.AmountMinor
	}
	sort.Strings(imp.Services)
	if imp.Services == nil {
		imp.Services = []string{}
	}
	return imp
}

// ── 22. Compensation ────────────────────────────────────────────────────────

// CompPolicy is one compensation rule.
type CompPolicy struct {
	ID               string
	MinOutageMinutes int
	Eligibility      string // "all" or a tier name
	AmountMinor      int64
}

// CompAward is one granted award.
type CompAward struct {
	ID          string
	ClaimKey    string
	Account     string
	PolicyID    string
	AmountMinor int64
	Status      string // AWARDED / OVERRIDDEN / FLAGGED
	FraudFlag   bool
	DecidedAt   time.Time
}

// AddPolicy registers a compensation rule.
func (s *Store) AddPolicy(id string, minOutageMinutes int, eligibility string, amountMinor int64) error {
	if id == "" {
		return errors.New("policy id is required")
	}
	if minOutageMinutes < 0 {
		return errors.New("outage threshold must not be negative")
	}
	if eligibility == "" {
		return errors.New("eligibility is required")
	}
	if amountMinor <= 0 {
		return errors.New("amount must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.policies[id]; ok {
		return ErrDuplicatePolicy
	}
	s.policies[id] = &CompPolicy{ID: id, MinOutageMinutes: minOutageMinutes, Eligibility: eligibility, AmountMinor: amountMinor}
	return nil
}

// Evaluate finds the best matching policy for an outage.
func (s *Store) Evaluate(account string, outageMinutes int, tier string) (bool, int64, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *CompPolicy
	for _, p := range s.policies {
		if outageMinutes < p.MinOutageMinutes {
			continue
		}
		if p.Eligibility != "all" && p.Eligibility != tier {
			continue
		}
		if best == nil || p.AmountMinor > best.AmountMinor {
			best = p
		}
	}
	if best == nil {
		return false, 0, ""
	}
	_ = account
	return true, best.AmountMinor, best.ID
}

// Award grants compensation with duplicate-claim dedup; accounts with two
// or more prior awards are fraud-flagged for manual review.
func (s *Store) Award(claimKey, account string, outageMinutes int, tier string, now time.Time) (*CompAward, error) {
	if claimKey == "" || account == "" {
		return nil, errors.New("claim key and account are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.claimKeys[claimKey]; dup {
		return nil, ErrDuplicateClaim
	}
	var best *CompPolicy
	for _, p := range s.policies {
		if outageMinutes < p.MinOutageMinutes {
			continue
		}
		if p.Eligibility != "all" && p.Eligibility != tier {
			continue
		}
		if best == nil || p.AmountMinor > best.AmountMinor {
			best = p
		}
	}
	if best == nil {
		return nil, errors.New("no eligible compensation policy")
	}
	s.awardSeq++
	a := &CompAward{
		ID: s.fmtAwardID(), ClaimKey: claimKey, Account: account,
		PolicyID: best.ID, AmountMinor: best.AmountMinor,
		Status: "AWARDED", DecidedAt: now,
	}
	if s.accountUses[account] >= 2 {
		a.FraudFlag = true
		a.Status = "FLAGGED"
	}
	s.accountUses[account]++
	s.awards[a.ID] = a
	s.claimKeys[claimKey] = a.ID
	cp := *a
	return &cp, nil
}

func (s *Store) fmtAwardID() string { return fmt.Sprintf("award-%d", s.awardSeq) }

// Override lets an operator correct an award (amount and/or status).
func (s *Store) Override(awardID, approver string, newAmount *int64, newStatus string, now time.Time) (*CompAward, error) {
	if approver == "" {
		return nil, errors.New("approver is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.awards[awardID]
	if !ok {
		return nil, ErrUnknownAward
	}
	if newAmount != nil {
		if *newAmount <= 0 {
			return nil, errors.New("override amount must be positive")
		}
		a.AmountMinor = *newAmount
	}
	if newStatus != "" {
		a.Status = newStatus
	} else {
		a.Status = "OVERRIDDEN"
	}
	a.DecidedAt = now
	cp := *a
	return &cp, nil
}

// GetAward returns a copy of an award.
func (s *Store) GetAward(id string) (*CompAward, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.awards[id]
	if !ok {
		return nil, ErrUnknownAward
	}
	cp := *a
	return &cp, nil
}
