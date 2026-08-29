package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
	"github.com/nexora/nexora/services/transfer-service/internal/service"
)

type Handlers struct {
	transferService *service.TransferService
	logger          zerolog.Logger
}

func NewHandlers(transferService *service.TransferService, logger zerolog.Logger) *Handlers {
	return &Handlers{transferService: transferService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/transfers", h.CreateTransfer).Methods("POST")
	router.HandleFunc("/v1/transfers/{id}", h.GetTransfer).Methods("GET")
	router.HandleFunc("/v1/accounts/{id}/transfers", h.GetAccountTransfers).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	transfer, err := h.transferService.CreateTransfer(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, transfer)
}

func (h *Handlers) GetTransfer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	transfer, err := h.transferService.GetTransfer(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, transfer)
}

func (h *Handlers) GetAccountTransfers(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	transfers, err := h.transferService.GetTransfersByAccount(r.Context(), accountID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, transfers)
}
