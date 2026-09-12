package keysec

import (
	"testing"
	"time"
)

func TestKeyLifecycle(t *testing.T) {
	s := NewStore()
	k, err := s.CreateKey("payments", KeyTypeEncryption)
	if err != nil {
		t.Fatal(err)
	}
	if k.Status != StatusCreated || k.Version != 1 {
		t.Fatalf("created: %+v", k)
	}
	if _, err := s.ActiveKey("payments"); err == nil {
		t.Fatal("no activation yet -> no active key (never fall back to latest)")
	}
	if _, err := s.ActivateKey(k.ID); err != nil {
		t.Fatal(err)
	}
	v, err := s.ActiveVersion("payments")
	if err != nil || v != 1 {
		t.Fatalf("active version = %d %v, want 1", v, err)
	}
	// A newer CREATED key must not hijack the explicit active pointer.
	k2, err := s.CreateKey("payments", KeyTypeEncryption)
	if err != nil {
		t.Fatal(err)
	}
	if k2.Version != 2 {
		t.Fatalf("versions increment: %+v", k2)
	}
	v, _ = s.ActiveVersion("payments")
	if v != 1 {
		t.Fatalf("active must stay v1 until explicit activation, got v%d", v)
	}
	// Rotate the active key: successor becomes ACTIVE, old becomes ROTATED.
	next, err := s.RotateKey(k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if next.Version != 3 || next.Status != StatusActive {
		t.Fatalf("rotated successor: %+v", next)
	}
	old, _ := s.GetKey(k.ID)
	if old.Status != StatusRotated {
		t.Fatalf("old key must be ROTATED: %+v", old)
	}
	v, _ = s.ActiveVersion("payments")
	if v != 3 {
		t.Fatalf("active after rotate = v%d, want v3", v)
	}
	if _, err := s.RotateKey(k.ID); err == nil {
		t.Fatal("rotating a non-active key must conflict")
	}
	rev, err := s.RevokeKey(next.ID)
	if err != nil || rev.Status != StatusRevoked {
		t.Fatalf("revoke: %+v %v", rev, err)
	}
	if _, err := s.ActiveKey("payments"); err == nil {
		t.Fatal("revoking active must clear the pointer")
	}
	if _, err := s.DestroyKey(k2.ID); err == nil {
		t.Fatal("only REVOKED keys may be destroyed")
	}
	dst, err := s.DestroyKey(next.ID)
	if err != nil || dst.Status != StatusDestroyed {
		t.Fatalf("destroy: %+v %v", dst, err)
	}
	if _, err := s.ActivateKey(next.ID); err == nil {
		t.Fatal("destroyed key must not activate")
	}
	if _, err := s.GetKey("missing"); err == nil {
		t.Fatal("unknown key must fail")
	}
	if _, err := s.CreateKey("payments", "bogus"); err == nil {
		t.Fatal("bad key type must fail")
	}
}

func TestActiveSurvivesRestart(t *testing.T) {
	s := NewStore()
	a, _ := s.CreateKey("ledger", KeyTypeSigning)
	if _, err := s.ActivateKey(a.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := s.CreateKey("ledger", KeyTypeSigning)
	_ = b
	// Snapshot + restore simulates a process restart: the explicit
	// pointer (v1) must survive, not resolve to the latest created (v2).
	keys, active := s.Snapshot()
	r := Restore(keys, active)
	got, err := r.ActiveKey("ledger")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != a.ID || got.Version != 1 {
		t.Fatalf("restart must preserve active v1, got %+v", got)
	}
}

func TestUsageAnomaly(t *testing.T) {
	s := NewStore()
	k, _ := s.CreateKey("payments", KeyTypeAPICredential)
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		day := base.AddDate(0, 0, i)
		if err := s.ObserveUsage(k.ID, day, 100, "payments", "eu-west", "charge"); err != nil {
			t.Fatal(err)
		}
	}
	// Normal day: no alert.
	if err := s.ObserveUsage(k.ID, base.AddDate(0, 0, 7), 110, "payments", "eu-west", "charge"); err != nil {
		t.Fatal(err)
	}
	if anomalies := s.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("steady traffic must not alert: %+v", anomalies)
	}
	// Spike at 5x baseline alerts with full context.
	if err := s.ObserveUsage(k.ID, base.AddDate(0, 0, 7), 500, "payments", "eu-west", "charge"); err != nil {
		t.Fatal(err)
	}
	anomalies := s.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("spike must alert: %+v", anomalies)
	}
	a := anomalies[0]
	if a.KeyID != k.ID || a.Service != "payments" || a.Region != "eu-west" || a.Operation != "charge" {
		t.Fatalf("anomaly context: %+v", a)
	}
	if a.Baseline < 90 || a.Baseline > 110 || a.Today != 500 {
		t.Fatalf("baseline/today: %+v", a)
	}
	if err := s.ObserveUsage("missing", base, 1, "", "", ""); err == nil {
		t.Fatal("observe on unknown key must fail")
	}
}

func TestSecretScanner(t *testing.T) {
	// Real staged secrets BLOCK.
	verdict, findings := ScanCommit("add payments\n+ api_key = \"nxr-staged-key-9f2k7qz4tm8x1a\"\n")
	if verdict != VerdictBlock || len(findings) != 1 || findings[0].Kind != "api_key" {
		t.Fatalf("api key must BLOCK: %s %+v", verdict, findings)
	}
	verdict, findings = ScanCommit("-----BEGIN RSA PRIVATE KEY-----\nMIIE...")
	if verdict != VerdictBlock || findings[0].Kind != "private_key" {
		t.Fatalf("private key must BLOCK: %s %+v", verdict, findings)
	}
	verdict, findings = ScanCommit("+ customer_credential: \"cust-secret-abc12345\"\n")
	if verdict != VerdictBlock {
		t.Fatalf("customer credential must BLOCK: %s %+v", verdict, findings)
	}
	// Contextual allowlist: example/test fixtures ALLOW.
	for _, benign := range []string{
		"example: api_key = \"EXAMPLE-KEY-FOR-DOCS\"",
		"test fixture api_key = \"test-key-placeholder\"",
		"docs: use api_key = \"xxx\" as a placeholder",
		"fix typo in README, update example fixtures",
		"refactor login handler, no secrets",
	} {
		if v, f := ScanCommit(benign); v != VerdictAllow || len(f) != 0 {
			t.Fatalf("false positive on %q: %s %+v", benign, v, f)
		}
	}
}

func TestPolicySim(t *testing.T) {
	p := Policy{Name: "strict-geo", BlockNewRegion: true, ChallengeNewDevice: true}
	traffic := []TrafficEvent{
		{},
		{NewDevice: true},
		{NewRegion: true},
		{NewDevice: true, NewRegion: true},
		{NewLogin: true},
	}
	res := Simulate(p, traffic)
	if res.Total != 5 {
		t.Fatalf("total = %+v", res)
	}
	// 2 blocked (both new_region), 1 challenged (lone new_device),
	// 2 allowed (clean + new_login without challenge rule).
	if res.Blocked != 2 || res.Challenged != 1 || res.Allowed != 2 {
		t.Fatalf("sim counts: %+v", res)
	}
	open := Policy{Name: "open"}
	res = Simulate(open, traffic)
	if res.Allowed != 5 || res.Blocked != 0 || res.Challenged != 0 {
		t.Fatalf("open policy must allow all: %+v", res)
	}
	empty := Simulate(p, nil)
	if empty.Total != 0 || empty.Allowed != 0 || empty.Blocked != 0 || empty.Challenged != 0 {
		t.Fatalf("empty traffic renders zero counts: %+v", empty)
	}
}
