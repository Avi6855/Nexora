package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/audit-service/internal/domain"
	"github.com/nexora/nexora/services/audit-service/internal/service"
)

type Handlers struct {
	auditService *service.AuditService
	logger       zerolog.Logger
}

func NewHandlers(auditService *service.AuditService, logger zerolog.Logger) *Handlers {
	return &Handlers{auditService: auditService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/audit/events", h.RecordEvent).Methods("POST")
	router.HandleFunc("/v1/audit/events/{id}", h.GetEvent).Methods("GET")
	router.HandleFunc("/v1/audit/events/resource/{type}/{id}", h.QueryAuditTrail).Methods("GET")
	router.HandleFunc("/v1/audit/events/user/{userId}", h.QueryUserAuditTrail).Methods("GET")
	router.HandleFunc("/v1/audit/logs", h.CreateAuditLog).Methods("POST")
	router.HandleFunc("/v1/audit/logs/{id}", h.GetAuditLog).Methods("GET")
	router.HandleFunc("/v1/audit/users/{userId}/logs", h.GetUserAuditLogs).Methods("GET")
	router.HandleFunc("/v1/audit/resources/{type}/{id}/logs", h.GetResourceAuditLogs).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) RecordEvent(w http.ResponseWriter, r *http.Request) {
	var req domain.RecordEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.auditService.RecordEventFromRequest(r.Context(), &req); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"message": "event recorded"})
}

func (h *Handlers) GetEvent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid event ID")
		return
	}
	event, err := h.auditService.GetAuditEvent(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, event)
}

func (h *Handlers) QueryAuditTrail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	events, err := h.auditService.QueryAuditTrail(r.Context(), vars["type"], vars["id"], limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, events)
}

func (h *Handlers) QueryUserAuditTrail(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	events, err := h.auditService.QueryUserAuditTrail(r.Context(), vars["userId"], limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, events)
}

func (h *Handlers) CreateAuditLog(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateAuditLogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	log, err := h.auditService.CreateAuditLog(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, log)
}

func (h *Handlers) GetAuditLog(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid log ID")
		return
	}
	log, err := h.auditService.GetAuditLog(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, log)
}

func (h *Handlers) GetUserAuditLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	userID, err := uuid.Parse(vars["userId"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	logs, err := h.auditService.GetAuditLogsByUser(r.Context(), userID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, logs)
}

func (h *Handlers) GetResourceAuditLogs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	logs, err := h.auditService.GetAuditLogsByResource(r.Context(), vars["type"], vars["id"])
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, logs)
}
