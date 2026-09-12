package schemes

import (
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	sharedschemes "github.com/nexora/nexora/shared/schemes"
)

func testService() *Service { return NewService(zerolog.Nop()) }

func TestServiceCertRun(t *testing.T) {
	svc := testService()
	rep, err := svc.RunCert([]CertCaseInput{
		{Kind: sharedschemes.ScenarioAuth, Message: map[string]string{"amount": "100", "currency": "GBP", "pan": "p"}, Expected: map[string]string{"response_code": "00"}},
		{Kind: sharedschemes.ScenarioRefund, Message: map[string]string{"original_tx": "T1", "amount": "10"}, Expected: map[string]string{"refund_status": "REFUNDED"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed != 2 || !rep.AllPassed {
		t.Fatalf("cert must pass: %+v", rep)
	}
	got, err := svc.GetCertRun(rep.RunID)
	if err != nil || got.RunID != rep.RunID {
		t.Fatalf("GetCertRun: %v", err)
	}
	if _, err := svc.GetCertRun("missing"); !errors.Is(err, sharedschemes.ErrCertRunNotFound) {
		t.Fatalf("missing run: %v", err)
	}
}

func TestServiceRoute(t *testing.T) {
	svc := testService()
	opts, err := svc.Route(10000, "GBP", "UK")
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) == 0 || opts[0].Reason == "" {
		t.Fatalf("route needs ranked options with reasons: %+v", opts)
	}
	if _, err := svc.Route(100, "GBP", "XX"); !errors.Is(err, sharedschemes.ErrNoRoute) {
		t.Fatalf("unknown dest: %v", err)
	}
}

func TestServiceSettlement(t *testing.T) {
	svc := testService()
	ts := time.Date(2026, 9, 15, 14, 0, 0, 0, time.UTC) // Tue before cutoff
	next, err := svc.NextSettlement("VISA", ts)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 16, 15, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("got %s want %s", next, want)
	}
	if _, err := svc.NextSettlement("NOPE", ts); !errors.Is(err, sharedschemes.ErrSchemeNotFound) {
		t.Fatalf("unknown scheme: %v", err)
	}
}

func TestServiceFees(t *testing.T) {
	svc := testService()
	tot, err := svc.AttributeFee(sharedschemes.FeeBreakdown{TxID: "tx-1", Scheme: "VISA", Currency: "GBP", Network: 10, Processor: 5, Interchange: 20, Internal: 2})
	if err != nil {
		t.Fatal(err)
	}
	if tot.TotalMinor != 37 {
		t.Fatalf("total 37, got %d", tot.TotalMinor)
	}
	sum := svc.FeeSummary("VISA")
	if sum.Count != 1 || sum.TotalMinor != 37 {
		t.Fatalf("summary: %+v", sum)
	}
	all := svc.FeeSummaryAll()
	if len(all) != 1 {
		t.Fatalf("summary all: %+v", all)
	}
}
