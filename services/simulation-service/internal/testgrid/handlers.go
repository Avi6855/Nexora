package testgrid

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	sharedgrid "github.com/nexora/nexora/shared/testgrid"
)

// Handlers exposes the test grid over /v1/test-grid.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds test-grid handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts /v1/test-grid routes on the shared mux router.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/test-grid/scenarios/generate", h.GenerateScenarios).Methods("POST")
	router.HandleFunc("/v1/test-grid/scenarios", h.ListScenarios).Methods("GET")
	router.HandleFunc("/v1/test-grid/chaos/evaluate", h.EvaluateChaos).Methods("POST")
	router.HandleFunc("/v1/test-grid/journeys", h.CreateJourney).Methods("POST")
	router.HandleFunc("/v1/test-grid/journeys/{id}/run-grid", h.RunGrid).Methods("POST")
	router.HandleFunc("/v1/test-grid/journeys/{id}/results", h.GridResults).Methods("GET")
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
	case errors.Is(err, sharedgrid.ErrScenarioNotFound),
		errors.Is(err, sharedgrid.ErrJourneyNotFound),
		errors.Is(err, sharedgrid.ErrJourneyNotRun):
		return http.StatusNotFound
	default:
		return http.StatusBadRequest
	}
}

type generateRequest struct {
	Kind  sharedgrid.IncidentKind `json:"kind"`
	Seed  int64                   `json:"seed"`
	Count int                     `json:"count"`
}

func (h *Handlers) GenerateScenarios(w http.ResponseWriter, r *http.Request) {
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Count == 0 {
		req.Count = 1
	}
	cases, err := h.svc.Generate(req.Kind, req.Seed, req.Count)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("kind", string(req.Kind)).Int("count", len(cases)).Msg("test scenarios generated via API")
	respondJSON(w, http.StatusCreated, map[string]interface{}{"scenarios": cases})
}

func (h *Handlers) ListScenarios(w http.ResponseWriter, r *http.Request) {
	kind := sharedgrid.IncidentKind(r.URL.Query().Get("kind"))
	respondJSON(w, http.StatusOK, map[string]interface{}{"scenarios": h.svc.ListScenarios(kind)})
}

func (h *Handlers) EvaluateChaos(w http.ResponseWriter, r *http.Request) {
	var req sharedgrid.ExperimentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rep, err := h.svc.EvaluateChaos(req)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("target", req.Target).Str("verdict", string(rep.Verdict)).Msg("chaos experiment evaluated via API")
	respondJSON(w, http.StatusOK, rep)
}

type createJourneyRequest struct {
	Steps        []string          `json:"steps"`
	Faults       []string          `json:"faults"`
	Expectations map[string]string `json:"expectations,omitempty"`
}

func (h *Handlers) CreateJourney(w http.ResponseWriter, r *http.Request) {
	var req createJourneyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	j, err := h.svc.CreateJourney(req.Steps, req.Faults, req.Expectations)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("journey_id", j.ID).Msg("journey created via API")
	respondJSON(w, http.StatusCreated, j)
}

func (h *Handlers) RunGrid(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "journey id is required")
		return
	}
	res, err := h.svc.RunGrid(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("journey_id", id).Msg("journey grid run via API")
	respondJSON(w, http.StatusOK, res)
}

func (h *Handlers) GridResults(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "journey id is required")
		return
	}
	res, err := h.svc.GridResults(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}
