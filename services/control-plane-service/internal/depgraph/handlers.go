package depgraph

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// Handlers serves the /v1/depgraph API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds depgraph handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the depgraph routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/depgraph/health", h.PostHealth).Methods("POST")
	router.HandleFunc("/v1/depgraph/graph", h.GetGraph).Methods("GET")
	router.HandleFunc("/v1/depgraph/advice", h.GetAdvice).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type healthRequest struct {
	Service   string  `json:"service"`
	Status    string  `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
}

func (h *Handlers) PostHealth(w http.ResponseWriter, r *http.Request) {
	var req healthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.ReportHealth(req.Service, req.Status, req.LatencyMs); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"status": "recorded", "service": req.Service})
}

func (h *Handlers) GetGraph(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.Graph())
}

func (h *Handlers) GetAdvice(w http.ResponseWriter, r *http.Request) {
	advice := h.svc.Advice()
	if advice == nil {
		advice = []Advice{}
	}
	respondJSON(w, http.StatusOK, advice)
}
