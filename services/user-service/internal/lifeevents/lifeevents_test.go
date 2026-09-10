package lifeevents

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	shared "github.com/nexora/nexora/shared/lifeevents"
)

func TestBereavementWorkflow(t *testing.T) {
	svc := NewService()
	c, err := svc.ReportBereavement("cust1", []string{"dd1"})
	if err != nil {
		t.Fatal(err)
	}
	if !c.OutgoingBlocked {
		t.Fatal("reporting must block outgoing payments")
	}
	if _, err := svc.AdvanceCase(c.CaseID, "agent", "id ok"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvanceCase(c.CaseID, "agent", "protected"); err != nil {
		t.Fatal(err)
	}
	// Executor gate: no verified evidence yet.
	if _, err := svc.AdvanceCase(c.CaseID, "agent", "skip evidence"); err == nil {
		t.Fatal("executor gate must hold without verified evidence")
	}
	if _, err := svc.AddExecutorDoc(c.CaseID, "probate-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DiscoverAsset(c.CaseID, "acc_main"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvanceCase(c.CaseID, "agent", "verified"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvanceCase(c.CaseID, "agent", "discovery"); err != nil {
		t.Fatal(err)
	}
	// Obligations gate.
	if _, err := svc.AdvanceCase(c.CaseID, "agent", "try settle"); err == nil {
		t.Fatal("unsettled obligations must gate the workflow")
	}
	if _, err := svc.SettleObligation(c.CaseID, "dd1"); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"settled", "statement", "distribute", "close"} {
		if _, err := svc.AdvanceCase(c.CaseID, "agent", reason); err != nil {
			t.Fatalf("%s: %v", reason, err)
		}
	}
	got, err := svc.GetCase(c.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stage != shared.BVClosed {
		t.Fatalf("stage %s", got.Stage)
	}
	if _, err := svc.DisputeCase(c.CaseID, "legal", "too late"); err == nil {
		t.Fatal("closed case cannot be disputed")
	}
	if _, err := svc.AdvanceCase("missing", "agent", "x"); err == nil {
		t.Fatal("unknown case must 404")
	}
}

func TestBereavementDisputeRoundTrip(t *testing.T) {
	svc := NewService()
	c, err := svc.ReportBereavement("cust9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvanceCase(c.CaseID, "a", "r1"); err != nil {
		t.Fatal(err)
	}
	before, _ := svc.GetCase(c.CaseID)
	if _, err := svc.DisputeCase(c.CaseID, "legal", "contested"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AdvanceCase(c.CaseID, "a", "nope"); err == nil {
		t.Fatal("disputed case must not advance")
	}
	if _, err := svc.ResolveDispute(c.CaseID, "legal"); err != nil {
		t.Fatal(err)
	}
	after, _ := svc.GetCase(c.CaseID)
	if after.Stage != before.Stage {
		t.Fatalf("must return to %s, got %s", before.Stage, after.Stage)
	}
}

func TestWorkspaceTasksAndBeneficiaries(t *testing.T) {
	svc := NewService()
	w, err := svc.OpenWorkspace(shared.EventMovingHouse)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(w.ID, shared.Task{ID: "notify", Title: "Notify landlord"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(w.ID, shared.Task{ID: "deposit", Title: "Pay deposit", DependsOn: []string{"notify"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTask(w.ID, "deposit"); err == nil {
		t.Fatal("dependency must gate completion")
	}
	if _, err := svc.CompleteTask(w.ID, "notify"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteTask(w.ID, "deposit"); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetWorkspace(w.ID)
	if got.Progress() != 100 {
		t.Fatalf("progress %d", got.Progress())
	}
	if err := svc.SetBeneficiaries(w.ID, []shared.Beneficiary{{Name: "a", ShareBps: 6000}, {Name: "b", ShareBps: 3000}}); err == nil {
		t.Fatal("90% shares must be rejected")
	}
	if err := svc.SetBeneficiaries(w.ID, []shared.Beneficiary{{Name: "a", ShareBps: 10000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(w.ID, shared.Task{ID: "notify", Title: "dup"}); err == nil {
		t.Fatal("duplicate task must fail")
	}
	if _, err := svc.CompleteTask(w.ID, "missing"); err == nil {
		t.Fatal("unknown task must fail")
	}
}

func TestWorkspaceOverdue(t *testing.T) {
	svc := NewService()
	w, err := svc.OpenWorkspace(shared.EventDivorce)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	future := time.Now().UTC().Add(time.Hour)
	if _, err := svc.AddTask(w.ID, shared.Task{ID: "t1", Title: "late", Due: past}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddTask(w.ID, shared.Task{ID: "t2", Title: "later", Due: future}); err != nil {
		t.Fatal(err)
	}
	od, err := svc.OverdueTasks(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(od) != 1 || od[0].ID != "t1" {
		t.Fatalf("overdue: %+v", od)
	}
}

func TestLifeEventsHTTPRoutes(t *testing.T) {
	svc := NewService()
	h := NewHandlers(svc)
	router := mux.NewRouter()
	h.RegisterRoutes(router)

	// POST /bereavements → 201, then 400 on missing customer.
	body, _ := json.Marshal(map[string]interface{}{"customer_id": "cust-http", "obligations": []string{"dd1"}})
	req := httptest.NewRequest("POST", "/v1/life-events/bereavements", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("report status %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	caseID, _ := created["CaseID"].(string)
	if caseID == "" {
		t.Fatalf("missing CaseID: %v", created)
	}

	req = httptest.NewRequest("POST", "/v1/life-events/bereavements", bytes.NewReader([]byte(`{}`)))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty report status %d", rec.Code)
	}

	// Unknown case advance → 404.
	advBody, _ := json.Marshal(map[string]string{"actor": "agent"})
	req = httptest.NewRequest("POST", "/v1/life-events/bereavements/missing/advance", bytes.NewReader(advBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing advance status %d", rec.Code)
	}

	// Workspaces: open → add task → overdue → beneficiaries.
	wsBody, _ := json.Marshal(map[string]string{"kind": string(shared.EventBaby)})
	req = httptest.NewRequest("POST", "/v1/life-events/workspaces", bytes.NewReader(wsBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("workspace status %d: %s", rec.Code, rec.Body.String())
	}
	var ws map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatal(err)
	}
	wsID, _ := ws["ID"].(string)
	if wsID == "" {
		t.Fatalf("missing workspace ID: %v", ws)
	}

	taskBody, _ := json.Marshal(map[string]string{"id": "t1", "title": "Buy crib"})
	req = httptest.NewRequest("POST", "/v1/life-events/workspaces/"+wsID+"/tasks", bytes.NewReader(taskBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add task status %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("GET", "/v1/life-events/workspaces/"+wsID+"/overdue", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("overdue status %d", rec.Code)
	}

	benBody, _ := json.Marshal(map[string]interface{}{
		"beneficiaries": []map[string]interface{}{{"Name": "kid", "ShareBps": 10000}},
	})
	req = httptest.NewRequest("POST", "/v1/life-events/workspaces/"+wsID+"/beneficiaries", bytes.NewReader(benBody))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("beneficiaries status %d: %s", rec.Code, rec.Body.String())
	}

	// Bad shares → 400/409.
	badBen, _ := json.Marshal(map[string]interface{}{
		"beneficiaries": []map[string]interface{}{{"Name": "kid", "ShareBps": 5000}},
	})
	req = httptest.NewRequest("POST", "/v1/life-events/workspaces/"+wsID+"/beneficiaries", bytes.NewReader(badBen))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusConflict {
		t.Fatalf("bad shares status %d", rec.Code)
	}
}
