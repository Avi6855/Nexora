package transport

import (
	"encoding/json"
	"errors"
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
	router.HandleFunc("/v1/ledger/entries/{id}/note", h.UpdateEntryNote).Methods("PUT")
	router.HandleFunc("/v1/ledger/reserve", h.ReserveFunds).Methods("POST")
	router.HandleFunc("/v1/ledger/reservations/{id}/release", h.ReleaseReservation).Methods("POST")
	router.HandleFunc("/v1/ledger/reservations/{id}/settle", h.SettleReservation).Methods("POST")
	router.HandleFunc("/v1/ledger/verify/{id}", h.VerifyIntegrity).Methods("GET")
	router.HandleFunc("/v1/ledger/transfers", h.BookTransfer).Methods("POST")
	// ── Ledger invariant monitor ──
	router.HandleFunc("/v1/ledger/integrity/scan", h.ScanIntegrity).Methods("POST")
	router.HandleFunc("/v1/ledger/integrity/events", h.ListIntegrityEvents).Methods("GET")
	router.HandleFunc("/v1/ledger/integrity/{id}/clear", h.ClearIntegrityGuard).Methods("POST")
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

// requireAccess authorises an account-scoped operation. Internal service calls
// (X-Internal-Token) pass through; user calls must own every account involved.
// X-User-ID is written by the auth middleware from the verified JWT subject.
func (h *Handlers) requireAccess(w http.ResponseWriter, r *http.Request, accountIDs ...uuid.UUID) bool {
	if r.Header.Get("X-Internal-Token") != "" {
		return true
	}

	caller, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return false
	}

	for _, id := range accountIDs {
		owner, err := h.ledgerService.AccountOwner(r.Context(), id)
		if err != nil || owner != caller {
			respondError(w, http.StatusForbidden, "you can only access your own accounts")
			return false
		}
	}
	return true
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

	ids := make([]uuid.UUID, 0, len(req.Lines))
	for _, line := range req.Lines {
		ids = append(ids, line.AccountID)
	}
	if !h.requireAccess(w, r, ids...) {
		return
	}

	tx, entries, err := h.ledgerService.CreateDoubleEntryTransaction(r.Context(), &req)
	if err != nil {
		if err == domain.ErrIdempotencyConflict {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		if err == domain.ErrAccountLocked {
			respondJSON(w, http.StatusForbidden, map[string]string{
				"error":   "account_locked",
				"message": err.Error(),
			})
			return
		}
		if err == domain.ErrAccountIntegrityViolation {
			respondJSON(w, http.StatusLocked, map[string]string{
				"error":   "account_integrity_violation",
				"message": err.Error(),
			})
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

// BookTransfer is the money-movement primitive used by transfer- and
// pot-service. It is safe to call only with the internal service token
// (middleware), because the availability guarantee lives here.
func (h *Handlers) BookTransfer(w http.ResponseWriter, r *http.Request) {
	var req domain.TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := req.Validate(); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	tx, entries, err := h.ledgerService.BookTransfer(r.Context(), &req)
	if err != nil {
		if err == domain.ErrInsufficientFunds {
			respondError(w, http.StatusPaymentRequired, err.Error())
			return
		}
		if err == domain.ErrAccountLocked {
			respondJSON(w, http.StatusForbidden, map[string]string{
				"error":   "account_locked",
				"message": err.Error(),
			})
			return
		}
		if err == domain.ErrAccountIntegrityViolation {
			respondJSON(w, http.StatusLocked, map[string]string{
				"error":   "account_integrity_violation",
				"message": err.Error(),
			})
			return
		}
		if err == domain.ErrInvalidAmount || err == domain.ErrInvalidCurrency {
			respondError(w, http.StatusBadRequest, err.Error())
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

	if !h.requireAccess(w, r, accountID) {
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

	filter := domain.EntryFilter{Limit: limit}
	filter.Query = r.URL.Query().Get("query")
	filter.Category = r.URL.Query().Get("category")
	filter.Type = r.URL.Query().Get("type")

	if !h.requireAccess(w, r, accountID) {
		return
	}

	entries, err := h.ledgerService.GetEntriesFiltered(r.Context(), accountID, filter)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, entries)
}

// UpdateEntryNote stores or clears the caller's note on a transaction
// ("Add a note" in the app). Notes live in transaction_notes, never in the
// append-only ledger.
func (h *Handlers) UpdateEntryNote(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	entryID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid entry ID")
		return
	}

	var req struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Note) > 500 {
		respondError(w, http.StatusBadRequest, "note must be 500 characters or fewer")
		return
	}

	caller, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	if err := h.ledgerService.UpdateEntryNote(r.Context(), entryID, caller, req.Note); err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			respondError(w, http.StatusForbidden, "you can only annotate your own transactions")
			return
		}
		if errors.Is(err, domain.ErrTransactionNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "note saved"})
}

type reserveFundsRequest struct {
	AccountID     string `json:"account_id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	TTL           string `json:"ttl"`
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

	if !h.requireAccess(w, r, accountID) {
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
		if err == domain.ErrAccountLocked {
			respondJSON(w, http.StatusForbidden, map[string]string{
				"error":   "account_locked",
				"message": err.Error(),
			})
			return
		}
		if err == domain.ErrAccountIntegrityViolation {
			respondJSON(w, http.StatusLocked, map[string]string{
				"error":   "account_integrity_violation",
				"message": err.Error(),
			})
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

	res, err := h.ledgerService.GetReservation(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	if !h.requireAccess(w, r, res.AccountID) {
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

	res, err := h.ledgerService.GetReservation(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	if !h.requireAccess(w, r, res.AccountID) {
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

	if !h.requireAccess(w, r, accountID) {
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
		"status":    "healthy",
		"service":   fmt.Sprintf("ledger-service"),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// ── Ledger invariant monitor ────────────────────────────────────────────────

// ScanIntegrity triggers an immediate full sweep (manual or ticker-driven).
func (h *Handlers) ScanIntegrity(w http.ResponseWriter, r *http.Request) {
	summary, err := h.ledgerService.ScanAllAccountsIntegrity(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, summary)
}

// ListIntegrityEvents returns an account's integrity trail.
func (h *Handlers) ListIntegrityEvents(w http.ResponseWriter, r *http.Request) {
	accountID, err := uuid.Parse(r.URL.Query().Get("account_id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "account_id query parameter is required")
		return
	}
	if !h.requireAccess(w, r, accountID) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := h.ledgerService.ListIntegrityEvents(r.Context(), accountID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = make([]*domain.IntegrityEvent, 0)
	}
	respondJSON(w, http.StatusOK, events)
}

// ClearIntegrityGuard re-verifies an account and lifts the freeze once the
// ledger is clean (engineers call this after repairing entries).
func (h *Handlers) ClearIntegrityGuard(w http.ResponseWriter, r *http.Request) {
	accountID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}
	result, err := h.ledgerService.ClearAccountGuard(r.Context(), accountID)
	if err != nil {
		if err == domain.ErrAccountIntegrityViolation || errors.Is(err, domain.ErrAccountIntegrityViolation) {
			respondJSON(w, http.StatusConflict, map[string]string{
				"error":   "account_still_violated",
				"message": err.Error(),
			})
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}
