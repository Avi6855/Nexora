package distcoord

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/distcoord"
)

func testService() *Service {
	return NewService(zerolog.Nop())
}

func TestServiceCorrelationAndCausality(t *testing.T) {
	s := testService()
	root, err := s.IssueCorrelation("payment.authorize")
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.PropagateCorrelation(root, "fraud.check")
	if err != nil {
		t.Fatal(err)
	}
	if child.CausationID != root.ID {
		t.Fatalf("child causation must be parent id")
	}
	if err := s.ValidateChain([]shared.Correlation{root, child}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCausalEdge("A", "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCausalEdge("B", "C"); err != nil {
		t.Fatal(err)
	}
	chain, err := s.WhyHappened("C")
	if err != nil || len(chain) != 2 {
		t.Fatalf("expected 2-cause chain, got %v %v", chain, err)
	}
}

func TestServiceClocksHLCLocks(t *testing.T) {
	s := testService()
	if err := s.RecordClockSample("n1", 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordClockSample("n2", 5000); err != nil {
		t.Fatal(err)
	}
	if p := s.ClockPolicy(); !p.DisableCritical {
		t.Fatalf("expected skew policy to disable critical, got %+v", p)
	}
	t1 := s.HLCIssue(1000)
	t2 := s.HLCIssue(900)
	if shared.Compare(t2, t1) <= 0 {
		t.Fatalf("HLC must stay monotonic on regression")
	}
	t3 := s.HLCReceive(shared.Timestamp{WallMs: 9000, Logical: 1, Node: "peer"}, 800)
	if t3.WallMs != 9000 {
		t.Fatalf("expected merge to remote wall, got %+v", t3)
	}
	now := time.Now().UTC()
	ok, err := s.LockAcquire("ledger", "a", 100, now)
	if err != nil || !ok {
		t.Fatalf("acquire failed: %v %v", ok, err)
	}
	ok, err = s.LockAcquire("ledger", "b", 100, now)
	if err != nil || ok {
		t.Fatalf("second acquire must queue")
	}
	if stuck := s.StuckLocks(2, now.Add(time.Second)); len(stuck) != 1 {
		t.Fatalf("expected stuck lock, got %v", stuck)
	}
	if stats := s.LockStats(); len(stats) != 1 || stats[0].Contentions != 1 {
		t.Fatalf("bad stats: %+v", stats)
	}
	adv := s.AdviseContention([]shared.WaitEdge{{Waiter: "w", Holder: "h", Resource: "r", HoldMs: 5}})
	if len(adv) != 1 {
		t.Fatalf("expected advice, got %v", adv)
	}
}

func TestHandlersRoutes(t *testing.T) {
	s := testService()
	r := mux.NewRouter()
	NewHandlers(s, zerolog.Nop()).RegisterRoutes(r)
	post := func(path string, body interface{}) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	rec := post("/v1/distcoord/correlations/issue", map[string]string{"action": "pay"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("issue: %d %s", rec.Code, rec.Body.String())
	}
	var root shared.Correlation
	if err := json.NewDecoder(rec.Body).Decode(&root); err != nil {
		t.Fatal(err)
	}
	rec = post("/v1/distcoord/correlations/issue", map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty action must 400, got %d", rec.Code)
	}
	rec = post("/v1/distcoord/correlations/validate", map[string]interface{}{"correlations": []shared.Correlation{root}})
	if rec.Code != http.StatusOK {
		t.Fatalf("validate root: %d %s", rec.Code, rec.Body.String())
	}
	rec = post("/v1/distcoord/correlations/validate", map[string]interface{}{"parent": root, "action": "child"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("propagate: %d %s", rec.Code, rec.Body.String())
	}
	rec = post("/v1/distcoord/causality/edges", map[string]string{"from": "A", "to": "B"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("edge: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/v1/distcoord/causality/why?node=ghost"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown why must 404, got %d", rec.Code)
	}
	if rec := get("/v1/distcoord/causality/why"); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing node must 400, got %d", rec.Code)
	}
	rec = post("/v1/distcoord/clocks/samples", map[string]interface{}{"node": "n1", "wall_ms": 1000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("clock sample: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get("/v1/distcoord/clocks/policy"); rec.Code != http.StatusOK {
		t.Fatalf("policy: %d", rec.Code)
	}
	rec = post("/v1/distcoord/hlc/issue", map[string]interface{}{"wall_ms": 1000})
	if rec.Code != http.StatusOK {
		t.Fatalf("hlc issue: %d", rec.Code)
	}
	rec = post("/v1/distcoord/hlc/receive", map[string]interface{}{"remote": shared.Timestamp{WallMs: 5, Node: "p"}, "wall_ms": 10})
	if rec.Code != http.StatusOK {
		t.Fatalf("hlc receive: %d", rec.Code)
	}
	rec = post("/v1/distcoord/locks/acquire", map[string]interface{}{"resource": "db", "owner": "a", "ttl_ms": 60000})
	if rec.Code != http.StatusCreated {
		t.Fatalf("acquire: %d %s", rec.Code, rec.Body.String())
	}
	rec = post("/v1/distcoord/locks/acquire", map[string]interface{}{"resource": "db", "owner": "b", "ttl_ms": 60000})
	if rec.Code != http.StatusOK {
		t.Fatalf("queued acquire must 200, got %d", rec.Code)
	}
	rec = post("/v1/distcoord/locks/release", map[string]string{"resource": "db", "owner": "b"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("non-owner release must 409, got %d", rec.Code)
	}
	rec = post("/v1/distcoord/locks/release", map[string]string{"resource": "ghost", "owner": "a"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown release must 404, got %d", rec.Code)
	}
	if rec := get("/v1/distcoord/locks/stuck"); rec.Code != http.StatusOK {
		t.Fatalf("stuck: %d", rec.Code)
	}
	if rec := get("/v1/distcoord/locks/stats"); rec.Code != http.StatusOK {
		t.Fatalf("stats: %d", rec.Code)
	}
	rec = post("/v1/distcoord/contention/advise", map[string]interface{}{"edges": []shared.WaitEdge{{Waiter: "w", Holder: "h", Resource: "r"}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("advise: %d", rec.Code)
	}
}
