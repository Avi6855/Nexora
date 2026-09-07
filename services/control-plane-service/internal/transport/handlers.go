package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/nexora/nexora/services/control-plane-service/internal/domain"
	"github.com/nexora/nexora/services/control-plane-service/internal/regimpact"
	"github.com/nexora/nexora/services/control-plane-service/internal/regreport"
	"github.com/nexora/nexora/services/control-plane-service/internal/service"
)

type Handlers struct {
	healthService *service.HealthService
	dependencyService *service.DependencyService
	regImpact     *regimpact.Analyzer
	regReport     *regreport.Builder
	logger        zerolog.Logger
}

func NewHandlers(healthService *service.HealthService, dependencyService *service.DependencyService, logger zerolog.Logger) *Handlers {
	return &Handlers{
		healthService:     healthService,
		dependencyService: dependencyService,
		regImpact:         regimpact.NewAnalyzer(),
		regReport:         regreport.NewBuilder(),
		logger:            logger,
	}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/control/health", h.GetSystemHealth).Methods("GET")
	router.HandleFunc("/v1/control/services/{name}", h.GetServiceStatus).Methods("GET")

	// ── Service Dependency Health Graph + adaptive shedding ──
	router.HandleFunc("/v1/control/graph", h.GetDependencyGraph).Methods("GET")
	router.HandleFunc("/v1/control/reports", h.RecordReport).Methods("POST")
	router.HandleFunc("/v1/control/throttle/{name}", h.GetThrottle).Methods("GET")

	// ── Regulatory Change Impact Analyzer ──
	router.HandleFunc("/v1/control/regulatory/impact", h.RegImpact).Methods("POST")
	router.HandleFunc("/v1/control/regulatory/services", h.RegRegisterService).Methods("POST")

	// ── Regulatory Reporting Pipeline (evidence provenance) ──
	router.HandleFunc("/v1/control/regulatory/reports", h.RegCreateReport).Methods("POST")
	router.HandleFunc("/v1/control/regulatory/reports/{id}/figures", h.RegAddFigure).Methods("POST")
	router.HandleFunc("/v1/control/regulatory/reports/{id}/validate", h.RegValidateReport).Methods("POST")
	router.HandleFunc("/v1/control/regulatory/reports/{id}/submit", h.RegSubmitReport).Methods("POST")
	router.HandleFunc("/v1/control/regulatory/reports/{id}", h.RegGetReport).Methods("GET")
	router.HandleFunc("/v1/control/regulatory/reports/{id}/evidence/{key}", h.RegEvidence).Methods("GET")
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

// GetDependencyGraph renders the live dependency health graph (the platform
// view the app's System Status screen visualises).
func (h *Handlers) GetDependencyGraph(w http.ResponseWriter, r *http.Request) {
	graph, err := h.dependencyService.GetGraph(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, graph)
}

// RecordReport accepts a service's periodic health report (internal).
func (h *Handlers) RecordReport(w http.ResponseWriter, r *http.Request) {
	var rep domain.HealthReport
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.dependencyService.RecordReport(r.Context(), &rep); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

// GetThrottle answers the shed decision for a service (internal, polled).
func (h *Handlers) GetThrottle(w http.ResponseWriter, r *http.Request) {
	decision, err := h.dependencyService.GetThrottle(r.Context(), mux.Vars(r)["name"])
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, decision)
}
