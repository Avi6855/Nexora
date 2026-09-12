package incidentops

import (
	"testing"
	"time"

	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/incidentops"
)

var testNow = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func seed(t *testing.T, s *Service) string {
	t.Helper()
	if _, err := s.Ingest("ledger", shared.KindDeploy, "deploy v42", false, testNow.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.Ingest("ledger", shared.KindMetric, "5xx spike", true, testNow.Add(time.Duration(-10+i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	cands, err := s.Correlate(30*time.Minute, testNow)
	if err != nil || len(cands) != 1 {
		t.Fatalf("seed candidate: %+v %v", cands, err)
	}
	return cands[0].ID
}

func TestTriageWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	id := seed(t, s)
	res, err := s.Triage(id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Severity != "SEV2" || len(res.LikelyCauses) == 0 {
		t.Fatalf("triage: %+v", res)
	}
	if _, err := s.Triage("missing"); err != shared.ErrUnknownCandidate {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.Correlate(0, testNow); err == nil {
		t.Fatal("non-positive window must fail")
	}
	if _, err := s.Ingest("", shared.KindLog, "x", false, testNow); err == nil {
		t.Fatal("empty service must fail")
	}
}

func TestImpactWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	imp, err := s.Calculate([]string{"ledger"}, []shared.ServiceLedger{
		{Service: "ledger", Customers: 10, Transactions: 20, AmountMinor: 300},
	})
	if err != nil || imp.Customers != 10 || imp.AmountAtRiskMinor != 300 {
		t.Fatalf("impact: %+v %v", imp, err)
	}
	if _, err := s.Calculate(nil, nil); err == nil {
		t.Fatal("empty services must fail")
	}
}

func TestCompensationWiring(t *testing.T) {
	s := NewService(zerolog.Nop())
	if err := s.AddPolicy("p1", 30, "all", 500); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPolicy("p1", 30, "all", 500); err != shared.ErrDuplicatePolicy {
		t.Fatalf("dup: %v", err)
	}
	ok, amount, _ := s.Evaluate("a", 60, "standard")
	if !ok || amount != 500 {
		t.Fatalf("evaluate: %v %v", ok, amount)
	}
	a1, err := s.Award("k1", "acct-1", 60, "standard", testNow)
	if err != nil || a1.FraudFlag {
		t.Fatalf("award: %+v %v", a1, err)
	}
	if _, err := s.Award("k1", "acct-1", 60, "standard", testNow); err != shared.ErrDuplicateClaim {
		t.Fatalf("dedup: %v", err)
	}
	_, _ = s.Award("k2", "acct-1", 60, "standard", testNow)
	a3, _ := s.Award("k3", "acct-1", 60, "standard", testNow)
	if !a3.FraudFlag {
		t.Fatalf("flag: %+v", a3)
	}
	newAmt := int64(900)
	over, err := s.Override(a1.ID, "lead", &newAmt, "", testNow)
	if err != nil || over.AmountMinor != 900 {
		t.Fatalf("override: %+v %v", over, err)
	}
	if _, err := s.GetAward("missing"); err != shared.ErrUnknownAward {
		t.Fatalf("unknown award: %v", err)
	}
}
