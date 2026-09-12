// Package eventgov implements Nexora's event-governance platform:
//
//  11. Traffic replay lab: capture sanitized envelopes per scenario and
//     replay them at 1x/10x/100x/1000x fan-out with per-run stats and
//     baseline-vs-candidate regression diffs.
//
//  12. Schema compatibility: producer schema registry with BACKWARD /
//     FORWARD / FULL checks, field deprecation and additive-only migration
//     plans.
package eventgov

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned when a scenario, run or schema is unknown.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned on duplicate IDs or schema versions.
	ErrConflict = errors.New("conflict")
	// ErrInvalid is returned when the caller supplies a bad multiplier,
	// empty scenario/type, or otherwise malformed input.
	ErrInvalid = errors.New("invalid request")
)

// ── 11. Traffic replay lab ──────────────────────────────────────────────────

// Envelope is a sanitized captured event: the type, a hash of the payload
// (never the payload itself), the writer schema version and capture time.
type Envelope struct {
	Type          string    `json:"type"`
	PayloadHash   string    `json:"payload_hash"`
	SchemaVersion int       `json:"schema_version"`
	At            time.Time `json:"at"`
}

// Run is one replay execution over a scenario's captures.
type Run struct {
	ID         string         `json:"id"`
	Scenario   string         `json:"scenario"`
	Multiplier int            `json:"multiplier"`
	Delivered  int            `json:"delivered"`
	PerType    map[string]int `json:"per_type"`
	Elapsed    time.Duration  `json:"elapsed_nanos"`
	At         time.Time      `json:"at"`
}

// RunDiff is the regression comparison of a candidate run against a
// baseline run. Added holds per-type counts present in the candidate
// beyond the baseline; Removed holds counts present in the baseline
// beyond the candidate. Both maps omit zero entries, so an empty diff
// means the runs are type-identical.
type RunDiff struct {
	BaseID      string         `json:"base_id"`
	CandidateID string         `json:"candidate_id"`
	Added       map[string]int `json:"added"`
	Removed     map[string]int `json:"removed"`
}

// Lab stores sanitized captures per scenario and the runs derived from
// them. Multipliers are simulated via batch fan-out counts — no sleeping —
// so tests stay fast and deterministic.
type Lab struct {
	mu       sync.Mutex
	captures map[string][]Envelope
	runs     map[string]*Run
	seq      int
}

// NewLab builds an empty replay lab.
func NewLab() *Lab {
	return &Lab{
		captures: map[string][]Envelope{},
		runs:     map[string]*Run{},
	}
}

// Capture stores one sanitized envelope under a scenario.
func (l *Lab) Capture(scenario string, env Envelope) error {
	if strings.TrimSpace(scenario) == "" {
		return fmt.Errorf("%w: scenario is required", ErrInvalid)
	}
	if strings.TrimSpace(env.Type) == "" {
		return fmt.Errorf("%w: envelope type is required", ErrInvalid)
	}
	if strings.TrimSpace(env.PayloadHash) == "" {
		return fmt.Errorf("%w: payload_hash is required", ErrInvalid)
	}
	if env.SchemaVersion < 0 {
		return fmt.Errorf("%w: schema_version must be >= 0", ErrInvalid)
	}
	if env.At.IsZero() {
		env.At = time.Now().UTC()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.captures[scenario] = append(l.captures[scenario], env)
	return nil
}

// validMultiplier reports whether m is an allowed fan-out.
func validMultiplier(m int) bool {
	switch m {
	case 1, 10, 100, 1000:
		return true
	default:
		return false
	}
}

// StartRun replays a scenario's captures at the given multiplier. Each
// captured envelope is fanned out `multiplier` times, so delivered =
// len(captures)*multiplier and per-type counts scale identically. Elapsed
// is the measured wall time of the in-memory fan-out (no sleeping).
func (l *Lab) StartRun(scenario string, multiplier int) (*Run, error) {
	if strings.TrimSpace(scenario) == "" {
		return nil, fmt.Errorf("%w: scenario is required", ErrInvalid)
	}
	if !validMultiplier(multiplier) {
		return nil, fmt.Errorf("%w: multiplier must be one of 1, 10, 100, 1000, got %d", ErrInvalid, multiplier)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	caps, ok := l.captures[scenario]
	if !ok || len(caps) == 0 {
		return nil, fmt.Errorf("scenario %q: %w", scenario, ErrNotFound)
	}
	start := time.Now().UTC()
	perType := map[string]int{}
	delivered := 0
	for _, c := range caps {
		perType[c.Type] += multiplier
		delivered += multiplier
	}
	elapsed := time.Since(start)
	l.seq++
	run := &Run{
		ID:         fmt.Sprintf("run-%d", l.seq),
		Scenario:   scenario,
		Multiplier: multiplier,
		Delivered:  delivered,
		PerType:    perType,
		Elapsed:    elapsed,
		At:         time.Now().UTC(),
	}
	l.runs[run.ID] = run
	out := *run
	out.PerType = cloneCounts(run.PerType)
	return &out, nil
}

// GetRun returns one run by ID.
func (l *Lab) GetRun(id string) (*Run, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.runs[id]
	if !ok {
		return nil, fmt.Errorf("run %q: %w", id, ErrNotFound)
	}
	out := *r
	out.PerType = cloneCounts(r.PerType)
	return &out, nil
}

// ListRuns returns runs in ID order. An empty scenario returns every run;
// otherwise only runs for that scenario are returned.
func (l *Lab) ListRuns(scenario string) []Run {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Run
	for _, r := range l.runs {
		if scenario != "" && r.Scenario != scenario {
			continue
		}
		cp := *r
		cp.PerType = cloneCounts(r.PerType)
		out = append(out, cp)
	}
	// Deterministic order for tests: sort by ID.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].ID > out[j].ID; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// DiffRuns compares a candidate run against a baseline run.
func (l *Lab) DiffRuns(baseID, candidateID string) (*RunDiff, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	base, ok := l.runs[baseID]
	if !ok {
		return nil, fmt.Errorf("base run %q: %w", baseID, ErrNotFound)
	}
	cand, ok := l.runs[candidateID]
	if !ok {
		return nil, fmt.Errorf("candidate run %q: %w", candidateID, ErrNotFound)
	}
	added := map[string]int{}
	removed := map[string]int{}
	for typ, c := range cand.PerType {
		if d := c - base.PerType[typ]; d > 0 {
			added[typ] = d
		} else if d < 0 {
			removed[typ] = -d
		}
	}
	for typ, b := range base.PerType {
		if _, seen := cand.PerType[typ]; !seen && b > 0 {
			removed[typ] = b
		}
	}
	return &RunDiff{
		BaseID:      baseID,
		CandidateID: candidateID,
		Added:       added,
		Removed:     removed,
	}, nil
}

func cloneCounts(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
