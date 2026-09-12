package offlineops

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/offline"
	"github.com/rs/zerolog"
)

func newTestHandlers() *Handlers {
	return NewHandlers(NewService(), zerolog.Nop())
}

func doRequest(h *Handlers, method, path string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	rec := httptest.NewRecorder()
	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)
	return rec
}

func queueSigned(t *testing.T, h *Handlers, deviceID, secret, requestID string, seq uint64, prevHash string) {
	t.Helper()
	intent := offline.SignedIntent{
		RequestID: requestID, Account: "acct-1", Amount: 100,
		Payee: "bob", Seq: seq, PrevHash: prevHash,
	}
	intent.Signature = offline.SignIntent(secret, intent)
	rec := doRequest(h, "POST", "/v1/offline/intents/queue", map[string]interface{}{
		"device_id": deviceID, "request_id": intent.RequestID, "account": intent.Account,
		"amount": intent.Amount, "payee": intent.Payee, "seq": intent.Seq,
		"prev_hash": intent.PrevHash, "signature": intent.Signature, "blob": "opaque",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("queue %s status = %d (%s)", requestID, rec.Code, rec.Body.String())
	}
}

func syncDevice(t *testing.T, h *Handlers, deviceID string) []offline.SyncResult {
	t.Helper()
	rec := doRequest(h, "POST", "/v1/offline/sync", map[string]interface{}{"device_id": deviceID})
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d (%s)", rec.Code, rec.Body.String())
	}
	var results []offline.SyncResult
	if err := json.NewDecoder(rec.Body).Decode(&results); err != nil {
		t.Fatalf("decode sync: %v", err)
	}
	return results
}

func TestServiceQueueAndSyncCommit(t *testing.T) {
	svc := NewService()
	const secret = "svc-secret"
	if err := svc.RegisterDevice("svc-dev", secret); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	first := offline.SignedIntent{RequestID: "s-1", Account: "a", Amount: 10, Payee: "p", Seq: 1}
	first.Signature = offline.SignIntent(secret, first)
	if err := svc.QueueIntent("svc-dev", first); err != nil {
		t.Fatalf("queue failed: %v", err)
	}
	second := offline.SignedIntent{RequestID: "s-2", Account: "a", Amount: 5, Payee: "q", Seq: 2, PrevHash: offline.ChainHash("", first)}
	second.Signature = offline.SignIntent(secret, second)
	if err := svc.QueueIntent("svc-dev", second); err != nil {
		t.Fatalf("queue 2 failed: %v", err)
	}
	results, err := svc.Sync("svc-dev")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if len(results) != 2 || results[0].Status != offline.StatusCommitted || results[1].Status != offline.StatusCommitted {
		t.Fatalf("expected 2 COMMITTED, got %+v", results)
	}
}

