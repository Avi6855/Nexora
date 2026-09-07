package latency

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllocateAndExhaustion(t *testing.T) {
	m := New(Budget{Total: 100 * time.Millisecond})
	d, err := m.Allocate("risk", 250*time.Millisecond)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if d != 100*time.Millisecond {
		t.Fatalf("allocation must clamp to remaining, got %v", d)
	}
	if _, err := m.Allocate("ledger", 1*time.Millisecond); err == nil {
		t.Fatal("budget must be exhausted after full allocation")
	}
	// Release reopens the budget.
	m.Release("risk", 50*time.Millisecond)
	if _, err := m.Allocate("ledger", 10*time.Millisecond); err != nil {
		t.Fatalf("after release allocation should succeed: %v", err)
	}
}

func TestDeadlineNeverExtends(t *testing.T) {
	m := New(Budget{Total: 100 * time.Millisecond})
	orig := m.Deadline()
	// Pulling the deadline earlier is allowed (downstream returned fast).
	if err := m.ExtendDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("earlier deadline must be allowed: %v", err)
	}
	// Pushing beyond the original budget must be refused.
	future := orig.Add(10 * time.Second)
	if err := m.ExtendDeadline(future); err == nil {
		t.Fatal("deadline must never extend beyond the original budget")
	}
}

func TestHeaderPropagationRoundTrip(t *testing.T) {
	m := New(BudgetPayment)
	outbound, _ := http.NewRequest("POST", "http://downstream/v1/payments", nil)
	m.Inject(outbound)

	inbound := httptest.NewRequest("POST", "/v1/payments", nil)
	inbound.Header.Set(HeaderDeadline, outbound.Header.Get(HeaderDeadline))
	inbound.Header.Set(HeaderBudget, outbound.Header.Get(HeaderBudget))

	downstream := FromRequest(inbound, BudgetInternal)
	remaining := time.Until(downstream.Deadline())
	if remaining <= 0 || remaining > BudgetPayment.Total {
		t.Fatalf("downstream must continue the same deadline, got %v", remaining)
	}
	// Downstream allocation is bounded by the remaining shared budget.
	d, err := downstream.Allocate("ledger", 2*time.Second)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	if d > BudgetPayment.Total {
		t.Fatalf("downstream allocation %v exceeds total budget", d)
	}
}

func TestMiddlewareAndContext(t *testing.T) {
	srv := httptest.NewServer(Middleware(map[string]Budget{
		"/v1/balance": BudgetBalance,
	}, BudgetInternal)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, ok := FromContext(r.Context())
		if !ok {
			t.Error("manager missing from context")
			return
		}
		if m.Summary().Total != BudgetBalance.Total {
			t.Errorf("expected balance budget, got %v", m.Summary().Total)
		}
		w.WriteHeader(200)
	})))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/balance")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
}

func TestSplitEvenShares(t *testing.T) {
	m := New(Budget{Total: 300 * time.Millisecond})
	shares, err := m.Split("risk", "ledger", "notify")
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	for name, d := range shares {
		if d != 100*time.Millisecond {
			t.Fatalf("%s share = %v, want 100ms", name, d)
		}
	}
	if _, err := m.Allocate("extra", 1*time.Millisecond); err == nil {
		t.Fatal("after full split budget must be exhausted")
	}
}

func TestSummaryAccounting(t *testing.T) {
	m := New(Budget{Total: 100 * time.Millisecond})
	m.Allocate("risk", 30*time.Millisecond)
	m.Release("risk", 10*time.Millisecond) // used only 20
	rep := m.Summary()
	if rep.Spent != 20*time.Millisecond {
		t.Fatalf("spent = %v, want 20ms", rep.Spent)
	}
	if rep.Overhead["risk"] != 20*time.Millisecond {
		t.Fatalf("risk overhead = %v, want 20ms", rep.Overhead["risk"])
	}
}
