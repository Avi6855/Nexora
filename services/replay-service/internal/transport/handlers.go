package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/replay-service/internal/domain"
	"github.com/nexora/nexora/services/replay-service/internal/service"
)

type Handlers struct {
	replayService *service.ReplayService
	logger        zerolog.Logger
}

func NewHandlers(replayService *service.ReplayService, logger zerolog.Logger) *Handlers {
	return &Handlers{replayService: replayService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/replay", h.CreateReplay).Methods("POST")
	router.HandleFunc("/v1/replay/{id}", h.GetReplay).Methods("GET")
	router.HandleFunc("/v1/replay/{id}/start", h.StartReplay).Methods("POST")
	router.HandleFunc("/v1/replay/{id}/complete", h.CompleteReplay).Methods("POST")
	router.HandleFunc("/v1/replay/transaction", h.ReplayTransaction).Methods("POST")
	router.HandleFunc("/v1/replay/account", h.ReplayAccount).Methods("POST")
	router.HandleFunc("/v1/replay/payment", h.ReplayPayment).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreateReplay(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateReplayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	replay, err := h.replayService.CreateReplay(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, replay)
}

func (h *Handlers) GetReplay(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid replay ID")
		return
	}
	replay, err := h.replayService.GetReplay(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, replay)
}

func (h *Handlers) StartReplay(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid replay ID")
		return
	}
	if err := h.replayService.StartReplay(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "replay started"})
}

func (h *Handlers) CompleteReplay(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid replay ID")
		return
	}
	var req struct {
		EventsCount int `json:"events_count"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.replayService.CompleteReplay(r.Context(), id, req.EventsCount); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "replay completed"})
}

func (h *Handlers) ReplayTransaction(w http.ResponseWriter, r *http.Request) {
	var req domain.ReplayTransactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.replayService.ReplayTransaction(r.Context(), req.TransactionID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handlers) ReplayAccount(w http.ResponseWriter, r *http.Request) {
	var req domain.ReplayAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.replayService.ReplayAccount(r.Context(), req.AccountID, req.FromTime, req.ToTime)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handlers) ReplayPayment(w http.ResponseWriter, r *http.Request) {
	var req domain.ReplayPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.replayService.ReplayPayment(r.Context(), req.PaymentID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}
