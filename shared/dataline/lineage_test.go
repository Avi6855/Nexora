package dataline

import (
	"errors"
	"testing"
	"time"
)

func TestRecordNodeAndExplainOrder(t *testing.T) {
	g := NewGraph()
	base := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	nodes := []Node{
		{ID: "n-ui", Kind: "UI", ValueID: "v1", At: base.Add(3 * time.Hour)},
		{ID: "n-src", Kind: "SOURCE", ValueID: "v1", At: base},
		{ID: "n-api", Kind: "api", ValueID: "v1", At: base.Add(time.Hour)},
		{ID: "n-evt", Kind: "EVENT", ValueID: "v1", At: base.Add(2 * time.Hour)},
	}
	for _, n := range nodes {
		if _, err := g.RecordNode(n); err != nil {
			t.Fatalf("RecordNode %s: %v", n.ID, err)
		}
	}
	chain, err := g.Explain("v1")
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	want := []string{"n-src", "n-api", "n-evt", "n-ui"}
	if len(chain) != len(want) {
		t.Fatalf("chain len = %d, want %d", len(chain), len(want))
	}
	for i, id := range want {
		if chain[i].ID != id {
			t.Fatalf("chain[%d] = %s, want %s (full %+v)", i, chain[i].ID, id, chain)
		}
	}
}

func TestRecordNodeValidation(t *testing.T) {
	g := NewGraph()
	if _, err := g.RecordNode(Node{Kind: KindAPI, ValueID: "v"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty id, got %v", err)
	}
	if _, err := g.RecordNode(Node{ID: "n", Kind: "PORTAL", ValueID: "v"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for bad kind, got %v", err)
	}
	if _, err := g.RecordNode(Node{ID: "n", Kind: KindAPI}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty value, got %v", err)
	}
	if _, err := g.RecordNode(Node{ID: "n", Kind: KindSource, ValueID: "v"}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if _, err := g.RecordNode(Node{ID: "n", Kind: KindAPI, ValueID: "v2"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate id, got %v", err)
	}
}

func TestRecordEdge(t *testing.T) {
	g := NewGraph()
	if _, err := g.RecordNode(Node{ID: "a", Kind: KindSource, ValueID: "v"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordNode(Node{ID: "b", Kind: KindEvent, ValueID: "v"}); err != nil {
		t.Fatal(err)
	}
	if err := g.RecordEdge("a", "b"); err != nil {
		t.Fatalf("RecordEdge: %v", err)
	}
	if err := g.RecordEdge("a", "b"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate edge, got %v", err)
	}
	if err := g.RecordEdge("a", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := g.RecordEdge("missing", "b"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestExplainNotFound(t *testing.T) {
	g := NewGraph()
	if _, err := g.Explain("nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := g.Explain(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
}

func TestExplainIsolatesValues(t *testing.T) {
	g := NewGraph()
	if _, err := g.RecordNode(Node{ID: "a", Kind: KindSource, ValueID: "v1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.RecordNode(Node{ID: "b", Kind: KindAPI, ValueID: "v2"}); err != nil {
		t.Fatal(err)
	}
	chain, err := g.Explain("v2")
	if err != nil || len(chain) != 1 || chain[0].ID != "b" {
		t.Fatalf("Explain(v2) = %+v %v", chain, err)
	}
}
