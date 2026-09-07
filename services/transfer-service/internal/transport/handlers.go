package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nexora/nexora/services/transfer-service/internal/domain"
	"github.com/nexora/nexora/services/transfer-service/internal/service"
	"github.com/rs/zerolog"
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

// callerID resolves the authenticated user set by the auth middleware.
func callerID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAccountNotOwned):
		respondError(w, http.StatusForbidden, "you can only move money between your own accounts")
	case errors.Is(err, service.ErrAccountLocked):
		respondError(w, http.StatusForbidden, "this account is locked. Turn off Emergency Lockdown to move money")
	case errors.Is(err, service.ErrInsufficientFunds):
		respondError(w, http.StatusPaymentRequired, "insufficient funds for this transfer")
	case isValidation(err):
		respondError(w, http.StatusBadRequest, err.Error())
	default:
		respondError(w, http.StatusInternalServerError, "transfer could not be completed")
	}
}

func isValidation(err error) bool {
	msg := err.Error()
	for _, prefix := range []string{
		"invalid from account ID",
		"invalid to account ID",
		"from and to accounts must be different",
		"amount must be positive",
		"idempotency_key is required",
		"transfer previously failed",
	} {
		if len(msg) >= len(prefix) && msg[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (h *Handlers) CreateTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req domain.CreateTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	transfer, err := h.transferService.CreateTransfer(r.Context(), userID, &req)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, transfer)
}

func (h *Handlers) GetTransfer(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}

	transfer, err := h.transferService.GetTransferForUser(r.Context(), userID, id)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, transfer)
}

func (h *Handlers) GetAccountTransfers(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	vars := mux.Vars(r)
	accountID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	transfers, err := h.transferService.GetTransfersByAccount(r.Context(), userID, accountID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, transfers)
}
