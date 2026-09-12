package dataline

import (
	"errors"
	"testing"
	"time"
)

func testPolicy() Policy {
	return Policy{MetadataYears: 7, TelemetryDays: 30, EvidenceYears: 7, AttachmentsDays: 90}
}

func TestPutPolicyValidation(t *testing.T) {
	e := NewEngine()
	if err := e.PutPolicy(Policy{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty policy, got %v", err)
	}
	if err := e.PutPolicy(testPolicy()); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
}

func TestClassifyAndGet(t *testing.T) {
	e := NewEngine()
	if err := e.PutPolicy(testPolicy()); err != nil {
		t.Fatal(err)
	}
	it, err := e.Classify("doc-1", "metadata", time.Now().UTC())
	if err != nil || it.State != StateCreated {
		t.Fatalf("Classify: %+v %v", it, err)
	}
	if _, err := e.Classify("doc-1", "metadata", time.Now().UTC()); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate, got %v", err)
	}
	if _, err := e.Classify("doc-2", "everything", time.Now().UTC()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for bad category, got %v", err)
	}
}

func TestLifecycleDueAndApply(t *testing.T) {
	e := NewEngine()
	if err := e.PutPolicy(Policy{MetadataYears: 7, TelemetryDays: 30, EvidenceYears: 7, AttachmentsDays: 90}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	old := now.Add(-31 * 24 * time.Hour) // older than 30-day telemetry horizon
	if _, err := e.Classify("tel-1", "telemetry", old); err != nil {
		t.Fatal(err)
	}
	due, err := e.DueTransitions(now)
	if err != nil || len(due) != 1 || due[0].To != StateArchived {
		t.Fatalf("DueTransitions = %+v %v", due, err)
	}
	moved, err := e.ApplyTransition("tel-1", now)
	if err != nil || moved.State != StateArchived {
		t.Fatalf("ApplyTransition = %+v %v", moved, err)
	}
	// Just transitioned: nothing due until another horizon passes.
	due, _ = e.DueTransitions(now)
	if len(due) != 0 {
		t.Fatalf("nothing should be due right after transition: %+v", due)
	}
	// Early transition must be rejected.
	if _, err := e.ApplyTransition("tel-1", now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for early transition, got %v", err)
	}
	// Walk the full lifecycle.
	later := now.Add(31 * 24 * time.Hour)
	if _, err := e.ApplyTransition("tel-1", later); err != nil {
		t.Fatalf("ARCHIVED->ANONYMISED: %v", err)
	}
	muchLater := later.Add(31 * 24 * time.Hour)
	moved, err = e.ApplyTransition("tel-1", muchLater)
	if err != nil || moved.State != StateDeleted {
		t.Fatalf("ANONYMISED->DELETED: %+v %v", moved, err)
	}
	if _, err := e.ApplyTransition("tel-1", muchLater.Add(time.Hour)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid after DELETED, got %v", err)
	}
}

func TestLegalHoldBlocks(t *testing.T) {
	e := NewEngine()
	if err := e.PutPolicy(testPolicy()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 365 * 24 * time.Hour) // older than the 7-year evidence horizon
	if _, err := e.Classify("ev-1", "evidence", old); err != nil {
		t.Fatal(err)
	}
	h, err := e.PlaceHold("ev-1", "FCA inquiry")
	if err != nil || h.ItemID != "ev-1" {
		t.Fatalf("PlaceHold: %+v %v", h, err)
	}
	if _, err := e.PlaceHold("ev-1", "again"); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on double hold, got %v", err)
	}
	// Held items are excluded from due lists and blocked on apply.
	due, _ := e.DueTransitions(now)
	for _, d := range due {
		if d.ID == "ev-1" {
			t.Fatalf("held item must not be due: %+v", due)
		}
	}
	if _, err := e.ApplyTransition("ev-1", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict (hold) on apply, got %v", err)
	}
	if err := e.ReleaseHold(h.ID); err != nil {
		t.Fatalf("ReleaseHold: %v", err)
	}
	moved, err := e.ApplyTransition("ev-1", now)
	if err != nil || moved.State != StateArchived {
		t.Fatalf("post-release apply: %+v %v", moved, err)
	}
	if err := e.ReleaseHold("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := e.PlaceHold("missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDueRequiresPolicy(t *testing.T) {
	e := NewEngine()
	if _, err := e.DueTransitions(time.Now().UTC()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid without policy, got %v", err)
	}
}
