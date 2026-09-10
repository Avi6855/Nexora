package statements

import (
	"strings"
	"testing"
	"time"
)

var cutoff = time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC)

func sampleLines() []Line {
	return []Line{
		{Date: time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), Description: "SALARY", AmountMinor: 250000, Currency: "GBP", BalanceMinor: 300000, Ref: "tx-1"},
		{Date: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC), Description: "TESCO", AmountMinor: -4820, Currency: "GBP", BalanceMinor: 295180, Ref: "tx-2"},
	}
}

func TestVersioningAndImmutability(t *testing.T) {
	s, err := Generate("acc-1", "2026-08", cutoff, sampleLines())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Versions) != 1 || s.Versions[0].Number != 1 {
		t.Fatalf("expected v1: %+v", s.Versions)
	}
	v1Hash := s.Versions[0].Hash

	// Correction: Tesco was actually £48.20 vs £45.20 posted.
	corrected := sampleLines()
	corrected[1].AmountMinor = -4520
	corrected[1].BalanceMinor = 295480
	v2, err := s.Revise(cutoff, corrected, []string{"tx-2 amount corrected £45.20 → £48.20"})
	if err != nil {
		t.Fatal(err)
	}
	if v2.Number != 2 || v2.PrevHash != v1Hash {
		t.Fatalf("v2 must chain to v1: %+v", v2)
	}
	if s.Versions[0].Hash != v1Hash {
		t.Fatal("v1 must be untouched after revision")
	}
	if _, err := s.Revise(cutoff, corrected, nil); err == nil {
		t.Fatal("revision must declare corrections")
	}
}

func TestDiffBetweenVersions(t *testing.T) {
	s, _ := Generate("acc-1", "2026-08", cutoff, sampleLines())
	v3 := append(sampleLines(), Line{Date: time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC), Description: "REFUND AMAZON", AmountMinor: 1999, Currency: "GBP", BalanceMinor: 297179, Ref: "tx-3"})
	v3[0].Description = "SALARY AUG"
	if _, err := s.Revise(cutoff, v3, []string{"added late refund; salary description corrected"}); err != nil {
		t.Fatal(err)
	}
	d, err := s.DiffVersions(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Added) != 1 || d.Added[0].Ref != "tx-3" {
		t.Fatalf("expected tx-3 added: %+v", d.Added)
	}
	if len(d.Corrected) != 1 || d.Corrected[0].Before.Description != "SALARY" {
		t.Fatalf("expected salary correction: %+v", d.Corrected)
	}
	if len(d.Removed) != 0 {
		t.Fatalf("nothing removed: %+v", d.Removed)
	}
}

func TestCanonicalHashStable(t *testing.T) {
	a := sampleLines()
	b := sampleLines()
	// Input order must not matter (canonicalisation sorts).
	b[0], b[1] = b[1], b[0]
	if hashCanonical(a) != hashCanonical(b) {
		t.Fatal("canonical hash must be order-independent")
	}
	// Any content change must change the hash.
	c := sampleLines()
	c[1].AmountMinor += 1
	if hashCanonical(a) == hashCanonical(c) {
		t.Fatal("content change must change hash")
	}
}

func TestTamperEvidentVerification(t *testing.T) {
	v := NewVerifier([]byte("platform-secret-key"))
	s, _ := Generate("acc-1", "2026-08", cutoff, sampleLines())
	corrected := sampleLines()
	corrected[0].AmountMinor = 260000
	if _, err := s.Revise(cutoff, corrected, []string{"salary figure corrected"}); err != nil {
		t.Fatal(err)
	}
	for i := range s.Versions {
		if err := v.Sign(&s.Versions[i]); err != nil {
			t.Fatal(err)
		}
	}
	if res, msg := v.Verify(*s); res != VerifyValid {
		t.Fatalf("clean statement must verify: %s %s", res, msg)
	}

	// Tamper with v1 content → ALTERED, and the chain must catch it too.
	tampered := *s
	tampered.Versions[0].Lines[0].AmountMinor += 100
	if res, _ := v.Verify(tampered); res != VerifyAltered {
		t.Fatalf("tampered content must be ALTERED, got %s", res)
	}

	// Tamper with the stored hash (content untouched) → ALTERED.
	t2 := *s
	t2.Versions[1].Hash = strings.Repeat("0", 64)
	if res, _ := v.Verify(t2); res != VerifyAltered {
		t.Fatalf("forged hash must be ALTERED, got %s", res)
	}

	// Wrong key → ALTERED (signature check).
	wrongKey := NewVerifier([]byte("attacker-key"))
	if res, _ := wrongKey.Verify(*s); res != VerifyAltered {
		t.Fatalf("wrong key must not verify, got %s", res)
	}
}

func TestVerifyExportSingleVersion(t *testing.T) {
	v := NewVerifier([]byte("k"))
	lines := sampleLines()
	hash := hashCanonical(lines)
	ver := &Version{Hash: hash}
	if err := v.Sign(ver); err != nil {
		t.Fatal(err)
	}
	if res, _ := v.VerifyExport("acc-1", "2026-08", lines, hash, ver.Signature); res != VerifyValid {
		t.Fatalf("export must verify: %s", res)
	}
	lines[0].AmountMinor += 1
	if res, _ := v.VerifyExport("acc-1", "2026-08", lines, hash, ver.Signature); res != VerifyAltered {
		t.Fatalf("modified export must be ALTERED, got %s", res)
	}
}
