package credit

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	shared "github.com/nexora/nexora/shared/credit"
)

type Handlers struct {
	svc *Service
}

func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

func (h *Handlers) RegisterCreditRoutes(router *mux.Router) {
	router.HandleFunc("/v1/credit/limit-simulations", h.simulateLimit).Methods("POST")
	router.HandleFunc("/v1/credit/repayment-recommendations", h.recommendRepayment).Methods("POST")
	router.HandleFunc("/v1/credit/bureau-corrections", h.openCorrection).Methods("POST")
	router.HandleFunc("/v1/credit/bureau-corrections/{id}/evidence", h.attachEvidence).Methods("POST")
	router.HandleFunc("/v1/credit/bureau-corrections/{id}/submit", h.submitCorrection).Methods("POST")
	router.HandleFunc("/v1/credit/corrections/tick", h.tickCorrections).Methods("POST")
	router.HandleFunc("/v1/credit/decision-sandbox", h.runSandbox).Methods("POST")
	router.HandleFunc("/v1/credit/decisions/observe", h.observeDecision).Methods("POST")
	router.HandleFunc("/v1/credit/fairness/alerts", h.fairnessAlerts).Methods("GET")
}

// RegisterCreditRoutes is the package-level wiring helper.
func RegisterCreditRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterCreditRoutes(router)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) simulateLimit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Profile  shared.LimitProfile `json:"profile"`
		NewLimit int64               `json:"new_limit_minor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	proj, err := h.svc.SimulateLimit(req.Profile, req.NewLimit)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, proj)
}

func (h *Handlers) recommendRepayment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Debts     []shared.Debt `json:"debts"`
		Budget    int64         `json:"budget_minor"`
		MaxMonths int           `json:"max_months"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rec, err := h.svc.RecommendRepayment(req.Debts, req.Budget, req.MaxMonths)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rec)
}

func (h *Handlers) openCorrection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         string `json:"id"`
		CustomerID string `json:"customer_id"`
		Field      string `json:"field"`
		Detail     string `json:"detail"`
		ReportedBy string `json:"reported_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.svc.OpenCorrection(req.ID, req.CustomerID, req.Field, req.Detail, req.ReportedBy, time.Now().UTC())
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

func (h *Handlers) attachEvidence(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		Item string `json:"item"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.AttachEvidence(id, req.Item); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "evidence attached"})
}

func (h *Handlers) submitCorrection(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.SubmitCorrection(id, time.Now().UTC()); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "correction submitted"})
}

func (h *Handlers) tickCorrections(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Now *time.Time `json:"now"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	now := time.Now().UTC()
	if req.Now != nil && !req.Now.IsZero() {
		now = *req.Now
	}
	escalated := h.svc.TickCorrections(now)
	if escalated == nil {
		escalated = []string{}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"escalated": escalated})
}

func (h *Handlers) runSandbox(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Candidate  shared.Policy      `json:"candidate"`
		Baseline   shared.Policy      `json:"baseline"`
		Population []shared.Applicant `json:"population"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Population) == 0 {
		respondError(w, http.StatusBadRequest, "population must not be empty")
		return
	}
	res, err := h.svc.RunDecisionSandbox(req.Candidate, req.Baseline, req.Population)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

func (h *Handlers) observeDecision(w http.ResponseWriter, r *http.Request) {
	var o shared.FairnessObs
	if err := json.NewDecoder(r.Body).Decode(&o); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if o.Segment == "" {
		respondError(w, http.StatusBadRequest, "segment is required")
		return
	}
	if o.At.IsZero() {
		o.At = time.Now().UTC()
	}
	h.svc.ObserveDecision(o)
	respondJSON(w, http.StatusCreated, map[string]string{"message": "observation recorded"})
}

func (h *Handlers) fairnessAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := h.svc.EvaluateFairness(time.Now().UTC())
	if alerts == nil {
		alerts = []shared.FairnessAlert{}
	}
	respondJSON(w, http.StatusOK, alerts)
}
