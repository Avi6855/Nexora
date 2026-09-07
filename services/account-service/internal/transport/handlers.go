package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/account-service/internal/domain"
	"github.com/nexora/nexora/services/account-service/internal/service"
)

type Handlers struct {
	accountService *service.AccountService
	logger         zerolog.Logger
}

func NewHandlers(accountService *service.AccountService, logger zerolog.Logger) *Handlers {
	return &Handlers{
		accountService: accountService,
		logger:         logger,
	}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/accounts", h.CreateAccount).Methods("POST")
	router.HandleFunc("/v1/accounts", h.GetAccounts).Methods("GET")
	router.HandleFunc("/v1/accounts/{id}", h.GetAccount).Methods("GET")
	router.HandleFunc("/v1/accounts/{id}/balance", h.GetBalance).Methods("GET")
	router.HandleFunc("/v1/accounts/{id}/lockdown", h.SetLockdown).Methods("PUT", "POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreateAccount(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	account, err := h.accountService.CreateAccount(r.Context(), userID, req.AccountType, req.Currency)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, account)
}

func (h *Handlers) GetAccounts(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		respondError(w, http.StatusBadRequest, "X-User-ID header is required")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	accounts, err := h.accountService.GetAccountsByUser(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, accounts)
}

func (h *Handlers) GetAccount(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	account, err := h.accountService.GetAccount(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	// Ownership: the account may only be read by its owner (or an internal
	// service carrying the internal token). X-User-ID is set by the auth
	// middleware from the verified JWT subject.
	if r.Header.Get("X-Internal-Token") == "" {
		caller, perr := uuid.Parse(r.Header.Get("X-User-ID"))
		if perr != nil || caller != account.UserID {
			respondError(w, http.StatusForbidden, "you can only access your own accounts")
			return
		}
	}

	respondJSON(w, http.StatusOK, account)
}

func (h *Handlers) GetBalance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	if r.Header.Get("X-Internal-Token") == "" {
		account, aerr := h.accountService.GetAccount(r.Context(), id)
		if aerr != nil {
			respondError(w, http.StatusNotFound, aerr.Error())
			return
		}
		caller, perr := uuid.Parse(r.Header.Get("X-User-ID"))
		if perr != nil || caller != account.UserID {
			respondError(w, http.StatusForbidden, "you can only access your own accounts")
			return
		}
	}

	available, current, reserved, err := h.accountService.GetBalance(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"available_balance": available,
		"current_balance":   current,
		"reserved_balance":  reserved,
	})
}

// SetLockdown toggles the emergency lockdown switch (money out blocked, money
// in allowed). Only the account owner can toggle it.
func (h *Handlers) SetLockdown(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	caller, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req domain.SetLockdownRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	account, err := h.accountService.SetLockdown(r.Context(), caller, id, req.LockdownEnabled)
	if err != nil {
		if err.Error() == "account does not belong to caller" {
			respondError(w, http.StatusForbidden, err.Error())
			return
		}
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"account_id":       account.AccountID,
		"lockdown_enabled": account.LockdownEnabled,
		"status":           account.Status,
	})
}
