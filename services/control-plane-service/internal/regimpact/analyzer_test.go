package regimpact

import (
	"testing"
	"time"
)

func seed(t *testing.T) *Analyzer {
	t.Helper()
	a := NewAnalyzer()
	a.Register(&ServiceRegistration{
		Service: "payment-service",
		Capabilities: []Capability{
			{Name: "process_payment", DataClasses: []string{"payments", "transaction_history"}, CustomerFacing: true,
				APIs: []string{"POST /v1/payments"}, DataModels: []string{"payments"}},
		},
		DependsOn: []string{"fraud-service", "ledger-service", "policy-service"},
	})
	a.Register(&ServiceRegistration{
		Service: "card-service",
		Capabilities: []Capability{
			{Name: "authorize_card", DataClasses: []string{"card_credentials", "transaction_history"}, CustomerFacing: true,
				APIs: []string{"POST /v1/cards/authorize"}, DataModels: []string{"cards", "authorizations"}},
		},
		DependsOn: []string{"ledger-service"},
	})
	a.Register(&ServiceRegistration{
		Service: "ledger-service",
		Capabilities: []Capability{
			{Name: "post_entries", DataClasses: []string{"balances"}, CustomerFacing: false,
				APIs: []string{"POST /v1/ledger/entries"}, DataModels: []string{"ledger_entries"}},
		},
	})
	a.Register(&ServiceRegistration{
		Service: "notification-service",
		Capabilities: []Capability{
			{Name: "send_alert", DataClasses: []string{"contact_details"}, CustomerFacing: true,
				APIs: []string{"POST /v1/notify"}, DataModels: []string{"notifications"}},
		},
		DependsOn: []string{"payment-service", "ledger-service"},
	})
	a.Register(&ServiceRegistration{
		Service: "search-service",
		Capabilities: []Capability{
			{Name: "index_transactions", DataClasses: []string{"transaction_history"}, CustomerFacing: true,
				APIs: []string{"GET /v1/search"}, DataModels: []string{"search_index"}},
		},
		DependsOn: []string{"ledger-service"},
	})
	return a
}

func TestPaymentsRuleHitsDirectAndIndirect(t *testing.T) {
	a := seed(t)
	rule := Rule{
		RuleID:        "FCA-PSR-2026-41",
		Title:         "Confirmation of payee widening",
		Domain:        DomainPayments,
		EffectiveFrom: time.Now().UTC().Add(90 * 24 * time.Hour),
	}
	res := a.Analyze(rule)

	// Direct: payment-service (processes payments).
	// Indirect: notification-service depends on payment-service.
	var pay, notif *Impact
	for i := range res.AffectedServices {
		switch res.AffectedServices[i].Service {
		case "payment-service":
			pay = &res.AffectedServices[i]
		case "notification-service":
			notif = &res.AffectedServices[i]
		}
	}
	if pay == nil {
		t.Fatalf("payment-service must be directly affected: %+v", res.AffectedServices)
	}
	if pay.Indirect {
		t.Fatal("payment-service must be a DIRECT hit")
	}
	if notif == nil || !notif.Indirect {
		t.Fatalf("notification-service must be an INDIRECT hit via propagation, got %+v", notif)
	}
	if notif.HopsFromSource != 1 {
		t.Fatalf("notification-service is one hop away, got %d", notif.HopsFromSource)
	}
	if len(res.UniqueAPIs) == 0 {
		t.Fatal("analysis must enumerate affected APIs")
	}
	if res.EstimatedCustomers == 0 {
		t.Fatal("customer estimate must be computed")
	}
}

func TestDataProtectionRuleIsBroad(t *testing.T) {
	a := seed(t)
	rule := Rule{RuleID: "ICO-GDPR-7", Title: "Data retention tightening", Domain: DomainDataProtection,
		EffectiveFrom: time.Now().UTC().Add(180 * 24 * time.Hour)}
	res := a.Analyze(rule)
	// Every registered service processes personal data classes.
	if len(res.AffectedServices) < 4 {
		t.Fatalf("data-protection rule must sweep broadly, hit %d services", len(res.AffectedServices))
	}
	if res.RiskLevel != "CRITICAL" && res.RiskLevel != "HIGH" {
		t.Fatalf("broad sweep must rate high risk, got %s", res.RiskLevel)
	}
}

func TestScopedRuleNarrowsNothingHere(t *testing.T) {
	a := seed(t)
	rule := Rule{RuleID: "R-ISA-9", Title: "ISA reporting", Domain: DomainAccounts,
		ProductScopes: []string{"savings"},
		EffectiveFrom: time.Now().UTC().Add(30 * 24 * time.Hour)}
	res := a.Analyze(rule)
	// No service declares savings/balances-only accounts... ledger has balances.
	if len(res.AffectedServices) == 0 {
		t.Fatal("accounts rule must hit ledger (balances)")
	}
}

func TestUnrelatedRuleHitsNothing(t *testing.T) {
	a := seed(t)
	rule := Rule{RuleID: "R-COMPLAINTS-1", Title: "Complaints logging", Domain: DomainComplaints,
		EffectiveFrom: time.Now().UTC().Add(60 * 24 * time.Hour)}
	res := a.Analyze(rule)
	if len(res.AffectedServices) != 0 {
		t.Fatalf("no service handles complaints yet, got %+v", res.AffectedServices)
	}
	if res.RiskLevel != "NONE" {
		t.Fatalf("empty impact must rate NONE, got %s", res.RiskLevel)
	}
}

func TestDaysUntilEffectiveComputed(t *testing.T) {
	a := seed(t)
	rule := Rule{RuleID: "R-X", Title: "t", Domain: DomainCards,
		EffectiveFrom: time.Now().UTC().Add(10 * 24 * time.Hour)}
	res := a.Analyze(rule)
	if res.DaysUntilEffective != 10 {
		t.Fatalf("days = %d, want 10", res.DaysUntilEffective)
	}
}

func TestCustomerFacingDrivesRisk(t *testing.T) {
	a := seed(t)
	// A KYC rule hitting only identity-service (not registered) → NONE.
	// Register one small customer-facing service to check HIGH.
	a.Register(&ServiceRegistration{
		Service: "kyc-service",
		Capabilities: []Capability{
			{Name: "verify_identity", DataClasses: []string{"identity"}, CustomerFacing: true,
				APIs: []string{"POST /v1/kyc"}, DataModels: []string{"kyc"}},
		},
	})
	res := a.Analyze(Rule{RuleID: "R-KYC", Title: "CDD refresh", Domain: DomainKYC,
		EffectiveFrom: time.Now().UTC().Add(120 * 24 * time.Hour)})
	if res.RiskLevel != "HIGH" {
		t.Fatalf("single customer-facing hit must be HIGH, got %s", res.RiskLevel)
	}
}
