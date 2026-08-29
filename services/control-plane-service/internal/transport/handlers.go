package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/nexora/nexora/services/control-plane-service/internal/service"
)

type Handlers struct {
	healthService *service.HealthService
	logger        zerolog.Logger
}

func NewHandlers(healthService *service.HealthService, logger zerolog.Logger) *Handlers {
	return &Handlers{healthService: healthService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/control/health", h.GetSystemHealth).Methods("GET")
	router.HandleFunc("/v1/control/services/{name}", h.GetServiceStatus).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) GetSystemHealth(w http.ResponseWriter, r *http.Request) {
	health, err := h.healthService.GetSystemHealth(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, health)
}

func (h *Handlers) GetServiceStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	status, err := h.healthService.GetServiceStatus(r.Context(), vars["name"])
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, status)
}
