package taxpack

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTotalsMath(t *testing.T) {
	s := NewStore()
	d := func(y, m, day int) time.Time { return time.Date(y, time.Month(m), day, 12, 0, 0, 0, time.UTC) }
	if _, err := s.PostEntry("INTEREST", 10000, d(2024, 6, 1), "doc-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostEntry("INTEREST", 5000, d(2024, 7, 1), "doc-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostEntry("DIVIDENDS", 20000, d(2024, 8, 1), ""); err != nil {
		t.Fatal(err)
	}
	pack, err := s.BuildPack(2024)
	if err != nil {
		t.Fatal(err)
	}
	if pack.Totals[CategoryInterest] != 15000 {
		t.Fatalf("interest = %d, want 15000", pack.Totals[CategoryInterest])
	}
	if pack.Totals[CategoryDividends] != 20000 {
		t.Fatalf("dividends = %d", pack.Totals[CategoryDividends])
	}
	if pack.Status != StatusDraft || pack.Label != "2024/25" {
		t.Fatalf("pack = %+v", pack)
	}
	raw, err := s.RenderJSON(2024)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	csvOut, err := s.RenderCSV(2024)
	if err != nil || !strings.Contains(csvOut, "INTEREST,15000") {
		t.Fatalf("csv = %q (%v)", csvOut, err)
	}
	m, err := s.Manifest(2024)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.DocIDs) != 2 || m.Count != 3 {
		t.Fatalf("manifest = %+v", m)
	}
}

func TestYearBoundaries(t *testing.T) {
	s := NewStore()
	// 5 Apr 2024 12:00 belongs to 2023/24; 6 Apr 2024 belongs to 2024/25.
	if _, err := s.PostEntry("DONATIONS", 1000, time.Date(2024, 4, 5, 12, 0, 0, 0, time.UTC), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PostEntry("DONATIONS", 2000, time.Date(2024, 4, 6, 0, 0, 1, 0, time.UTC), ""); err != nil {
		t.Fatal(err)
	}
	p2324, err := s.BuildPack(2023)
	if err != nil {
		t.Fatal(err)
	}
	if p2324.Totals[CategoryDonations] != 1000 {
		t.Fatalf("2023/24 donations = %d, want 1000", p2324.Totals[CategoryDonations])
	}
	p2425, err := s.BuildPack(2024)
	if err != nil {
		t.Fatal(err)
	}
	if p2425.Totals[CategoryDonations] != 2000 {
		t.Fatalf("2024/25 donations = %d, want 2000", p2425.Totals[CategoryDonations])
	}
	if _, err := s.PostEntry("BOGUS", 100, time.Now().UTC(), ""); err == nil {
		t.Fatal("expected invalid category")
	}
}

func TestFinalizedImmutability(t *testing.T) {
	s := NewStore()
	d := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	if _, err := s.PostEntry("EXPENSES", 7000, d, "doc-9"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BuildPack(2024); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finalize(2024); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Finalize(2024); !errors.Is(err, ErrFinalized) {
		t.Fatalf("double finalize = %v, want ErrFinalized", err)
	}
	if _, err := s.BuildPack(2024); !errors.Is(err, ErrFinalized) {
		t.Fatalf("rebuild finalized = %v, want ErrFinalized", err)
	}
	if _, err := s.PostEntry("EXPENSES", 100, d, ""); !errors.Is(err, ErrFinalized) {
		t.Fatalf("post into finalized = %v, want ErrFinalized", err)
	}
	m, err := s.Manifest(2024)
	if err != nil || m.Status != StatusFinalized {
		t.Fatalf("manifest = %+v %v", m, err)
	}
	if _, err := s.GetPack(2099); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing pack = %v, want ErrNotFound", err)
	}
}
