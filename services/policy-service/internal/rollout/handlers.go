package rollout

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// Handlers serves the /v1/rollout API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds rollout handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the rollout routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/rollout/flags", h.CreateFlag).Methods("POST")
	router.HandleFunc("/v1/rollout/flags/{id}/advance", h.Advance).Methods("POST")
	router.HandleFunc("/v1/rollout/flags/{id}/metrics", h.ReportMetrics).Methods("POST")
	router.HandleFunc("/v1/rollout/flags/{id}", h.GetFlag).Methods("GET")
	router.HandleFunc("/v1/rollout/flags/{id}/rollback", h.Rollback).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type createFlagRequest struct {
	Key        string     `json:"key"`
	Targeting  Targeting  `json:"targeting"`
	Thresholds Thresholds `json:"thresholds"`
}

func (h *Handlers) CreateFlag(w http.ResponseWriter, r *http.Request) {
	var req createFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	f, err := h.svc.CreateFlag(req.Key, req.Targeting, req.Thresholds)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, f)
}

func flagID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(mux.Vars(r)["id"])
}

func (h *Handlers) Advance(w http.ResponseWriter, r *http.Request) {
	id, err := flagID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	f, aerr := h.svc.Advance(id)
	if aerr != nil {
		if strings.Contains(aerr.Error(), "not found") {
			respondError(w, http.StatusNotFound, aerr.Error())
			return
		}
		respondError(w, http.StatusConflict, aerr.Error())
		return
	}
	respondJSON(w, http.StatusOK, f)
}

func (h *Handlers) ReportMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := flagID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	var m Metrics
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	f, merr := h.svc.ReportMetrics(id, m)
	if merr != nil {
		respondError(w, http.StatusNotFound, merr.Error())
		return
	}
	respondJSON(w, http.StatusOK, f)
}

func (h *Handlers) GetFlag(w http.ResponseWriter, r *http.Request) {
	id, err := flagID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	f, gerr := h.svc.Get(id)
	if gerr != nil {
		respondError(w, http.StatusNotFound, gerr.Error())
		return
	}
	respondJSON(w, http.StatusOK, f)
}

func (h *Handlers) Rollback(w http.ResponseWriter, r *http.Request) {
	id, err := flagID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f, rerr := h.svc.Rollback(id, req.Reason)
	if rerr != nil {
		respondError(w, http.StatusNotFound, rerr.Error())
		return
	}
	respondJSON(w, http.StatusOK, f)
}
