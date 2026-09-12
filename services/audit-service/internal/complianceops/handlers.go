package complianceops

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	shared "github.com/nexora/nexora/shared/complianceops"
)

// Handlers exposes the compliance-ops API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterComplianceOpsRoutes mounts /v1/compliance-ops routes.
func (h *Handlers) RegisterComplianceOpsRoutes(router *mux.Router) {
	router.HandleFunc("/v1/compliance-ops/investigations/{id}/bundle", h.buildBundle).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/bundles/{id}/verify", h.verifyBundle).Methods("GET")
	router.HandleFunc("/v1/compliance-ops/investigations/{id}/snapshot", h.postSnapshot).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/investigations/{id}/snapshot", h.getSnapshot).Methods("GET")
	router.HandleFunc("/v1/compliance-ops/sla-policies", h.putPolicy).Methods("PUT")
	router.HandleFunc("/v1/compliance-ops/sla/cases", h.openCase).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/sla/tick", h.tick).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/sampling/run", h.samplingRun).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/requirements", h.postRequirement).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/coverage", h.coverage).Methods("GET")
	router.HandleFunc("/v1/compliance-ops/control-tests", h.postControlTest).Methods("POST")
	router.HandleFunc("/v1/compliance-ops/control-runs", h.postControlRun).Methods("POST")
}

// RegisterComplianceOpsRoutes is the package-level helper used by cmd/main.go.
func RegisterComplianceOpsRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterComplianceOpsRoutes(router)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func parseTime(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Now().UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func (h *Handlers) buildBundle(w http.ResponseWriter, r *http.Request) {
	var req shared.EvidenceGraph
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	b, err := h.svc.BuildBundle(mux.Vars(r)["id"], req, time.Now().UTC())
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, b)
}

func (h *Handlers) verifyBundle(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	verdict, err := h.svc.VerifyBundle(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	b, err := h.svc.GetBundle(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"id": id, "investigation_id": b.InvestigationID, "verdict": verdict, "valid": verdict == shared.BundleValid,
	})
}

func (h *Handlers) postSnapshot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomerID string `json:"customer_id"`
		State      string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	snap, err := h.svc.SnapshotCase(mux.Vars(r)["id"], req.CustomerID, req.State, time.Now().UTC())
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, snap)
}

func (h *Handlers) getSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := h.svc.GetSnapshot(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, snap)
}

func (h *Handlers) putPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CaseType     string  `json:"case_type"`
		DeadlineHrs  float64 `json:"deadline_hours"`
		DeadlineSecs *int64  `json:"deadline_seconds"`
		Priority     string  `json:"priority"`
		Escalation   string  `json:"escalation"`
		WarningHrs   float64 `json:"warning_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	deadline := time.Duration(req.DeadlineHrs * float64(time.Hour))
	if req.DeadlineSecs != nil {
		deadline = time.Duration(*req.DeadlineSecs) * time.Second
	}
	p := shared.SLAPolicy{
		CaseType:      req.CaseType,
		Deadline:      deadline,
		Priority:      req.Priority,
		Escalation:    req.Escalation,
		WarningBefore: time.Duration(req.WarningHrs * float64(time.Hour)),
	}
	if err := h.svc.SetSLAPolicy(p); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "policy updated"})
}

func (h *Handlers) openCase(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		CaseType  string `json:"case_type"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	created, err := parseTime(req.CreatedAt)
	if err != nil {
		respondError(w, http.StatusBadRequest, "created_at must be RFC3339")
		return
	}
	it, err := h.svc.OpenCaseWithID(req.ID, req.CaseType, created, time.Now().UTC())
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, it)
}

func (h *Handlers) tick(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Now  string `json:"now"`
		Open []struct {
			ID        string `json:"id"`
			CaseType  string `json:"case_type"`
			CreatedAt string `json:"created_at"`
		} `json:"open"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	now, err := parseTime(req.Now)
	if err != nil {
		respondError(w, http.StatusBadRequest, "now must be RFC3339")
		return
	}
	for _, o := range req.Open {
		created, err := parseTime(o.CreatedAt)
		if err != nil {
			respondError(w, http.StatusBadRequest, "created_at must be RFC3339")
			return
		}
		if _, err := h.svc.OpenCaseWithID(o.ID, o.CaseType, created, now); err != nil {
			respondError(w, statusFor(err), err.Error())
			return
		}
	}
	events := h.svc.Tick(now)
	if events == nil {
		events = []shared.SLAEvent{}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"now": now.Format(time.RFC3339), "events": events})
}

func (h *Handlers) samplingRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Population []shared.SampleDecision `json:"population"`
		N          int                     `json:"n"`
		Seed       int64                   `json:"seed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	out, err := h.svc.Sample(req.Population, req.N, req.Seed)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	if out == nil {
		out = []shared.SampleDecision{}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"sample": out, "count": len(out)})
}

func (h *Handlers) postRequirement(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID              string `json:"id"`
		Rule            string `json:"rule"`
		ImplementedRule string `json:"implemented_rule"`
		Service         string `json:"service"`
		TestRef         string `json:"test_ref"`
		MonitorRef      string `json:"monitor_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule := req.Rule
	if rule == "" {
		rule = req.ImplementedRule
	}
	if err := h.svc.RegisterRequirement(shared.Requirement{
		ID: req.ID, Rule: rule, Service: req.Service, TestRef: req.TestRef, MonitorRef: req.MonitorRef,
	}); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"message": "requirement registered"})
}

func (h *Handlers) coverage(w http.ResponseWriter, r *http.Request) {
	rep := h.svc.Coverage()
	if rep == nil {
		rep = []shared.CoverageEntry{}
	}
	respondJSON(w, http.StatusOK, rep)
}

func (h *Handlers) postControlTest(w http.ResponseWriter, r *http.Request) {
	var req shared.ControlTest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RegisterControlTest(req); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"message": "control test registered"})
}

func (h *Handlers) postControlRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Now string `json:"now"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	now, err := parseTime(req.Now)
	if err != nil {
		respondError(w, http.StatusBadRequest, "now must be RFC3339")
		return
	}
	// Empty body decodes to zero time only when "now" is absent; parseTime
	// already defaults to now in that case.
	run, err := h.svc.RunControls(now)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, run)
}
