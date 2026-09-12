package household

import (
	"testing"
)

func TestBillsCatalogCompareAction(t *testing.T) {
	s := NewStore()
	b, err := s.AddBill("Octopus Energy", 12000, "MONTHLY")
	if err != nil {
		t.Fatalf("add bill failed: %v", err)
	}
	market := []MarketPlan{
		{Provider: "Cheap Energy", Amount: 9000, Cadence: "MONTHLY"},
		{Provider: "Pricey Energy", Amount: 15000, Cadence: "MONTHLY"},
		{Provider: "Annual Deal", Amount: 8000, Cadence: "ANNUAL"},
	}
	cheaper, err := s.CompareBills(b.ID, market)
	if err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	if len(cheaper) != 1 || cheaper[0].Provider != "Cheap Energy" {
		t.Fatalf("cheaper-comparable wrong: %+v", cheaper)
	}
	acted, err := s.BillAction(b.ID, BillActionSwitch)
	if err != nil {
		t.Fatalf("action failed: %v", err)
	}
	if acted.Action != BillActionSwitch || acted.TaskState != TaskPending {
		t.Fatalf("action state wrong: %+v", acted)
	}
	done, err := s.CompleteBillTask(b.ID)
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if done.TaskState != TaskDone {
		t.Fatalf("task = %s, want DONE", done.TaskState)
	}
	for _, a := range []string{BillActionCancel, BillActionRenew, BillActionIgnore} {
		if _, err := s.BillAction(b.ID, a); err != nil {
			t.Fatalf("action %s failed: %v", a, err)
		}
	}
	if _, err := s.BillAction(b.ID, "explode"); err == nil {
		t.Fatalf("unknown action must fail")
	}
}

func TestMandatesMigrateVerify(t *testing.T) {
	s := NewStore()
	m, err := s.AddMandate("Netflix", "DD-123", 1599)
	if err != nil {
		t.Fatalf("add mandate failed: %v", err)
	}
	if m.MappedMerchant != "Netflix" {
		t.Fatalf("default mapping wrong: %+v", m)
	}
	mapped, err := s.MapMerchant(m.ID, "NETFLIX UK")
	if err != nil {
		t.Fatalf("map failed: %v", err)
	}
	if mapped.MappedMerchant != "NETFLIX UK" {
		t.Fatalf("mapped = %s", mapped.MappedMerchant)
	}
	mig, err := s.MigrateMandate(m.ID, "acct-new")
	if err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	if mig.MigrationState != TaskPending || mig.FirstCollectionVerified {
		t.Fatalf("migration must start PENDING unverified: %+v", mig)
	}
	ver, err := s.VerifyFirstCollection(m.ID)
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !ver.FirstCollectionVerified || ver.MigrationState != TaskDone {
		t.Fatalf("verify-first-collection must complete: %+v", ver)
	}
	list := s.ListMandates()
	if len(list) != 1 {
		t.Fatalf("mandate list len = %d, want 1", len(list))
	}
}

func TestClosureScan(t *testing.T) {
	safe := ScanClosure(ClosureInput{AccountID: "a-1"})
	if safe.Verdict != "SAFE" || len(safe.Blockers) != 0 {
		t.Fatalf("empty account must be SAFE: %+v", safe)
	}
	unsafe := ScanClosure(ClosureInput{
		AccountID: "a-2", DirectDebits: 2, Recurring: 1,
		PendingRefunds: 1, PendingTransfers: 1, Subscriptions: 1, BalanceMinor: 500,
	})
	if unsafe.Verdict != "UNSAFE" {
		t.Fatalf("loaded account must be UNSAFE: %+v", unsafe)
	}
	if len(unsafe.Blockers) != 6 {
		t.Fatalf("blockers = %v, want 6", unsafe.Blockers)
	}
	// Single blocker still blocks.
	one := ScanClosure(ClosureInput{AccountID: "a-3", BalanceMinor: 1})
	if one.Verdict != "UNSAFE" || len(one.Blockers) != 1 {
		t.Fatalf("non-zero balance must block: %+v", one)
	}
}