func TestHTTPDevicesQueueSyncIdempotent(t *testing.T) {
	h := newTestHandlers()
	const secret = "http-secret"

	rec := doRequest(h, "POST", "/v1/offline/devices", map[string]interface{}{
		"device_id": "dev-http", "secret": secret,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d (%s)", rec.Code, rec.Body.String())
	}
	// Re-register with a different secret → 409.
	rec = doRequest(h, "POST", "/v1/offline/devices", map[string]interface{}{
		"device_id": "dev-http", "secret": "other",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflicting re-register status = %d, want 409", rec.Code)
	}

	queueSigned(t, h, "dev-http", secret, "req-http-1", 1, "")
	results := syncDevice(t, h, "dev-http")
	if len(results) != 1 || results[0].Status != offline.StatusCommitted {
		t.Fatalf("expected COMMITTED, got %+v", results)
	}

	// Replay the same request_id → DUPLICATE, never double-commits.
	intent := offline.SignedIntent{RequestID: "req-http-1", Account: "acct-1", Amount: 100, Payee: "bob", Seq: 2, PrevHash: "anything"}
	intent.Signature = offline.SignIntent(secret, intent)
	rec = doRequest(h, "POST", "/v1/offline/intents/queue", map[string]interface{}{
		"device_id": "dev-http", "request_id": intent.RequestID, "account": intent.Account,
		"amount": intent.Amount, "payee": intent.Payee, "seq": intent.Seq,
		"prev_hash": intent.PrevHash, "signature": intent.Signature,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("queue replay status = %d", rec.Code)
	}
	results = syncDevice(t, h, "dev-http")
	if len(results) != 1 || results[0].Status != offline.StatusDuplicate {
		t.Fatalf("expected DUPLICATE, got %+v", results)
	}
}

func TestHTTPOldSeqAndTamperedSignatureRejected(t *testing.T) {
	h := newTestHandlers()
	const secret = "seq-secret"
	rec := doRequest(h, "POST", "/v1/offline/devices", map[string]interface{}{
		"device_id": "dev-seq", "secret": secret,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d", rec.Code)
	}
	queueSigned(t, h, "dev-seq", secret, "req-seq-1", 1, "")
	if got := syncDevice(t, h, "dev-seq"); len(got) != 1 || got[0].Status != offline.StatusCommitted {
		t.Fatalf("setup commit failed: %+v", got)
	}

	// Old seq with fresh request_id → REJECTED.
	queueSigned(t, h, "dev-seq", secret, "req-seq-2", 1, "bogus")
	if got := syncDevice(t, h, "dev-seq"); len(got) != 1 || got[0].Status != offline.StatusRejected {
		t.Fatalf("old seq must be REJECTED, got %+v", got)
	}

	// Tampered amount after signing → REJECTED.
	good := offline.SignedIntent{RequestID: "req-tamper", Account: "a", Amount: 100, Payee: "p", Seq: 5, PrevHash: "x"}
	sig := offline.SignIntent(secret, good)
	rec = doRequest(h, "POST", "/v1/offline/intents/queue", map[string]interface{}{
		"device_id": "dev-seq", "request_id": "req-tamper", "account": "a",
		"amount": 999999, "payee": "p", "seq": 5, "prev_hash": "x", "signature": sig,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("queue tampered status = %d", rec.Code)
	}
	if got := syncDevice(t, h, "dev-seq"); len(got) != 1 || got[0].Status != offline.StatusRejected {
		t.Fatalf("tampered signature must be REJECTED, got %+v", got)
	}

	// Unknown device → 404.
	rec = doRequest(h, "POST", "/v1/offline/sync", map[string]interface{}{"device_id": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown sync status = %d, want 404", rec.Code)
	}
}

func TestHTTPConflictResolutions(t *testing.T) {
	h := newTestHandlers()
	cases := []struct {
		name   string
		local  offline.VersionedOp
		remote offline.VersionedOp
		want   offline.Decision
	}{
		{
			"merge",
			offline.VersionedOp{Entity: "e1", BaseVersion: 5, Op: offline.OpUpdate, ActorDevice: "a", LamportTs: 1, Metadata: map[string]string{"x": "1"}},
			offline.VersionedOp{Entity: "e1", BaseVersion: 5, Op: offline.OpUpdate, ActorDevice: "b", LamportTs: 2, Metadata: map[string]string{"y": "2"}},
			offline.DecisionMerge,
		},
		{
			"reject",
			offline.VersionedOp{Entity: "e2", BaseVersion: 3, Op: offline.OpDelete, ActorDevice: "a", LamportTs: 1},
			offline.VersionedOp{Entity: "e2", BaseVersion: 5, Op: offline.OpUpdate, ActorDevice: "b", LamportTs: 2, Metadata: map[string]string{"k": "v"}},
			offline.DecisionReject,
		},
		{
			"retry",
			offline.VersionedOp{Entity: "e3", BaseVersion: 7, Op: offline.OpIncrement, ActorDevice: "a", LamportTs: 1},
			offline.VersionedOp{Entity: "e3", BaseVersion: 7, Op: offline.OpIncrement, ActorDevice: "b", LamportTs: 2},
			offline.DecisionRetry,
		},
		{
			"manual",
			offline.VersionedOp{Entity: "e4", BaseVersion: 8, Op: offline.OpDelete, ActorDevice: "a", LamportTs: 1},
			offline.VersionedOp{Entity: "e4", BaseVersion: 8, Op: offline.OpUpdate, ActorDevice: "b", LamportTs: 2, Metadata: map[string]string{"k": "v"}},
			offline.DecisionManualReview,
		},
	}
	for _, tc := range cases {
		rec := doRequest(h, "POST", "/v1/offline/conflicts/resolve", map[string]interface{}{
			"local": tc.local, "remote": tc.remote,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (%s)", tc.name, rec.Code, rec.Body.String())
		}
		var res offline.Resolution
		if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
			t.Fatalf("%s: decode: %v", tc.name, err)
		}
		if res.Decision != tc.want {
			t.Fatalf("%s: decision = %s, want %s", tc.name, res.Decision, tc.want)
		}
	}
	// Bad body → 400.
	rec := doRequest(h, "POST", "/v1/offline/conflicts/resolve", map[string]interface{}{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad resolve status = %d, want 400", rec.Code)
	}
}
