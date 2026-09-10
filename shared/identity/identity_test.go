package identity

import (
	"testing"
	"time"
)

var tnow = time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

func TestRecoveryWeakSignalsCannotVerify(t *testing.T) {
	r := NewRecoveryEngine(DefaultRecoveryPolicy())
	s := r.StartRecovery("acc1", tnow)
	for _, sig := range []SignalKind{SignalKnowledge, SignalBehavioural, SignalTrustedContact} {
		if err := r.Present(s, sig); err != nil {
			t.Fatal(err)
		}
	}
	verified, _, err := r.Evaluate(s)
	if verified || err == nil {
		t.Fatal("weak signals alone must never verify")
	}
	// Even piling on more weak evidence cannot cross the no-doc ceiling.
	if s.Score > DefaultRecoveryPolicy().MaxWithoutDoc {
		t.Fatalf("no-doc ceiling breached: %d", s.Score)
	}
}

func TestRecoveryStrongSignalsVerify(t *testing.T) {
	r := NewRecoveryEngine(DefaultRecoveryPolicy())
	s := r.StartRecovery("acc1", tnow)
	for _, sig := range []SignalKind{SignalKnownDevice, SignalDocMatch, SignalBiometric} {
		if err := r.Present(s, sig); err != nil {
			t.Fatal(err)
		}
	}
	verified, reason, err := r.Evaluate(s)
	if err != nil || !verified {
		t.Fatalf("strong evidence must verify: %v %s", err, reason)
	}
}

func TestRecoverySingleStrongFactorInsufficient(t *testing.T) {
	r := NewRecoveryEngine(DefaultRecoveryPolicy())
	s := r.StartRecovery("acc1", tnow)
	if err := r.Present(s, SignalDocMatch); err != nil {
		t.Fatal(err)
	}
	// One strong factor (50) + knowledge (15) = 65 raw, but capped at the
	// no-biometric ceiling of 55 — weak evidence cannot top up a single
	// strong factor into a verification.
	if err := r.Present(s, SignalKnowledge); err != nil {
		t.Fatal(err)
	}
	if s.Score > DefaultRecoveryPolicy().MaxWithoutBio {
		t.Fatalf("no-biometric ceiling breached: %d", s.Score)
	}
	verified, _, err := r.Evaluate(s)
	if verified || err == nil {
		t.Fatal("a single strong factor plus weak signals must not verify")
	}
}

func TestRecoverySignalDedup(t *testing.T) {
	r := NewRecoveryEngine(DefaultRecoveryPolicy())
	s := r.StartRecovery("acc1", tnow)
	if err := r.Present(s, SignalKnowledge); err != nil {
		t.Fatal(err)
	}
	if err := r.Present(s, SignalKnowledge); err == nil {
		t.Fatal("repeating a signal must not farm score")
	}
}

func TestDeviceTrustPropagation(t *testing.T) {
	g := NewTrustGraph()
	if _, err := g.Register("phone", tnow); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Register("watch", tnow); err != nil {
		t.Fatal(err)
	}
	if err := g.Link("phone", "watch", "MFA_PAIRED"); err != nil {
		t.Fatal(err)
	}
	if err := g.Trust("phone", tnow); err != nil {
		t.Fatal(err)
	}
	// Trust propagates to the paired new device.
	if st, _ := g.State("watch"); st != DeviceTrusted {
		t.Fatalf("paired device should be trusted, got %s", st)
	}
	// Revoking the phone cascades suspicion to the watch.
	if err := g.Revoke("phone", tnow); err != nil {
		t.Fatal(err)
	}
	if st, _ := g.State("watch"); st != DeviceSuspicious {
		t.Fatalf("distrust must cascade, got %s", st)
	}
}

func TestDeviceTrustDoesNotOverrideRevoked(t *testing.T) {
	g := NewTrustGraph()
	if _, err := g.Register("a", tnow); err != nil {
		t.Fatal(err)
	}
	if err := g.Revoke("a", tnow); err != nil {
		t.Fatal(err)
	}
	if err := g.Trust("a", tnow); err == nil {
		t.Fatal("revoked device cannot be trusted without re-enrolment")
	}
}

func TestDeviceExpiry(t *testing.T) {
	g := NewTrustGraph()
	if _, err := g.Register("old", tnow); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Register("fresh", tnow.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	expired := g.ExpireOld(tnow.Add(30*24*time.Hour), 90*24*time.Hour)
	// "old" was registered at tnow, last seen tnow; 30 days later is < 90 days.
	if len(expired) != 0 {
		t.Fatalf("routine 30-day absence must not expire: %v", expired)
	}
	if _, err := g.Register("ghost", tnow); err != nil {
		t.Fatal(err)
	}
	expired = g.ExpireOld(tnow.Add(91*24*time.Hour), 90*24*time.Hour)
	if len(expired) != 2 { // old + ghost (fresh last seen +30d is 61d old)
		t.Fatalf("stale devices must expire: %v", expired)
	}
}

func TestPasskeyChallengeSingleUse(t *testing.T) {
	p := NewPasskeyStore()
	id, _ := p.IssueChallenge("acc1", tnow)
	if err := p.ConsumeChallenge(id, "acc1", tnow); err != nil {
		t.Fatal(err)
	}
	if err := p.ConsumeChallenge(id, "acc1", tnow); err == nil {
		t.Fatal("challenge replay must be rejected")
	}
}

func TestPasskeyChallengeWrongAccount(t *testing.T) {
	p := NewPasskeyStore()
	id, _ := p.IssueChallenge("acc1", tnow)
	if err := p.ConsumeChallenge(id, "acc2", tnow); err == nil {
		t.Fatal("challenge bound to account")
	}
}

func TestPasskeyChallengeExpiry(t *testing.T) {
	p := NewPasskeyStore()
	id, _ := p.IssueChallenge("acc1", tnow)
	if err := p.ConsumeChallenge(id, "acc1", tnow.Add(3*time.Minute)); err == nil {
		t.Fatal("expired challenge rejected")
	}
}

func TestRecoveryCredentialRotation(t *testing.T) {
	p := NewPasskeyStore()
	if err := p.EnrolRecoveryCredential("acc1", "rc1", tnow); err != nil {
		t.Fatal(err)
	}
	if err := p.EnrolRecoveryCredential("acc1", "rc2", tnow); err != nil {
		t.Fatal(err)
	}
	// Cap enforcement.
	if err := p.EnrolRecoveryCredential("acc1", "rc3", tnow); err == nil {
		t.Fatal("recovery credential cap")
	}
	if n := p.ValidRecoveryCount("acc1", tnow); n != 2 {
		t.Fatalf("valid %d", n)
	}
	// Rotation replaces in place, keeping the count stable.
	if err := p.RotateRecoveryCredential("acc1", "rc1", "rc1b", tnow); err != nil {
		t.Fatal(err)
	}
	if err := p.UseRecoveryCredential("acc1", "rc1", tnow); err == nil {
		t.Fatal("rotated-away credential must be unusable")
	}
	if err := p.UseRecoveryCredential("acc1", "rc1b", tnow); err != nil {
		t.Fatal(err)
	}
	if n := p.ValidRecoveryCount("acc1", tnow); n != 2 {
		t.Fatalf("rotation must preserve valid count: %d", n)
	}
	// Expiry counts against validity.
	if n := p.ValidRecoveryCount("acc1", tnow.Add(366*24*time.Hour)); n != 0 {
		t.Fatalf("expired creds must not count: %d", n)
	}
}
