package payees

import (
	"strings"
	"testing"
	"time"
)

func TestValidationGateway(t *testing.T) {
	ok := Validate(ValidationRequest{SortCode: "04-00-04", AccountNumber: "12345678", PayeeName: "Sarah", Country: "GB"}, nil)
	if ok.Outcome != ValidationValid {
		t.Fatalf("valid details must pass: %+v", ok)
	}
	bad := Validate(ValidationRequest{SortCode: "04000", AccountNumber: "12345", Country: "GB"}, nil)
	if bad.Outcome != ValidationInvalid || len(bad.Reasons) != 2 {
		t.Fatalf("structurally invalid details must fail with both reasons: %+v", bad)
	}
	// Modulus hook is honoured.
	fail := Validate(ValidationRequest{SortCode: "040004", AccountNumber: "99999999", Country: "GB"},
		func(sc, an string) bool { return false })
	if fail.Outcome != ValidationInvalid || fail.ChecksumOK {
		t.Fatalf("modulus failure must invalidate: %+v", fail)
	}
	if got := Validate(ValidationRequest{SortCode: "040004", AccountNumber: "12345678", Country: "FR"}, nil); got.Outcome != ValidationUnsupported {
		t.Fatalf("unsupported country: %+v", got)
	}
}

func TestNameResolution(t *testing.T) {
	cases := []struct {
		entered, legal string
		want           MatchLevel
	}{
		{"Sarah Smith", "sarah smith", MatchExact},
		{"Amazon", "Amazon Europe Core Services UK Ltd", MatchClose},
		{"amazon", "AMAZON EU S.A.R.L", MatchClose},
		{"Bob's Widgets", "Completely Different Trading Ltd", MatchNoMatch},
		{"", "someone", MatchUnknown},
	}
	for _, c := range cases {
		lvl, conf := ResolveName(c.entered, c.legal)
		if lvl != c.want {
			t.Fatalf("ResolveName(%q,%q) = %s (%d), want %s", c.entered, c.legal, lvl, conf, c.want)
		}
	}
	if _, conf := ResolveName("Sarah Smith", "sarah smith"); conf != confExact {
		t.Fatalf("exact confidence %d, want %d", conf, confExact)
	}
	if _, conf := ResolveName("Amazon", "Amazon Europe Core Services UK Ltd"); conf != confClose {
		t.Fatalf("close confidence %d, want %d", conf, confClose)
	}
}

func TestDirectoryAddSearchStale(t *testing.T) {
	d := NewDirectory()
	now := time.Now()
	r1, err := d.Add(Recipient{
		OwnerCustomerID: "c1", DisplayName: "Sarah (Landlord)",
		SortCode: "040004", AccountNumber: "12345678", Verified: true,
		LastUsedAt: now.AddDate(0, 0, -3), TimesUsed: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Add(Recipient{OwnerCustomerID: "c1", DisplayName: "Duplicate", SortCode: "040004", AccountNumber: "12345678"}); err == nil {
		t.Fatal("identical account must be rejected")
	}
	if r1.AccountFingerprint != Fingerprint("04-00-04", "12345678") {
		t.Fatal("fingerprint must be normalisation-independent")
	}
	if _, err = d.Add(Recipient{OwnerCustomerID: "c1", DisplayName: ""}); err == nil {
		t.Fatal("display name required")
	}

	r2, _ := d.Add(Recipient{OwnerCustomerID: "c1", DisplayName: "John the Accountant", SortCode: "040005", AccountNumber: "87654321"})
	d.recipients[r2.ID].LastUsedAt = now.AddDate(0, 0, -100) // old usage

	// Search by token substring.
	res := d.Search("c1", "landlord")
	if len(res) == 0 || res[0].ID != r1.ID {
		t.Fatalf("landlord search must rank r1 first: %+v", res)
	}
	// Recency beats frequency when both weakly match.
	res = d.Search("c1", "")
	if len(res) != 2 {
		t.Fatalf("bare listing should return both, got %d", len(res))
	}
	// Stale detection.
	if StaleAfter(r1, 30*24*time.Hour, now) {
		t.Fatal("used 3 days ago is not stale")
	}
	if !StaleAfter(d.recipients[r2.ID], 30*24*time.Hour, now) {
		t.Fatal("unused 100 days must be stale")
	}
	// Isolation between owners.
	if got := d.Search("c2", "landlord"); len(got) != 0 {
		t.Fatalf("owner isolation broken: %+v", got)
	}
}

func TestChangeHistoryAudit(t *testing.T) {
	h := &ChangeHistory{}
	before := map[string]string{"sort_code": "040004", "account": "12345678"}
	after := map[string]string{"sort_code": "040005", "account": "87654321"}
	rec := ChangeRecord{RecipientID: "rcp-x", ChangedBy: "customer-c1", ChangedAt: time.Now(),
		Before: before, After: after, Reason: "customer edit"}
	if err := h.Record(rec); err != nil {
		t.Fatal(err)
	}
	// No-op changes rejected.
	if err := h.Record(ChangeRecord{RecipientID: "rcp-x"}); err == nil {
		t.Fatal("empty change record must be rejected")
	}
	got := h.For("rcp-x")
	if len(got) != 1 {
		t.Fatalf("history length %d, want 1", len(got))
	}
	s := Summarise(got[0])
	for _, want := range []string{"customer-c1", "040004", "040005", "****5678", "customer edit"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q: %s", want, s)
		}
	}
	// Raw account number must never appear in the support summary.
	if strings.Contains(s, "87654321") {
		t.Fatal("summary leaks full account number")
	}
}

func TestTemplateDrift(t *testing.T) {
	d := NewDirectory()
	r, _ := d.Add(Recipient{OwnerCustomerID: "c1", DisplayName: "ABC Homes", SortCode: "040004", AccountNumber: "12345678"})
	tpl := Template{OwnerCustomerID: "c1", Name: "Rent", RecipientID: r.ID, Reference: "RENT-SEPT", ExpectedMinor: 125000, Currency: "GBP"}

	if changes := CheckTemplate(tpl, r); len(changes) != 0 {
		t.Fatalf("unchanged template must have no drift: %+v", changes)
	}
	// Recipient deleted → critical.
	if changes := CheckTemplate(tpl, nil); len(changes) != 1 || !changes[0].Critical {
		t.Fatalf("missing recipient must be critical drift: %+v", changes)
	}
	// Wrong owner binding → critical.
	other, _ := d.Add(Recipient{OwnerCustomerID: "c2", DisplayName: "Other", SortCode: "040005", AccountNumber: "55555555"})
	if changes := CheckTemplate(tpl, other); len(changes) == 0 {
		t.Fatal("owner mismatch must surface as drift")
	}
}
