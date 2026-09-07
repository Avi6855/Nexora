package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nexora/nexora/services/pot-service/internal/domain"
	"github.com/nexora/nexora/services/pot-service/internal/service"
	"github.com/rs/zerolog"
)

type Handlers struct {
	potService *service.PotService
	logger     zerolog.Logger
}

func NewHandlers(potService *service.PotService, logger zerolog.Logger) *Handlers {
	return &Handlers{potService: potService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/pots", h.GetPots).Methods("GET")
	router.HandleFunc("/v1/pots", h.CreatePot).Methods("POST")
	router.HandleFunc("/v1/pots/{id}", h.GetPot).Methods("GET")
	router.HandleFunc("/v1/pots/{id}/deposit", h.Deposit).Methods("POST")
	router.HandleFunc("/v1/pots/{id}/withdraw", h.Withdraw).Methods("POST")
	router.HandleFunc("/v1/pots/{id}/name", h.RenamePot).Methods("PUT")
	router.HandleFunc("/v1/pots/{id}/roundup", h.SetRoundUp).Methods("PUT")
	router.HandleFunc("/v1/pots/{id}", h.DeletePot).Methods("DELETE")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, domain.ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
	})
}

func parseUUIDParam(r *http.Request, paramName string) (uuid.UUID, error) {
	vars := mux.Vars(r)
	return uuid.Parse(vars[paramName])
}

func getUserID(r *http.Request) (uuid.UUID, error) {
	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		return uuid.Nil, errors.New("X-User-ID header is required")
	}
	return uuid.Parse(userIDStr)
}

func getAccountID(r *http.Request) (uuid.UUID, error) {
	accountIDStr := r.Header.Get("X-Account-ID")
	if accountIDStr == "" {
		return uuid.Nil, errors.New("X-Account-ID header is required")
	}
	return uuid.Parse(accountIDStr)
}

func (h *Handlers) GetPots(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	pots, err := h.potService.GetPotsByUser(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, pots)
}

func (h *Handlers) CreatePot(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req domain.CreatePotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}

	if req.TargetAmount <= 0 {
		respondError(w, http.StatusBadRequest, "target_amount must be positive")
		return
	}

	pot, err := h.potService.CreatePot(r.Context(), userID, &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, pot)
}

func (h *Handlers) GetPot(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid pot ID")
		return
	}

	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	pot, err := h.potService.GetPotForUser(r.Context(), userID, id)
	if err != nil {
		if errors.Is(err, domain.ErrPotNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, pot)
}

func (h *Handlers) Deposit(w http.ResponseWriter, r *http.Request) {
	potID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid pot ID")
		return
	}

	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	accountID, err := getAccountID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req domain.AmountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.potService.Deposit(r.Context(), potID, userID, accountID, req.Amount, ""); err != nil {
		if errors.Is(err, domain.ErrPotNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, service.ErrAccountNotOwned) {
			respondError(w, http.StatusForbidden, "funding account does not belong to you")
			return
		}
		if errors.Is(err, service.ErrInsufficientFunds) {
			respondError(w, http.StatusPaymentRequired, "insufficient funds in the funding account")
			return
		}
		if errors.Is(err, domain.ErrInvalidAmount) || errors.Is(err, domain.ErrPotClosed) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "deposited"})
}

func (h *Handlers) Withdraw(w http.ResponseWriter, r *http.Request) {
	potID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid pot ID")
		return
	}

	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	accountID, err := getAccountID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req domain.AmountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.potService.Withdraw(r.Context(), potID, userID, accountID, req.Amount, ""); err != nil {
		if errors.Is(err, domain.ErrPotNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, service.ErrAccountNotOwned) {
			respondError(w, http.StatusForbidden, "destination account does not belong to you")
			return
		}
		if errors.Is(err, service.ErrInsufficientFunds) || errors.Is(err, domain.ErrInsufficientFunds) {
			respondError(w, http.StatusPaymentRequired, "insufficient pot balance")
			return
		}
		if errors.Is(err, domain.ErrInvalidAmount) || errors.Is(err, domain.ErrPotClosed) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "withdrawn"})
}

func (h *Handlers) RenamePot(w http.ResponseWriter, r *http.Request) {
	potID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid pot ID")
		return
	}

	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req domain.RenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.potService.RenamePot(r.Context(), potID, userID, req.Name); err != nil {
		if errors.Is(err, domain.ErrPotNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrPotClosed) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "pot renamed"})
}

// SetRoundUp toggles automatic round-ups into one of the caller's pots.
func (h *Handlers) SetRoundUp(w http.ResponseWriter, r *http.Request) {
	potID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid pot ID")
		return
	}

	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	pot, err := h.potService.SetRoundUp(r.Context(), potID, userID, req.Enabled)
	if err != nil {
		if errors.Is(err, domain.ErrPotNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrPotClosed) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, pot)
}

func (h *Handlers) DeletePot(w http.ResponseWriter, r *http.Request) {
	potID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid pot ID")
		return
	}

	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.potService.DeletePot(r.Context(), potID, userID); err != nil {
		if errors.Is(err, domain.ErrPotNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrBalanceNonZero) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		if errors.Is(err, domain.ErrPotNotClosed) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "pot deleted"})
}
