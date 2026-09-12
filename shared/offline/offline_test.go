package offline

import (
	"testing"
)

const testSecret = "device-secret-abc"

func signedFor(secret string, in SignedIntent) SignedIntent {
	in.Signature = SignIntent(secret, in)
	return in
}

func TestOfflineQueueAndSyncCommit(t *testing.T) {
	s := NewIntentStore()
	if err := s.RegisterDevice("dev-1", testSecret); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	first := signedFor(testSecret, SignedIntent{
		RequestID: "req-1", Account: "acct-1", Amount: 1000,
		Payee: "alice", Seq: 1, PrevHash: "",
		Blob: []byte("opaque-encrypted-1"),
	})
	second := signedFor(testSecret, SignedIntent{
		RequestID: "req-2", Account: "acct-1", Amount: 500,
		Payee: "bob", Seq: 2, PrevHash: ChainHash("", first),
		Blob: []byte("opaque-encrypted-2"),
	})
	if err := s.QueueIntent("dev-1", first); err != nil {
		t.Fatalf("queue 1 failed: %v", err)
	}
	if err := s.QueueIntent("dev-1", second); err != nil {
		t.Fatalf("queue 2 failed: %v", err)
	}
	results, err := s.Sync("dev-1")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Status != StatusCommitted {
			t.Fatalf("result %d must be COMMITTED, got %s (%s)", i, r.Status, r.Reason)
		}
	}
	if n := s.Pending("dev-1"); n != 0 {
		t.Fatalf("queue must drain after sync, pending=%d", n)
	}
}

func TestReplayOfSameRequestIDIsIdempotent(t *testing.T) {
	s := NewIntentStore()
	if err := s.RegisterDevice("dev-dup", testSecret); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	first := signedFor(testSecret, SignedIntent{
		RequestID: "req-dup", Account: "acct-1", Amount: 100,
		Payee: "alice", Seq: 1, PrevHash: "",
	})
	if err := s.QueueIntent("dev-dup", first); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	res, err := s.Sync("dev-dup")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(res) != 1 || res[0].Status != StatusCommitted {
		t.Fatalf("first submit must commit, got %+v", res)
	}
	// Double submit of the same request_id returns the original result.
	replay := signedFor(testSecret, SignedIntent{
		RequestID: "req-dup", Account: "acct-1", Amount: 100,
		Payee: "alice", Seq: 2, PrevHash: ChainHash("", first),
	})
	if err := s.QueueIntent("dev-dup", replay); err != nil {
		t.Fatalf("queue replay failed: %v", err)
	}
	res2, err := s.Sync("dev-dup")
	if err != nil {
		t.Fatalf("sync replay failed: %v", err)
	}
	if len(res2) != 1 {
		t.Fatalf("expected 1 replay result, got %d", len(res2))
	}
	if res2[0].Status != StatusDuplicate {
		t.Fatalf("replay must be DUPLICATE, got %s", res2[0].Status)
	}
}

func TestOldSeqRejected(t *testing.T) {
	s := NewIntentStore()
	if err := s.RegisterDevice("dev-seq", testSecret); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	first := signedFor(testSecret, SignedIntent{
		RequestID: "req-s1", Account: "a", Amount: 10, Payee: "p", Seq: 1, PrevHash: "",
	})
	if err := s.QueueIntent("dev-seq", first); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	if _, err := s.Sync("dev-seq"); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	// Replay an old seq number with a fresh request_id.
	stale := signedFor(testSecret, SignedIntent{
		RequestID: "req-s2", Account: "a", Amount: 10, Payee: "p", Seq: 1, PrevHash: "whatever",
	})
	if err := s.QueueIntent("dev-seq", stale); err != nil {
		t.Fatalf("queue stale failed: %v", err)
	}
	res, err := s.Sync("dev-seq")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(res) != 1 || res[0].Status != StatusRejected {
		t.Fatalf("old seq must be REJECTED, got %+v", res)
	}
}

