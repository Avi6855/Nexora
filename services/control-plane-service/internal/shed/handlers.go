package shed

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/shedding"
)

// Handlers serves the /v1/shed API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds shedding handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the shedding routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/shed/samples", h.PostSample).Methods("POST")
	router.HandleFunc("/v1/shed/decide", h.Decide).Methods("POST")
	router.HandleFunc("/v1/shed/tier", h.GetTier).Methods("GET")
	router.HandleFunc("/v1/shed/policy", h.UpdatePolicy).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) PostSample(w http.ResponseWriter, r *http.Request) {
	var sample shared.Sample
	if err := json.NewDecoder(r.Body).Decode(&sample); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"tier": string(h.svc.Observe(sample))})
}

type decideRequest struct {
	Class  string        `json:"class"`
	Sample shared.Sample `json:"sample"`
}

func (h *Handlers) Decide(w http.ResponseWriter, r *http.Request) {
	var req decideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Class == "" {
		respondError(w, http.StatusBadRequest, "class is required")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.Decide(shared.Class(req.Class), req.Sample))
}

func (h *Handlers) GetTier(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"tier": string(h.svc.Tier())})
}

func (h *Handlers) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var cfg shared.AdaptiveConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.UpdatePolicy(cfg))
}
