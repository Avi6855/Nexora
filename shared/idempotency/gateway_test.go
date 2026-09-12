package idempotency

import (
	"errors"
	"testing"
	"time"
)

func TestGatewayTransitions(t *testing.T) {
	g := NewGateway(time.Hour)
	rec, err := g.Request("k1", "POST", "/v1/payments", []byte(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != StateRequested {
		t.Fatalf("expected REQUESTED, got %s", rec.State)
	}
	rec, err = g.StartProcessing("k1")
	if err != nil || rec.State != StateProcessing {
		t.Fatalf("expected PROCESSING, got %v %v", rec.State, err)
	}
	rec, err = g.Complete("k1", []byte(`{"ok":true}`))
	if err != nil || rec.State != StateSucceeded {
		t.Fatalf("expected SUCCEEDED, got %v %v", rec.State, err)
	}
	// Failed path.
	if _, err := g.Request("k2", "POST", "/v1/payments", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := g.StartProcessing("k2"); err != nil {
		t.Fatal(err)
	}
	rec, err = g.Fail("k2")
	if err != nil || rec.State != StateFailed {
		t.Fatalf("expected FAILED, got %v %v", rec.State, err)
	}
}

func TestGatewayExecuteDedupe(t *testing.T) {
	g := NewGateway(time.Hour)
	calls := 0
	fn := func() ([]byte, error) { calls++; return []byte(`{"n":1}`), nil }
	resp, replayed, err := g.Execute("k", "POST", "/v1/payments", []byte("body"), fn)
	if err != nil || replayed || string(resp) != `{"n":1}` {
		t.Fatalf("first execute failed: %v %v %s", replayed, err, resp)
	}
	resp, replayed, err = g.Execute("k", "POST", "/v1/payments", []byte("body"), fn)
	if err != nil || !replayed || string(resp) != `{"n":1}` {
		t.Fatalf("expected replay, got replayed=%v err=%v resp=%s", replayed, err, resp)
	}
	if calls != 1 {
		t.Fatalf("expected 1 execution, got %d", calls)
	}
}

func TestGatewayProcessingConflictRetry(t *testing.T) {
	g := NewGateway(time.Hour)
	if _, err := g.Request("k", "POST", "/v1/payments", []byte("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.StartProcessing("k"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Execute("k", "POST", "/v1/payments", []byte("b"), func() ([]byte, error) {
		return []byte("x"), nil
	}); !errors.Is(err, ErrConflictRetry) {
		t.Fatalf("expected ErrConflictRetry, got %v", err)
	}
}

func TestGatewayFingerprintStability(t *testing.T) {
	a := RequestFingerprint("POST", "/v1/payments", []byte("hello"), "key-1")
	b := RequestFingerprint("POST", "/v1/payments", []byte("hello"), "key-1")
	if a != b {
		t.Fatal("fingerprint must be stable for identical inputs")
	}
	c := RequestFingerprint("POST", "/v1/payments", []byte("different"), "key-1")
	if a == c {
		t.Fatal("fingerprint must change when the body changes")
	}
	d := RequestFingerprint("POST", "/v1/payments", []byte("hello"), "key-2")
	if a == d {
		t.Fatal("fingerprint must change when the key changes")
	}
}

func TestGatewayExpiryReexecutes(t *testing.T) {
	now := time.Now().UTC()
	g := NewGateway(10 * time.Millisecond)
	g.Now = func() time.Time { return now }
	calls := 0
	fn := func() ([]byte, error) { calls++; return []byte("v1"), nil }
	if _, _, err := g.Execute("k", "POST", "/p", []byte("b"), fn); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if n := g.Sweep(); n != 1 {
		t.Fatalf("expected 1 swept, got %d", n)
	}
	rec, err := g.Get("k")
	if err != nil || rec.State != StateExpired {
		t.Fatalf("expected EXPIRED, got %v %v", rec, err)
	}
	fn2 := func() ([]byte, error) { calls++; return []byte("v2"), nil }
	resp, replayed, err := g.Execute("k", "POST", "/p", []byte("b"), fn2)
	if err != nil || replayed || string(resp) != "v2" {
		t.Fatalf("expected re-execution after expiry, got %v %v %s", replayed, err, resp)
	}
	if calls != 2 {
		t.Fatalf("expected 2 executions, got %d", calls)
	}
}
