package lifeevents

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	shared "github.com/nexora/nexora/shared/lifeevents"
)

// Handlers exposes the life-events service over HTTP.
type Handlers struct {
	svc *Service
}

// NewHandlers constructs Handlers.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

// RegisterRoutes mounts /v1/life-events endpoints.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/life-events/bereavements", h.reportBereavement).Methods("POST")
	router.HandleFunc("/v1/life-events/bereavements/{id}/advance", h.advanceCase).Methods("POST")
	router.HandleFunc("/v1/life-events/bereavements/{id}/dispute", h.disputeCase).Methods("POST")
	router.HandleFunc("/v1/life-events/bereavements/{id}/resolve", h.resolveDispute).Methods("POST")
	router.HandleFunc("/v1/life-events/bereavements/{id}/executor-docs", h.addExecutorDoc).Methods("POST")
	router.HandleFunc("/v1/life-events/bereavements/{id}/assets", h.discoverAsset).Methods("POST")
	router.HandleFunc("/v1/life-events/bereavements/{id}/obligations/settle", h.settleObligation).Methods("POST")
	router.HandleFunc("/v1/life-events/workspaces", h.openWorkspace).Methods("POST")
	router.HandleFunc("/v1/life-events/workspaces/{id}/tasks", h.addTask).Methods("POST")
	router.HandleFunc("/v1/life-events/workspaces/{id}/tasks/{task_id}/complete", h.completeTask).Methods("POST")
	router.HandleFunc("/v1/life-events/workspaces/{id}/overdue", h.overdueTasks).Methods("GET")
	router.HandleFunc("/v1/life-events/workspaces/{id}/beneficiaries", h.setBeneficiaries).Methods("POST")
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// domainError maps service/shared failures: 404 unknown IDs, 409 gated or
// conflicting transitions, 400 validation.
func domainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, shared.ErrInvalidTransition),
		errors.Is(err, shared.ErrNotReady),
		errors.Is(err, shared.ErrUnknownTask):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

type reportRequest struct {
	CustomerID  string   `json:"customer_id"`
	Obligations []string `json:"obligations,omitempty"`
}

type actorRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason,omitempty"`
}

type evidenceRequest struct {
	EvidenceID string `json:"evidence_id"`
}

type assetRequest struct {
	AssetID string `json:"asset_id"`
}

type settleRequest struct {
	ObligationID string `json:"obligation_id"`
}

type workspaceRequest struct {
	Kind shared.LifeEventKind `json:"kind"`
}

type taskRequest struct {
	ID        string     `json:"id,omitempty"`
	Title     string     `json:"title"`
	Due       *time.Time `json:"due,omitempty"`
	DependsOn []string   `json:"depends_on,omitempty"`
}

type beneficiariesRequest struct {
	Beneficiaries []shared.Beneficiary `json:"beneficiaries"`
}

func (h *Handlers) reportBereavement(w http.ResponseWriter, r *http.Request) {
	var req reportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CustomerID == "" {
		writeError(w, http.StatusBadRequest, "customer_id is required")
		return
	}
	c, err := h.svc.ReportBereavement(req.CustomerID, req.Obligations)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handlers) advanceCase(w http.ResponseWriter, r *http.Request) {
	var req actorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Actor == "" {
		writeError(w, http.StatusBadRequest, "actor is required")
		return
	}
	c, err := h.svc.AdvanceCase(mux.Vars(r)["id"], req.Actor, req.Reason)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) disputeCase(w http.ResponseWriter, r *http.Request) {
	var req actorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Actor == "" {
		writeError(w, http.StatusBadRequest, "actor is required")
		return
	}
	c, err := h.svc.DisputeCase(mux.Vars(r)["id"], req.Actor, req.Reason)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) resolveDispute(w http.ResponseWriter, r *http.Request) {
	var req actorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Actor == "" {
		writeError(w, http.StatusBadRequest, "actor is required")
		return
	}
	c, err := h.svc.ResolveDispute(mux.Vars(r)["id"], req.Actor)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) addExecutorDoc(w http.ResponseWriter, r *http.Request) {
	var req evidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EvidenceID == "" {
		writeError(w, http.StatusBadRequest, "evidence_id is required")
		return
	}
	c, err := h.svc.AddExecutorDoc(mux.Vars(r)["id"], req.EvidenceID)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) discoverAsset(w http.ResponseWriter, r *http.Request) {
	var req assetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AssetID == "" {
		writeError(w, http.StatusBadRequest, "asset_id is required")
		return
	}
	c, err := h.svc.DiscoverAsset(mux.Vars(r)["id"], req.AssetID)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) settleObligation(w http.ResponseWriter, r *http.Request) {
	var req settleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ObligationID == "" {
		writeError(w, http.StatusBadRequest, "obligation_id is required")
		return
	}
	c, err := h.svc.SettleObligation(mux.Vars(r)["id"], req.ObligationID)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (h *Handlers) openWorkspace(w http.ResponseWriter, r *http.Request) {
	var req workspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}
	ws, err := h.svc.OpenWorkspace(req.Kind)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

func (h *Handlers) addTask(w http.ResponseWriter, r *http.Request) {
	var req taskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	task := shared.Task{ID: req.ID, Title: req.Title, DependsOn: req.DependsOn}
	if req.Due != nil {
		task.Due = *req.Due
	}
	created, err := h.svc.AddTask(mux.Vars(r)["id"], task)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handlers) completeTask(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	completed, err := h.svc.CompleteTask(vars["id"], vars["task_id"])
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, completed)
}

func (h *Handlers) overdueTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := h.svc.OverdueTasks(mux.Vars(r)["id"])
	if err != nil {
		domainError(w, err)
		return
	}
	if tasks == nil {
		tasks = []*shared.Task{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"overdue": tasks})
}

func (h *Handlers) setBeneficiaries(w http.ResponseWriter, r *http.Request) {
	var req beneficiariesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Beneficiaries) == 0 {
		writeError(w, http.StatusBadRequest, "at least one beneficiary is required")
		return
	}
	if err := h.svc.SetBeneficiaries(mux.Vars(r)["id"], req.Beneficiaries); err != nil {
		domainError(w, err)
		return
	}
	ws, err := h.svc.GetWorkspace(mux.Vars(r)["id"])
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}
