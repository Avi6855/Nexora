package standin

import (
	"testing"
)

func TestCertifyCompatible(t *testing.T) {
	c := NewCertifier()
	res := c.Certify("payment-service@v2", []RequestResponse{
		{
			Endpoint: "POST /v1/payments",
			Request:  `{"amount":1000}`,
			Primary:  Response{StatusClass: "2xx", Decision: "APPROVED", StateTransition: "PENDING→PROCESSING"},
			StandIn:  Response{StatusClass: "2xx", Decision: "APPROVED", StateTransition: "PENDING→PROCESSING"},
		},
	})
	if res.Overall != VerdictCompatible || res.Score != 100 {
		t.Fatalf("want COMPATIBLE/100, got %s/%d", res.Overall, res.Score)
	}
}

func TestDecisionDivergenceIsBreaking(t *testing.T) {
	c := NewCertifier()
	res := c.Certify("card-service@v9", []RequestResponse{
		{
			Endpoint: "POST /v1/cards/authorize",
			Primary:  Response{StatusClass: "2xx", Decision: "APPROVED", StateTransition: "PENDING→AUTHORIZED"},
			StandIn:  Response{StatusClass: "2xx", Decision: "DECLINED", StateTransition: "PENDING→DECLINED"},
		},
	})
	if res.Overall != VerdictBreaking {
		t.Fatalf("decision divergence must be BREAKING, got %s", res.Overall)
	}
	// Score must reflect the divergence too.
	if res.Score >= 100 {
		t.Fatalf("score must drop, got %d", res.Score)
	}
}

func TestStatusClassFlipIsBreaking(t *testing.T) {
	c := NewCertifier()
	res := c.Certify("x", []RequestResponse{{
		Endpoint: "GET /v1/balance",
		Primary:  Response{StatusClass: "2xx", Decision: "OK"},
		StandIn:  Response{StatusClass: "4xx", Decision: "OK"},
	}})
	if res.Overall != VerdictBreaking {
		t.Fatalf("2xx→4xx must be BREAKING, got %s", res.Overall)
	}
}

func TestErrorOnlyDriftIsPartial(t *testing.T) {
	c := NewCertifier()
	res := c.Certify("x", []RequestResponse{{
		Endpoint: "POST /v1/payments",
		Primary:  Response{StatusClass: "4xx", Decision: "DECLINED", ErrorCode: "INSUFFICIENT_FUNDS"},
		StandIn:  Response{StatusClass: "4xx", Decision: "DECLINED", ErrorCode: "INSUFFICIENT_FUNDS_V2"},
	}})
	if res.Overall != VerdictPartial {
		t.Fatalf("error-code-only drift must be PARTIAL, got %s (%v)", res.Overall, res.Endpoints[0].Differences)
	}
}

func TestEmptyStateTransitionOnBothIsTolerated(t *testing.T) {
	c := NewCertifier()
	res := c.Certify("x", []RequestResponse{{
		Endpoint: "GET /v1/health",
		Primary:  Response{StatusClass: "2xx", Decision: "OK"},
		StandIn:  Response{StatusClass: "2xx", Decision: "OK"},
	}})
	if res.Overall != VerdictCompatible {
		t.Fatalf("read-only endpoint must certify, got %s: %v", res.Overall, res.Endpoints[0].Differences)
	}
}

func TestHistoryAccumulates(t *testing.T) {
	c := NewCertifier()
	c.Certify("a", nil)
	c.Certify("b", nil)
	if len(c.History()) != 2 {
		t.Fatalf("history = %d", len(c.History()))
	}
}

func TestCapabilityCanServeFailsClosed(t *testing.T) {
	p := &PlatformCapabilities{
		Platform: "stand-in", Epoch: 42, Active: true, Version: "2026.09",
		Capabilities: []Capability{
			{Name: "cards", Available: true},
			{Name: "cash", Available: true, Degraded: true, Note: "deposits only, no withdrawals"},
			{Name: "investments", Available: false},
		},
	}
	if !p.CanServe("cards") {
		t.Fatal("cards must be available")
	}
	if p.CanServe("investments") {
		t.Fatal("investments must be unavailable")
	}
	if p.CanServe("never_declared") {
		t.Fatal("undeclared capabilities must fail closed")
	}
}

func TestRegistryPublishAndRead(t *testing.T) {
	r := NewRegistry()
	r.Publish(&PlatformCapabilities{Platform: "stand-in", Version: "2026.09", Epoch: 42, Active: true,
		Capabilities: []Capability{{Name: "transfers", Available: true}}})
	got := r.Active("stand-in", "2026.09")
	if got == nil || !got.CanServe("transfers") {
		t.Fatal("published capabilities must be readable")
	}
	if r.Active("stand-in", "9999") != nil {
		t.Fatal("unknown version must return nil")
	}
}
