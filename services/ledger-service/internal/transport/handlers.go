package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/ledger-service/internal/domain"
	"github.com/nexora/nexora/services/ledger-service/internal/service"
)

type Handlers struct {
	ledgerService *service.LedgerService
	logger        zerolog.Logger
}

func NewHandlers(ledgerService *service.LedgerService, logger zerolog.Logger) *Handlers {
	return &Handlers{
		ledgerService: ledgerService,
		logger:        logger,
	}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/ledger/transactions", h.CreateDoubleEntryTransaction).Methods("POST")
	router.HandleFunc("/v1/ledger/transactions/{id}", h.GetTransaction).Methods("GET")
	router.HandleFunc("/v1/ledger/accounts/{id}/balance", h.GetBalance).Methods("GET")
	router.HandleFunc("/v1/ledger/accounts/{id}/entries", h.GetEntries).Methods("GET")
	router.HandleFunc("/v1/ledger/reserve", h.ReserveFunds).Methods("POST")
	router.HandleFunc("/v1/ledger/reservations/{id}/release", h.ReleaseReservation).Methods("POST")
	router.HandleFunc("/v1/ledger/reservations/{id}/settle", h.SettleReservation).Methods("POST")
	router.HandleFunc("/v1/ledger/verify/{id}", h.VerifyIntegrity).Methods("GET")
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

func (h *Handlers) CreateDoubleEntryTransaction(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateDoubleEntryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, entries, err := h.ledgerService.CreateDoubleEntryTransaction(r.Context(), &req)
	if err != nil {
		if err == domain.ErrIdempotencyConflict {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"transaction": tx,
		"entries":     entries,
	})
}

func (h *Handlers) GetTransaction(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid transaction ID")
		return
	}

	tx, err := h.ledgerService.GetTransaction(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, tx)
}

func (h *Handlers) GetBalance(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	breakdown, err := h.ledgerService.GetAccountBalance(r.Context(), accountID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, breakdown)
}

func (h *Handlers) GetEntries(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	entries, err := h.ledgerService.GetEntries(r.Context(), accountID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, entries)
}

type reserveFundsRequest struct {
	AccountID string `json:"account_id"`
	Amount    int64  `json:"amount"`
	Currency  string `json:"currency"`
	TTL       string `json:"ttl"`
	TransactionID string `json:"transaction_id"`
}

func (h *Handlers) ReserveFunds(w http.ResponseWriter, r *http.Request) {
	var req reserveFundsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	ttl := 30 * time.Minute
	if req.TTL != "" {
		parsed, err := time.ParseDuration(req.TTL)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid ttl format, use Go duration like '30m'")
			return
		}
		ttl = parsed
	}

	txID := uuid.New()
	if req.TransactionID != "" {
		parsed, err := uuid.Parse(req.TransactionID)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid transaction_id")
			return
		}
		txID = parsed
	}

	res, err := h.ledgerService.ReserveFunds(r.Context(), accountID, req.Amount, txID, req.Currency, ttl)
	if err != nil {
		if err == domain.ErrInsufficientFunds {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, res)
}

func (h *Handlers) ReleaseReservation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid reservation ID")
		return
	}

	if err := h.ledgerService.ReleaseReservation(r.Context(), id); err != nil {
		switch err {
		case domain.ErrReservationNotFound:
			respondError(w, http.StatusNotFound, err.Error())
		case domain.ErrReservationNotActive:
			respondError(w, http.StatusConflict, err.Error())
		case domain.ErrReservationExpired:
			respondError(w, http.StatusGone, err.Error())
		default:
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "reservation released"})
}

func (h *Handlers) SettleReservation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid reservation ID")
		return
	}

	entry, err := h.ledgerService.SettleReservation(r.Context(), id)
	if err != nil {
		switch err {
		case domain.ErrReservationNotFound:
			respondError(w, http.StatusNotFound, err.Error())
		case domain.ErrReservationNotActive:
			respondError(w, http.StatusConflict, err.Error())
		case domain.ErrReservationExpired:
			respondError(w, http.StatusGone, err.Error())
		default:
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	respondJSON(w, http.StatusOK, entry)
}

func (h *Handlers) VerifyIntegrity(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	result, err := h.ledgerService.VerifyBalanceIntegrity(r.Context(), accountID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	status := http.StatusOK
	if !result.IsBalanced {
		status = http.StatusConflict
	}

	respondJSON(w, status, result)
}

func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{
		"status": "healthy",
		"service": fmt.Sprintf("ledger-service"),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
