package dataline

import (
	"errors"
	"strings"
	"testing"
)

func TestTokeniseDeterministicPerPurpose(t *testing.T) {
	v := NewVault()
	t1, err := v.Tokenise("fraud", "name", "Ada Lovelace")
	if err != nil || t1 == "" {
		t.Fatalf("Tokenise: %q %v", t1, err)
	}
	t2, err := v.Tokenise("fraud", "name", "Ada Lovelace")
	if err != nil || t2 != t1 {
		t.Fatalf("same input should give same token: %q vs %q (%v)", t1, t2, err)
	}
	// Purpose-bound: same raw under another purpose must differ.
	t3, err := v.Tokenise("marketing", "name", "Ada Lovelace")
	if err != nil || t3 == t1 {
		t.Fatalf("purpose-bound salt must separate tokens: %q vs %q", t1, t3)
	}
	// Raw must never equal the token.
	if strings.Contains(t1, "Ada") {
		t.Fatalf("token leaks raw value: %q", t1)
	}
}

func TestTokeniseValidation(t *testing.T) {
	v := NewVault()
	if _, err := v.Tokenise("", "name", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty purpose, got %v", err)
	}
	if _, err := v.Tokenise("p", "email", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for bad field, got %v", err)
	}
	if _, err := v.Tokenise("p", "name", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty value, got %v", err)
	}
	for _, f := range []string{"name", "account", "address"} {
		if _, err := v.Tokenise("p", f, "v-"+f); err != nil {
			t.Fatalf("field %s should tokenise: %v", f, err)
		}
	}
}

func TestRotateInvalidatesOldTokens(t *testing.T) {
	v := NewVault()
	old, _ := v.Tokenise("fraud", "account", "acc-123")
	if !v.Valid(old) {
		t.Fatal("token should be valid before rotation")
	}
	if err := v.RotatePurpose("fraud"); err != nil {
		t.Fatalf("RotatePurpose: %v", err)
	}
	if v.Valid(old) {
		t.Fatal("old token must be invalid after rotation")
	}
	fresh, err := v.Tokenise("fraud", "account", "acc-123")
	if err != nil || fresh == old {
		t.Fatalf("post-rotation token must differ: %q vs %q (%v)", old, fresh, err)
	}
	// Other purposes are untouched.
	other, _ := v.Tokenise("support", "account", "acc-999")
	if err := v.RotatePurpose("fraud"); err != nil {
		t.Fatal(err)
	}
	if !v.Valid(other) {
		t.Fatal("rotation of one purpose must not touch another")
	}
}

func TestCohortCountAggregateOnly(t *testing.T) {
	v := NewVault()
	if _, err := v.Tokenise("research", "name", "Alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Tokenise("research", "name", "Bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Tokenise("research", "account", "acc-1"); err != nil {
		t.Fatal(err)
	}
	n, err := v.CohortCount("research", "name")
	if err != nil || n != 2 {
		t.Fatalf("CohortCount name = %d, want 2 (%v)", n, err)
	}
	all, err := v.CohortCount("research", "")
	if err != nil || all != 3 {
		t.Fatalf("CohortCount all = %d, want 3 (%v)", all, err)
	}
	if _, err := v.CohortCount("", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid, got %v", err)
	}
	// Rotation clears the cohort.
	if err := v.RotatePurpose("research"); err != nil {
		t.Fatal(err)
	}
	n, _ = v.CohortCount("research", "")
	if n != 0 {
		t.Fatalf("cohort after rotation = %d, want 0", n)
	}
}

func TestAuditLog(t *testing.T) {
	v := NewVault()
	if _, err := v.Tokenise("p", "name", "Alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.CohortCount("p", ""); err != nil {
		t.Fatal(err)
	}
	if err := v.RotatePurpose("p"); err != nil {
		t.Fatal(err)
	}
	log := v.AuditLog()
	if len(log) != 3 {
		t.Fatalf("audit len = %d, want 3", len(log))
	}
	if log[0].Action != ActionTokenise || log[1].Action != ActionCohortCount || log[2].Action != ActionRotate {
		t.Fatalf("bad audit order: %+v", log)
	}
}
