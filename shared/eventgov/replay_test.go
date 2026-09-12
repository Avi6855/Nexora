package eventgov

import (
	"errors"
	"testing"
	"time"
)

func mustCapture(t *testing.T, lab *Lab, scenario, typ string) {
	t.Helper()
	if err := lab.Capture(scenario, Envelope{
		Type:          typ,
		PayloadHash:   "hash-" + typ,
		SchemaVersion: 1,
		At:            time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Capture: %v", err)
	}
}

func TestCaptureValidation(t *testing.T) {
	lab := NewLab()
	if err := lab.Capture("", Envelope{Type: "a", PayloadHash: "h"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty scenario, got %v", err)
	}
	if err := lab.Capture("s", Envelope{PayloadHash: "h"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty type, got %v", err)
	}
	if err := lab.Capture("s", Envelope{Type: "a"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty payload_hash, got %v", err)
	}
	if err := lab.Capture("s", Envelope{Type: "a", PayloadHash: "h", SchemaVersion: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for negative schema_version, got %v", err)
	}
}

func TestStartRunFanOut(t *testing.T) {
	lab := NewLab()
	mustCapture(t, lab, "checkout", "payment.created")
	mustCapture(t, lab, "checkout", "payment.created")
	mustCapture(t, lab, "checkout", "fraud.checked")

	run, err := lab.StartRun("checkout", 10)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if run.Delivered != 30 {
		t.Fatalf("delivered = %d, want 30", run.Delivered)
	}
	if run.PerType["payment.created"] != 20 {
		t.Fatalf("payment.created = %d, want 20", run.PerType["payment.created"])
	}
	if run.PerType["fraud.checked"] != 10 {
		t.Fatalf("fraud.checked = %d, want 10", run.PerType["fraud.checked"])
	}
	if run.Multiplier != 10 {
		t.Fatalf("multiplier = %d, want 10", run.Multiplier)
	}
	if run.ID == "" || run.Scenario != "checkout" {
		t.Fatalf("bad run identity: %+v", run)
	}
}

func TestStartRunRejectsBadMultiplierAndUnknownScenario(t *testing.T) {
	lab := NewLab()
	mustCapture(t, lab, "s", "a")
	if _, err := lab.StartRun("s", 7); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for multiplier 7, got %v", err)
	}
	if _, err := lab.StartRun("missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown scenario, got %v", err)
	}
}

func TestStartRunAllMultipliers(t *testing.T) {
	for _, m := range []int{1, 10, 100, 1000} {
		lab := NewLab()
		mustCapture(t, lab, "s", "a")
		mustCapture(t, lab, "s", "b")
		run, err := lab.StartRun("s", m)
		if err != nil {
			t.Fatalf("multiplier %d: %v", m, err)
		}
		if run.Delivered != 2*m {
			t.Fatalf("multiplier %d: delivered = %d, want %d", m, run.Delivered, 2*m)
		}
	}
}

func TestGetAndListRuns(t *testing.T) {
	lab := NewLab()
	mustCapture(t, lab, "a", "x")
	mustCapture(t, lab, "b", "y")
	r1, _ := lab.StartRun("a", 1)
	r2, _ := lab.StartRun("b", 1)
	got, err := lab.GetRun(r1.ID)
	if err != nil || got.Scenario != "a" {
		t.Fatalf("GetRun: %+v %v", got, err)
	}
	if _, err := lab.GetRun("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if all := lab.ListRuns(""); len(all) != 2 {
		t.Fatalf("ListRuns all = %d, want 2", len(all))
	}
	if one := lab.ListRuns("a"); len(one) != 1 || one[0].ID != r1.ID {
		t.Fatalf("ListRuns(a) = %+v, want [%s]", one, r1.ID)
	}
	_ = r2
}

func TestDiffRunsAddedRemoved(t *testing.T) {
	lab := NewLab()
	mustCapture(t, lab, "base", "payment.created")
	mustCapture(t, lab, "base", "fraud.checked")
	base, _ := lab.StartRun("base", 1)

	mustCapture(t, lab, "cand", "payment.created")
	mustCapture(t, lab, "cand", "payment.created")
	mustCapture(t, lab, "cand", "ledger.posted")
	cand, _ := lab.StartRun("cand", 1)

	diff, err := lab.DiffRuns(base.ID, cand.ID)
	if err != nil {
		t.Fatalf("DiffRuns: %v", err)
	}
	if diff.Added["payment.created"] != 1 {
		t.Fatalf("added payment.created = %d, want 1", diff.Added["payment.created"])
	}
	if diff.Added["ledger.posted"] != 1 {
		t.Fatalf("added ledger.posted = %d, want 1", diff.Added["ledger.posted"])
	}
	if diff.Removed["fraud.checked"] != 1 {
		t.Fatalf("removed fraud.checked = %d, want 1", diff.Removed["fraud.checked"])
	}
}

func TestDiffRunsIdenticalIsEmpty(t *testing.T) {
	lab := NewLab()
	mustCapture(t, lab, "s", "a")
	r1, _ := lab.StartRun("s", 1)
	r2, _ := lab.StartRun("s", 1)
	diff, err := lab.DiffRuns(r1.ID, r2.ID)
	if err != nil {
		t.Fatalf("DiffRuns: %v", err)
	}
	if len(diff.Added) != 0 || len(diff.Removed) != 0 {
		t.Fatalf("identical runs should diff empty, got %+v", diff)
	}
}

func TestDiffRunsMissing(t *testing.T) {
	lab := NewLab()
	mustCapture(t, lab, "s", "a")
	r, _ := lab.StartRun("s", 1)
	if _, err := lab.DiffRuns("missing", r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing base, got %v", err)
	}
	if _, err := lab.DiffRuns(r.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing candidate, got %v", err)
	}
}