func TestTamperedSignatureRejected(t *testing.T) {
	s := NewIntentStore()
	if err := s.RegisterDevice("dev-sig", testSecret); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	good := signedFor(testSecret, SignedIntent{
		RequestID: "req-t1", Account: "a", Amount: 100, Payee: "mallory", Seq: 1, PrevHash: "",
	})
	good.Amount = 999999 // tamper after signing
	if err := s.QueueIntent("dev-sig", good); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	res, err := s.Sync("dev-sig")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(res) != 1 || res[0].Status != StatusRejected {
		t.Fatalf("tampered signature must be REJECTED, got %+v", res)
	}
}

func TestStalePrevHashHeld(t *testing.T) {
	s := NewIntentStore()
	if err := s.RegisterDevice("dev-held", testSecret); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	intent := signedFor(testSecret, SignedIntent{
		RequestID: "req-h1", Account: "a", Amount: 50, Payee: "p", Seq: 1, PrevHash: "stale-head",
	})
	if err := s.QueueIntent("dev-held", intent); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	res, err := s.Sync("dev-held")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(res) != 1 || res[0].Status != StatusHeld {
		t.Fatalf("stale prev_hash must be HELD, got %+v", res)
	}
}

func TestResolveMerge(t *testing.T) {
	r := NewResolver()
	res, err := r.Resolve(
		VersionedOp{Entity: "pot-1", BaseVersion: 5, Op: OpUpdate, ActorDevice: "a", LamportTs: 10, Metadata: map[string]string{"name": "holiday", "color": "blue"}},
		VersionedOp{Entity: "pot-1", BaseVersion: 5, Op: OpUpdate, ActorDevice: "b", LamportTs: 11, Metadata: map[string]string{"target": "500", "color": "green"}},
	)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.Decision != DecisionMerge {
		t.Fatalf("expected MERGE, got %s", res.Decision)
	}
	if res.Merged["name"] != "holiday" || res.Merged["target"] != "500" {
		t.Fatalf("union lost fields: %+v", res.Merged)
	}
	// Higher lamport wins per-key conflicts.
	if res.Merged["color"] != "green" {
		t.Fatalf("lamport winner must win key conflict, got %+v", res.Merged)
	}
}

func TestResolveRejectStaleDelete(t *testing.T) {
	r := NewResolver()
	res, err := r.Resolve(
		VersionedOp{Entity: "note-1", BaseVersion: 3, Op: OpDelete, ActorDevice: "a", LamportTs: 7},
		VersionedOp{Entity: "note-1", BaseVersion: 5, Op: OpUpdate, ActorDevice: "b", LamportTs: 9, Metadata: map[string]string{"text": "newer"}},
	)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.Decision != DecisionReject {
		t.Fatalf("expected REJECT, got %s", res.Decision)
	}
}

func TestResolveRetryConcurrentIncrements(t *testing.T) {
	r := NewResolver()
	res, err := r.Resolve(
		VersionedOp{Entity: "counter-1", BaseVersion: 7, Op: OpIncrement, ActorDevice: "a", LamportTs: 3},
		VersionedOp{Entity: "counter-1", BaseVersion: 7, Op: OpIncrement, ActorDevice: "b", LamportTs: 4},
	)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.Decision != DecisionRetry {
		t.Fatalf("expected RETRY, got %s", res.Decision)
	}
}

func TestResolveManualReviewDeleteVsUpdate(t *testing.T) {
	r := NewResolver()
	res, err := r.Resolve(
		VersionedOp{Entity: "doc-1", BaseVersion: 8, Op: OpDelete, ActorDevice: "a", LamportTs: 12},
		VersionedOp{Entity: "doc-1", BaseVersion: 8, Op: OpUpdate, ActorDevice: "b", LamportTs: 13, Metadata: map[string]string{"text": "edit"}},
	)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if res.Decision != DecisionManualReview {
		t.Fatalf("expected MANUAL_REVIEW, got %s", res.Decision)
	}
}
