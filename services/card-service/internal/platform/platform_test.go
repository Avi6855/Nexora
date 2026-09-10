package platform

import (
	"errors"
	"testing"
	"time"

	"github.com/nexora/nexora/shared/cards"
)

func TestMerchantLockAllowAndDecline(t *testing.T) {
	p := NewPlatform()
	vc, err := p.CreateLockedCard("phys-1", "NETFLIX")
	if err != nil {
		t.Fatalf("CreateLockedCard: %v", err)
	}
	// Same group billed via a different descriptor must still pass.
	if err := p.AuthorizeMerchant(vc.ID, cards.MerchantIdentity{
		ID: "m-net", Name: "Netflix International B.V.", Group: "NETFLIX",
	}, 999); err != nil {
		t.Fatalf("locked merchant should allow: %v", err)
	}
	// Stolen number at another merchant must decline.
	err = p.AuthorizeMerchant(vc.ID, cards.MerchantIdentity{
		ID: "m-spot", Name: "Spotify", Group: "SPOTIFY",
	}, 999)
	if !errors.Is(err, cards.ErrMerchantLocked) {
		t.Fatalf("expected ErrMerchantLocked, got %v", err)
	}
	// Unknown card must 404.
	if err := p.AuthorizeMerchant("missing", cards.MerchantIdentity{Name: "x"}, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRuleEvaluation(t *testing.T) {
	p := NewPlatform()
	vc, err := p.CreateLockedCard("phys-1", "")
	if err != nil {
		t.Fatalf("CreateLockedCard: %v", err)
	}
	rules := []cards.AuthRule{{Priority: 1, Action: cards.ActionBlock, BlockGambling: true}}
	if err := p.SetRules(vc.ID, rules); err != nil {
		t.Fatalf("SetRules: %v", err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	dec, _, err := p.EvaluateSpend(vc.ID, cards.AuthAttempt{
		At: now, Country: "GB", Category: "GAMBLING", Online: true, AmountMinor: 100,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec != cards.ActionBlock {
		t.Fatalf("gambling should block, got %s", dec)
	}
	dec, _, err = p.EvaluateSpend(vc.ID, cards.AuthAttempt{
		At: now, Country: "GB", Category: "TRANSPORT", Online: true, AmountMinor: 100,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if dec != cards.ActionAllow {
		t.Fatalf("transport should allow, got %s", dec)
	}
	if err := p.SetRules("missing", rules); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestTokenContinuityReplace(t *testing.T) {
	p := NewPlatform()
	now := time.Now().UTC()
	if _, err := p.RegisterToken("tok-1", "old-card", "Netflix", now); err != nil {
		t.Fatalf("RegisterToken: %v", err)
	}
	if n := p.ReplaceCardCredentials("old-card", "new-card"); n != 1 {
		t.Fatalf("expected 1 remapped, got %d", n)
	}
	funding, err := p.ChargeThroughToken("tok-1")
	if err != nil {
		t.Fatalf("ChargeThroughToken: %v", err)
	}
	if funding != "new-card" {
		t.Fatalf("expected funding new-card, got %s", funding)
	}
	if err := p.SuspendToken("tok-1"); err != nil {
		t.Fatalf("SuspendToken: %v", err)
	}
	if _, err := p.ChargeThroughToken("tok-1"); err == nil {
		t.Fatal("suspended token should decline")
	}
	if err := p.SuspendToken("missing"); !errors.Is(err, cards.ErrTokenUnknown) {
		t.Fatalf("expected ErrTokenUnknown, got %v", err)
	}
}

func TestSagaAdvanceAndPartnerFailure(t *testing.T) {
	p := NewPlatform()
	now := time.Now().UTC()
	s, err := p.StartLifecycle("card-1", now)
	if err != nil {
		t.Fatalf("StartLifecycle: %v", err)
	}
	if s.Stage != cards.StageRequested {
		t.Fatalf("expected REQUESTED, got %s", s.Stage)
	}
	s, err = p.AdvanceLifecycle("card-1", now)
	if err != nil {
		t.Fatalf("AdvanceLifecycle: %v", err)
	}
	if s.Stage != cards.StagePersonalised {
		t.Fatalf("expected PERSONALISED, got %s", s.Stage)
	}
	if _, err := p.StartLifecycle("card-1", now); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict on duplicate start, got %v", err)
	}
	s, err = p.ReportPartnerFailure("card-1", now, "bureau down")
	if err != nil {
		t.Fatalf("ReportPartnerFailure: %v", err)
	}
	if s.FailReason != "bureau down" {
		t.Fatalf("expected fail reason recorded, got %q", s.FailReason)
	}
	if _, err := p.AdvanceLifecycle("missing", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeliveryClassification(t *testing.T) {
	p := NewPlatform()
	wf, err := p.ClassifyDelivery("lost")
	if err != nil {
		t.Fatalf("ClassifyDelivery: %v", err)
	}
	if !wf.NewCardNeeded || !wf.ReDispatch {
		t.Fatalf("LOST must reissue + re-dispatch, got %+v", wf)
	}
	wf, err = p.ClassifyDelivery("wrong address")
	if err != nil {
		t.Fatalf("ClassifyDelivery: %v", err)
	}
	if !wf.AddressCheck || wf.NewCardNeeded {
		t.Fatalf("WRONG_ADDRESS must verify + reuse same card, got %+v", wf)
	}
	if _, err := p.ClassifyDelivery("teleported by pigeons"); !errors.Is(err, cards.ErrDeliveryUnknown) {
		t.Fatalf("expected ErrDeliveryUnknown, got %v", err)
	}
}
