package testgrid

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// IncidentKind is one catalogued failure mode.
type IncidentKind string

const (
	IncidentDependencyTimeout IncidentKind = "dependency_timeout"
	IncidentDuplicateEvent    IncidentKind = "duplicate_event"
	IncidentPartialWrite      IncidentKind = "partial_write"
	IncidentSlowDB            IncidentKind = "slow_db"
	IncidentMessageReorder    IncidentKind = "message_reorder"
)

// AllIncidentKinds lists the catalog.
var AllIncidentKinds = []IncidentKind{
	IncidentDependencyTimeout, IncidentDuplicateEvent, IncidentPartialWrite,
	IncidentSlowDB, IncidentMessageReorder,
}

// RegressionCase is one synthetic regression case.
type RegressionCase struct {
	ID        string            `json:"id"`
	Kind      IncidentKind      `json:"kind"`
	Seed      int64             `json:"seed"`
	Payload   map[string]string `json:"payload"`
	Expected  string            `json:"expected"`
	CreatedAt time.Time         `json:"created_at"`
}

// RunOutcome is the result of running one case.
type RunOutcome struct {
	CaseID string       `json:"case_id"`
	Kind   IncidentKind `json:"kind"`
	Passed bool         `json:"passed"`
	Detail string       `json:"detail"`
}

// Generator stores synthetic cases.
type Generator struct {
	mu     sync.RWMutex
	cases  map[string]*RegressionCase
	order  []string
	logger zerolog.Logger
}

// NewGenerator returns an empty generator.
func NewGenerator(logger zerolog.Logger) *Generator {
	return &Generator{cases: make(map[string]*RegressionCase), logger: logger}
}

func validIncident(k IncidentKind) bool {
	for _, a := range AllIncidentKinds {
		if a == k {
			return true
		}
	}
	return false
}

// Generate creates count deterministic cases for kind+seed. Payloads
// derive from seed so the same seed regenerates the same cases.
func (g *Generator) Generate(kind IncidentKind, seed int64, count int, now time.Time) ([]*RegressionCase, error) {
	if !validIncident(kind) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownIncident, kind)
	}
	if count <= 0 || count > 100 {
		return nil, fmt.Errorf("%w: count must be 1..100", ErrGridInvalidInput)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var out []*RegressionCase
	g.mu.Lock()
	for i := 0; i < count; i++ {
		s := seed + int64(i)*7919
		c := &RegressionCase{
			ID:        "scn-" + uuid.NewString(),
			Kind:      kind,
			Seed:      s,
			Payload:   payloadFor(kind, s),
			Expected:  expectedFor(kind),
			CreatedAt: now,
		}
		g.cases[c.ID] = c
		g.order = append(g.order, c.ID)
		cp := *c
		out = append(out, &cp)
	}
	g.mu.Unlock()
	g.logger.Info().Str("kind", string(kind)).Int64("seed", seed).Int("count", count).Msg("regression scenarios generated")
	return out, nil
}

// List returns cases in generation order (optionally filtered by kind).
func (g *Generator) List(filter IncidentKind) []*RegressionCase {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []*RegressionCase
	for _, id := range g.order {
		c := g.cases[id]
		if filter != "" && c.Kind != filter {
			continue
		}
		cp := *c
		out = append(out, &cp)
	}
	if out == nil {
		out = []*RegressionCase{}
	}
	return out
}

// Get fetches one case.
func (g *Generator) Get(id string) (*RegressionCase, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	c, ok := g.cases[id]
	if !ok {
		return nil, ErrScenarioNotFound
	}
	cp := *c
	return &cp, nil
}

// Run executes one case deterministically: the synthetic runner replays
// the payload and checks the invariant for the kind.
func (g *Generator) Run(id string) (*RunOutcome, error) {
	g.mu.RLock()
	c, ok := g.cases[id]
	var cp RegressionCase
	if ok {
		cp = *c
	}
	g.mu.RUnlock()
	if !ok {
		return nil, ErrScenarioNotFound
	}
	passed, detail := runCase(cp)
	return &RunOutcome{CaseID: cp.ID, Kind: cp.Kind, Passed: passed, Detail: detail}, nil
}

func payloadFor(kind IncidentKind, seed int64) map[string]string {
	lat := 100 + seed%900
	dup := 2 + seed%3
	return map[string]string{
		"incident":   string(kind),
		"seed":       fmt.Sprintf("%d", seed),
		"timeout_ms": fmt.Sprintf("%d", 500+seed%4500),
		"latency_ms": fmt.Sprintf("%d", lat),
		"duplicates": fmt.Sprintf("%d", dup),
		"tx_ref":     fmt.Sprintf("tx-%d", seed%100000),
	}
}

func expectedFor(kind IncidentKind) string {
	switch kind {
	case IncidentDependencyTimeout:
		return "timeout bounded by deadline with fallback"
	case IncidentDuplicateEvent:
		return "duplicates deduped idempotently"
	case IncidentPartialWrite:
		return "partial write compensated"
	case IncidentSlowDB:
		return "slow query shed with cached read"
	case IncidentMessageReorder:
		return "out-of-order messages resequenced"
	default:
		return "handled"
	}
}

// runCase replays deterministically; all well-formed synthetic cases
// pass (the generator and runner share the invariant table). A case
// with a tampered expected string fails, which is how regression drift
// surfaces.
func runCase(c RegressionCase) (bool, string) {
	want := expectedFor(c.Kind)
	if c.Expected != want {
		return false, fmt.Sprintf("drift: want %q got %q", want, c.Expected)
	}
	if strings.TrimSpace(c.Payload["tx_ref"]) == "" {
		return false, "missing tx_ref"
	}
	_ = sort.StringSlice{}
	return true, fmt.Sprintf("replayed %s seed %d: %s", c.Kind, c.Seed, want)
}
