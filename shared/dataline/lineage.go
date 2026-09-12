// Package dataline implements Nexora's data-lineage, privacy-analytics and
// retention platform:
//
//  13. Lineage graph: UI/API/projection/event/source nodes plus edges with
//     Explain(valueID) returning the full chain oldest → newest.
//
//  14. Privacy analytics: per-purpose HMAC token vault for name/account/
//     address, aggregate-only cohort counts and an access audit log.
//
//  15. Retention engine: per-category policies, CREATED→ARCHIVED→
//     ANONYMISED→DELETED lifecycle with due-date computation and
//     legal-hold blocking.
package dataline

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrNotFound is returned when a node, value, token purpose, item or
	// hold is unknown.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned on duplicate nodes/edges/items, double
	// holds, or transitions blocked by a legal hold.
	ErrConflict = errors.New("conflict")
	// ErrInvalid is returned for bad kinds, categories, states,
	// multipliers or otherwise malformed input.
	ErrInvalid = errors.New("invalid request")
)

// ── 13. Lineage graph ───────────────────────────────────────────────────────

// Node kinds in the provenance chain.
const (
	KindUI         = "UI"
	KindAPI        = "API"
	KindProjection = "PROJECTION"
	KindEvent      = "EVENT"
	KindSource     = "SOURCE"
)

// Node is one provenance point for a logical value.
type Node struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`     // UI, API, PROJECTION, EVENT, SOURCE
	ValueID string    `json:"value_id"` // logical value this node describes
	At      time.Time `json:"at"`
}

// Edge links two nodes (from → to) in capture order.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Graph is the in-memory lineage store.
type Graph struct {
	mu    sync.Mutex
	nodes map[string]*Node
	edges []Edge
}

// NewGraph builds an empty lineage graph.
func NewGraph() *Graph {
	return &Graph{nodes: map[string]*Node{}}
}

func validKind(k string) bool {
	switch strings.ToUpper(strings.TrimSpace(k)) {
	case KindUI, KindAPI, KindProjection, KindEvent, KindSource:
		return true
	default:
		return false
	}
}

// RecordNode stores one lineage node.
func (g *Graph) RecordNode(n Node) (*Node, error) {
	if strings.TrimSpace(n.ID) == "" {
		return nil, fmt.Errorf("%w: node id is required", ErrInvalid)
	}
	if !validKind(n.Kind) {
		return nil, fmt.Errorf("%w: kind must be one of UI, API, PROJECTION, EVENT, SOURCE", ErrInvalid)
	}
	if strings.TrimSpace(n.ValueID) == "" {
		return nil, fmt.Errorf("%w: value_id is required", ErrInvalid)
	}
	if n.At.IsZero() {
		n.At = time.Now().UTC()
	}
	n.Kind = strings.ToUpper(strings.TrimSpace(n.Kind))
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, exists := g.nodes[n.ID]; exists {
		return nil, fmt.Errorf("node %q: %w", n.ID, ErrConflict)
	}
	cp := n
	g.nodes[n.ID] = &cp
	out := cp
	return &out, nil
}

// RecordEdge links two existing nodes.
func (g *Graph) RecordEdge(fromID, toID string) error {
	if strings.TrimSpace(fromID) == "" || strings.TrimSpace(toID) == "" {
		return fmt.Errorf("%w: from and to node ids are required", ErrInvalid)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[fromID]; !ok {
		return fmt.Errorf("node %q: %w", fromID, ErrNotFound)
	}
	if _, ok := g.nodes[toID]; !ok {
		return fmt.Errorf("node %q: %w", toID, ErrNotFound)
	}
	for _, e := range g.edges {
		if e.From == fromID && e.To == toID {
			return fmt.Errorf("edge %s->%s: %w", fromID, toID, ErrConflict)
		}
	}
	g.edges = append(g.edges, Edge{From: fromID, To: toID})
	return nil
}

// Explain returns the full chain for a value ordered oldest → newest by
// capture time (ID breaks ties for determinism).
func (g *Graph) Explain(valueID string) ([]Node, error) {
	if strings.TrimSpace(valueID) == "" {
		return nil, fmt.Errorf("%w: value is required", ErrInvalid)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Node
	for _, n := range g.nodes {
		if n.ValueID == valueID {
			out = append(out, *n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("value %q: %w", valueID, ErrNotFound)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if a.At.After(b.At) || (a.At.Equal(b.At) && a.ID > b.ID) {
				out[j-1], out[j] = out[j], out[j-1]
			} else {
				break
			}
		}
	}
	return out, nil
}

// Edges returns a copy of all recorded edges.
func (g *Graph) Edges() []Edge {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]Edge(nil), g.edges...)
}
