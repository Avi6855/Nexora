package eventgov

import (
	"errors"
	"testing"
)

func testFields() []Field {
	return []Field{
		{Name: "id", Type: "string", Required: true},
		{Name: "amount", Type: "int64", Required: true},
		{Name: "note", Type: "string", Required: false},
	}
}

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	s, err := r.RegisterSchema("payments", 1, testFields())
	if err != nil {
		t.Fatalf("RegisterSchema: %v", err)
	}
	if s.Topic != "payments" || s.Version != 1 || len(s.Fields) != 3 {
		t.Fatalf("bad schema: %+v", s)
	}
	got, err := r.GetSchema("payments", 1)
	if err != nil || len(got.Fields) != 3 {
		t.Fatalf("GetSchema: %+v %v", got, err)
	}
	if _, err := r.RegisterSchema("payments", 1, testFields()); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate version, got %v", err)
	}
	if _, err := r.GetSchema("payments", 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("", 1, testFields()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for empty topic, got %v", err)
	}
	if _, err := r.RegisterSchema("t", 0, testFields()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for version 0, got %v", err)
	}
	if _, err := r.RegisterSchema("t", 1, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for no fields, got %v", err)
	}
	dup := []Field{{Name: "a", Type: "string"}, {Name: "a", Type: "string"}}
	if _, err := r.RegisterSchema("t", 1, dup); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for duplicate fields, got %v", err)
	}
}

func TestBackwardCompatibility(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("orders", 1, testFields()); err != nil {
		t.Fatal(err)
	}
	// v2 adds an OPTIONAL field: backward compatible, forward compatible.
	v2 := append(testFields(), Field{Name: "promo", Type: "string", Required: false})
	if _, err := r.RegisterSchema("orders", 2, v2); err != nil {
		t.Fatal(err)
	}
	res, err := r.CheckCompatibility("orders", 1, 2, ModeBackward)
	if err != nil || !res.Compatible {
		t.Fatalf("backward should pass: %+v %v", res, err)
	}
	// v3 adds a REQUIRED field: backward breaks (old data lacks it).
	v3 := append(testFields(), Field{Name: "coupon", Type: "string", Required: true})
	if _, err := r.RegisterSchema("orders", 3, v3); err != nil {
		t.Fatal(err)
	}
	res, err = r.CheckCompatibility("orders", 1, 3, ModeBackward)
	if err != nil || res.Compatible {
		t.Fatalf("backward should fail on new required field: %+v %v", res, err)
	}
	// v3 forward: old readers ignore the extra field, so forward passes.
	res, err = r.CheckCompatibility("orders", 1, 3, ModeForward)
	if err != nil || !res.Compatible {
		t.Fatalf("forward should pass on additions: %+v %v", res, err)
	}
}

func TestForwardCompatibilityRemoval(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("ledger", 1, testFields()); err != nil {
		t.Fatal(err)
	}
	// v2 removes required field "amount": forward breaks, backward passes.
	v2 := []Field{
		{Name: "id", Type: "string", Required: true},
		{Name: "note", Type: "string", Required: false},
	}
	if _, err := r.RegisterSchema("ledger", 2, v2); err != nil {
		t.Fatal(err)
	}
	fwd, _ := r.CheckCompatibility("ledger", 1, 2, ModeForward)
	if fwd.Compatible {
		t.Fatal("forward should fail when a required field is removed")
	}
	bwd, _ := r.CheckCompatibility("ledger", 1, 2, ModeBackward)
	if !bwd.Compatible {
		t.Fatalf("backward should tolerate removals: %+v", bwd)
	}
	full, _ := r.CheckCompatibility("ledger", 1, 2, ModeFull)
	if full.Compatible {
		t.Fatal("full should fail when forward fails")
	}
}

func TestTypeChangeBreaksBoth(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("t", 1, []Field{{Name: "amount", Type: "int64", Required: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterSchema("t", 2, []Field{{Name: "amount", Type: "string", Required: true}}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []CompatibilityMode{ModeBackward, ModeForward, ModeFull} {
		res, err := r.CheckCompatibility("t", 1, 2, m)
		if err != nil || res.Compatible {
			t.Fatalf("mode %s should fail on type change: %+v %v", m, res, err)
		}
	}
}

func TestCheckBadModeAndUnknown(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("t", 1, testFields()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterSchema("t", 2, testFields()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CheckCompatibility("t", 1, 2, "SIDEWAYS"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected ErrInvalid for bad mode, got %v", err)
	}
	if _, err := r.CheckCompatibility("missing", 1, 2, ModeBackward); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeprecateField(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("t", 1, testFields()); err != nil {
		t.Fatal(err)
	}
	if err := r.DeprecateField("t", 1, "note"); err != nil {
		t.Fatalf("DeprecateField: %v", err)
	}
	got, _ := r.GetSchema("t", 1)
	found := false
	for _, f := range got.Fields {
		if f.Name == "note" && f.Deprecated {
			found = true
		}
	}
	if !found {
		t.Fatalf("note should be deprecated: %+v", got.Fields)
	}
	if err := r.DeprecateField("t", 1, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMigrationPlanSafeAndUnsafe(t *testing.T) {
	r := NewRegistry()
	if _, err := r.RegisterSchema("t", 1, testFields()); err != nil {
		t.Fatal(err)
	}
	v2 := append(testFields(), Field{Name: "promo", Type: "string", Required: false})
	if _, err := r.RegisterSchema("t", 2, v2); err != nil {
		t.Fatal(err)
	}
	plan, err := r.MigrationPlan("t", 1, 2)
	if err != nil || !plan.Safe || plan.Verdict != "SAFE" {
		t.Fatalf("additive-only should be SAFE: %+v %v", plan, err)
	}
	if len(plan.Added) != 1 || plan.Added[0].Name != "promo" {
		t.Fatalf("bad added: %+v", plan)
	}
	// v3 removes a field and retypes another: UNSAFE.
	v3 := []Field{
		{Name: "id", Type: "string", Required: true},
		{Name: "amount", Type: "string", Required: true},
	}
	if _, err := r.RegisterSchema("t", 3, v3); err != nil {
		t.Fatal(err)
	}
	plan, err = r.MigrationPlan("t", 1, 3)
	if err != nil || plan.Safe || plan.Verdict != "UNSAFE" {
		t.Fatalf("breaking change should be UNSAFE: %+v %v", plan, err)
	}
	if len(plan.Removed) == 0 || len(plan.TypeChanged) == 0 {
		t.Fatalf("expected removed + type_changed: %+v", plan)
	}
}
