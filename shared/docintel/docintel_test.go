package docintel

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyInterest(t *testing.T) {
	s := NewStore()
	doc, err := s.Ingest("STATEMENT", "mortgage-interest-2024.txt",
		"Merchant: Halifax\nMortgage interest statement\nInterest earned £1,234.56 on 15 Mar 2024\nAPR 4.5% savings interest rate")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if doc.Classification.Kind != KindInterest {
		t.Fatalf("kind = %s, want INTEREST", doc.Classification.Kind)
	}
	if doc.Classification.Confidence < 0.5 {
		t.Fatalf("confidence = %v, want >= 0.5", doc.Classification.Confidence)
	}
	if doc.ContentHash == "" || doc.KeyID != KeyID {
		t.Fatalf("encryption marker missing: %+v", doc)
	}
}

func TestAmountAndDateExtraction(t *testing.T) {
	s := NewStore()
	doc, err := s.Ingest("RECEIPT", "tesco.txt",
		"Merchant: Tesco\nReceipt purchase paid £42.50\nDate: 2024-05-06\nThank you, change 0")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if doc.AmountMinor != 4250 {
		t.Fatalf("amount = %d, want 4250", doc.AmountMinor)
	}
	if doc.Currency != "GBP" {
		t.Fatalf("currency = %s, want GBP", doc.Currency)
	}
	if doc.Merchant != "Tesco" {
		t.Fatalf("merchant = %q, want Tesco", doc.Merchant)
	}
	if doc.Date == nil || doc.Date.Format("2006-01-02") != "2024-05-06" {
		t.Fatalf("date = %v, want 2024-05-06", doc.Date)
	}
	if doc.TaxYear != "2024/25" || doc.TaxYearStart != 2024 {
		t.Fatalf("tax year = %q/%d, want 2024/25/2024", doc.TaxYear, doc.TaxYearStart)
	}
	// UK-format date + tax-year boundary (5 Apr belongs to prior year).
	doc2, err := s.Ingest("INVOICE", "inv.txt", "Invoice VAT\nMerchant: Acme\nTotal £100.00\n05/04/2024")
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if doc2.Date == nil || doc2.Date.Format("2006-01-02") != "2024-04-05" {
		t.Fatalf("date = %v", doc2.Date)
	}
	if doc2.TaxYearStart != 2023 {
		t.Fatalf("tax year start = %d, want 2023", doc2.TaxYearStart)
	}
}

func TestSearchAndTotals(t *testing.T) {
	s := NewStore()
	if _, err := s.Ingest("RECEIPT", "tesco.txt", "Merchant: Tesco\nReceipt purchase paid £120.00 on 10 Jun 2024"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest("RECEIPT", "sainsbury.txt", "Merchant: Sainsbury\nReceipt purchase paid £30.00 on 11 Jun 2024"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest("INTEREST", "halifax.txt", "Merchant: Halifax\nMortgage interest document £500.00 on 12 Jun 2024"); err != nil {
		t.Fatal(err)
	}
	// "all Tesco purchases above £100 in 2024"
	res, err := s.Search(Query{Text: "tesco", MinAmountMinor: 10000, Year: 2024})
	if err != nil || len(res) != 1 {
		t.Fatalf("search = %d (%v)", len(res), err)
	}
	// "mortgage interest document"
	res, err = s.Search(Query{Text: "mortgage interest"})
	if err != nil || len(res) != 1 {
		t.Fatalf("search mortgage = %d (%v)", len(res), err)
	}
	// Kind filter + totals: "total interest earned last tax year".
	tot, err := s.TotalByKind(KindInterest)
	if err != nil {
		t.Fatal(err)
	}
	if tot.Count != 1 || tot.TotalMinor != 50000 {
		t.Fatalf("interest total = %+v, want count=1 total=50000", tot)
	}
	all := s.Totals()
	if len(all) != len(AllKinds) {
		t.Fatalf("totals rows = %d, want %d", len(all), len(AllKinds))
	}
	// Classify endpoint + validation.
	got, err := s.Classify(res[0].ID)
	if err != nil || got.Kind != KindInterest {
		t.Fatalf("Classify = %+v %v", got, err)
	}
	if _, err := s.Classify("missing"); err == nil {
		t.Fatal("expected not-found")
	}
	if _, err := s.Ingest("BOGUS", "x.txt", "hello"); err == nil {
		t.Fatal("expected invalid kind")
	}
	if _, err := s.Search(Query{Kind: "BOGUS"}); err == nil {
		t.Fatal("expected invalid search kind")
	}
	if !strings.Contains("2024/25", "/") {
		t.Fatal("unreachable")
	}
	_ = time.Now
}
