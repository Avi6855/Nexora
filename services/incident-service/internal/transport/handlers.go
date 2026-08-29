package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/incident-service/internal/domain"
	"github.com/nexora/nexora/services/incident-service/internal/service"
)

type Handlers struct {
	incidentService *service.IncidentService
	logger          zerolog.Logger
}

func NewHandlers(incidentService *service.IncidentService, logger zerolog.Logger) *Handlers {
	return &Handlers{incidentService: incidentService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/incidents", h.CreateIncident).Methods("POST")
	router.HandleFunc("/v1/incidents", h.GetOpenIncidents).Methods("GET")
	router.HandleFunc("/v1/incidents/{id}", h.GetIncident).Methods("GET")
	router.HandleFunc("/v1/incidents/{id}", h.UpdateIncident).Methods("PUT")
	router.HandleFunc("/v1/incidents/{id}/resolve", h.ResolveIncident).Methods("POST")
	router.HandleFunc("/v1/incidents/{id}/status", h.UpdateIncidentStatus).Methods("POST")
	router.HandleFunc("/v1/incidents/{id}/timeline", h.GetTimeline).Methods("GET")
	router.HandleFunc("/v1/incidents/detect", h.DetectIncidents).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreateIncident(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	userIDStr := r.Header.Get("X-User-ID")
	createdBy := uuid.Nil
	if userIDStr != "" {
		createdBy, _ = uuid.Parse(userIDStr)
	}
	incident, err := h.incidentService.CreateIncident(r.Context(), &req, createdBy)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, incident)
}

func (h *Handlers) GetIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid incident ID")
		return
	}
	incident, err := h.incidentService.GetIncident(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, incident)
}

func (h *Handlers) GetOpenIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := h.incidentService.GetOpenIncidents(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, incidents)
}

func (h *Handlers) UpdateIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid incident ID")
		return
	}
	var req domain.UpdateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	incident, err := h.incidentService.UpdateIncident(r.Context(), id, &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, incident)
}

func (h *Handlers) ResolveIncident(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid incident ID")
		return
	}
	var req struct {
		Resolution string `json:"resolution"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.incidentService.ResolveIncident(r.Context(), id, req.Resolution); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "incident resolved"})
}

func (h *Handlers) UpdateIncidentStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid incident ID")
		return
	}
	var req domain.UpdateIncidentStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	newStatus := domain.IncidentStatus(req.Status)
	if err := h.incidentService.UpdateIncidentStatus(r.Context(), id, newStatus, req.Resolution); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "status updated"})
}

func (h *Handlers) GetTimeline(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid incident ID")
		return
	}
	timeline, err := h.incidentService.GetTimeline(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, timeline)
}

func (h *Handlers) DetectIncidents(w http.ResponseWriter, r *http.Request) {
	var req domain.DetectIncidentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := h.incidentService.DetectIncidents(r.Context(), req.Thresholds)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}
