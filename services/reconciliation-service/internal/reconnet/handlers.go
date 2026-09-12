package reconnet

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/recon"
	"github.com/rs/zerolog"
)

// Handlers exposes the reconciliation network over /v1/recon.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds recon network handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts /v1/recon routes on the shared mux router.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/recon/batches", h.CreateBatch).Methods("POST")
	router.HandleFunc("/v1/recon/batches/{id}/ingest", h.Ingest).Methods("POST")
	router.HandleFunc("/v1/recon/batches/{id}/run", h.Run).Methods("POST")
	router.HandleFunc("/v1/recon/batches/{id}/summary", h.Summary).Methods("GET")
	router.HandleFunc("/v1/recon/batches/{id}/repairs", h.Repairs).Methods("GET")
	router.HandleFunc("/v1/recon/batches/{id}/audit", h.Audit).Methods("GET")
	router.HandleFunc("/v1/recon/corrections", h.PostCorrection).Methods("POST")
	router.HandleFunc("/v1/recon/exceptions/{id}/ack", h.AckException).Methods("POST")
	router.HandleFunc("/v1/recon/exceptions/{id}/resolve", h.ResolveException).Methods("POST")
	router.HandleFunc("/v1/recon/exceptions", h.ListExceptions).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, recon.ErrBatchExists):
		return http.StatusConflict
	case errors.Is(err, recon.ErrBatchNotFound),
		errors.Is(err, recon.ErrExceptionNotFound),
		errors.Is(err, recon.ErrCorrectionNotFound):
		return http.StatusNotFound
	case errors.Is(err, recon.ErrExceptionResolved):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

type createBatchRequest struct {
	ID         string                `json:"id,omitempty"`
	Window     recon.Window          `json:"window"`
	Tolerances []recon.ToleranceRule `json:"tolerances"`
}

func (h *Handlers) CreateBatch(w http.ResponseWriter, r *http.Request) {
	var req createBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	b, err := h.svc.OpenBatch(req.ID, req.Window, req.Tolerances)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, b)
}

type ingestRequest struct {
	Source recon.Source      `json:"source"`
	Items  []recon.ReconItem `json:"items"`
}

func (h *Handlers) Ingest(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "batch id is required")
		return
	}
	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.Ingest(id, req.Source, req.Items); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "ingested"})
}

func (h *Handlers) Run(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "batch id is required")
		return
	}
	outcomes, err := h.svc.Run(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"batch_id": id, "outcomes": outcomes})
}

func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	sum, err := h.svc.Summary(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, sum)
}

func (h *Handlers) Repairs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	repairs, err := h.svc.Repairs(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"batch_id": id, "repairs": repairs})
}

func (h *Handlers) PostCorrection(w http.ResponseWriter, r *http.Request) {
	var req recon.Correction
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.svc.PostCorrection(req)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

func (h *Handlers) AckException(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "exception id is required")
		return
	}
	ex, err := h.svc.AcknowledgeException(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, ex)
}

type resolveRequest struct {
	Resolution string `json:"resolution,omitempty"`
}

func (h *Handlers) ResolveException(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "exception id is required")
		return
	}
	var req resolveRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	ex, err := h.svc.ResolveException(id, req.Resolution)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, ex)
}

func (h *Handlers) ListExceptions(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.ExceptionQueue())
}

func (h *Handlers) Audit(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]
	entries := h.svc.Audit(id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"batch_id": id, "entries": entries})
}
