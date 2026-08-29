package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/simulation-service/internal/domain"
	"github.com/nexora/nexora/services/simulation-service/internal/service"
)

type Handlers struct {
	simService *service.SimulationService
	logger     zerolog.Logger
}

func NewHandlers(simService *service.SimulationService, logger zerolog.Logger) *Handlers {
	return &Handlers{simService: simService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/simulations", h.CreateSimulation).Methods("POST")
	router.HandleFunc("/v1/simulations", h.ListSimulations).Methods("GET")
	router.HandleFunc("/v1/simulations/{id}", h.GetSimulation).Methods("GET")
	router.HandleFunc("/v1/simulations/{id}/start", h.StartSimulation).Methods("POST")
	router.HandleFunc("/v1/simulations/{id}/stop", h.StopSimulation).Methods("POST")
	router.HandleFunc("/v1/simulations/digital-twin", h.CreateDigitalTwin).Methods("POST")
	router.HandleFunc("/v1/simulations/payment", h.SimulatePayment).Methods("POST")
	router.HandleFunc("/v1/simulations/transfer", h.SimulateTransfer).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreateSimulation(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateSimulationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sim, err := h.simService.CreateSimulation(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, sim)
}

func (h *Handlers) ListSimulations(w http.ResponseWriter, r *http.Request) {
	sims, err := h.simService.ListSimulations(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, sims)
}

func (h *Handlers) GetSimulation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid simulation ID")
		return
	}
	sim, err := h.simService.GetSimulation(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, sim)
}

func (h *Handlers) StartSimulation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid simulation ID")
		return
	}
	if err := h.simService.StartSimulation(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "simulation started"})
}

func (h *Handlers) StopSimulation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid simulation ID")
		return
	}
	if err := h.simService.StopSimulation(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "simulation stopped"})
}

func (h *Handlers) CreateDigitalTwin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID string `json:"account_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	twin, err := h.simService.CreateDigitalTwin(r.Context(), req.AccountID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, twin)
}

func (h *Handlers) SimulatePayment(w http.ResponseWriter, r *http.Request) {
	var req domain.SimulatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.simService.SimulatePayment(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handlers) SimulateTransfer(w http.ResponseWriter, r *http.Request) {
	var req domain.SimulateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.simService.SimulateTransfer(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}
