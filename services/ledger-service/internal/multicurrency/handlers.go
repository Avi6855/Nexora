package multicurrency

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	sharedmc "github.com/nexora/nexora/shared/multicurrency"
	"github.com/rs/zerolog"
)

// Handlers exposes multi-currency balances + FX under /v1/multi-currency.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers returns multi-currency handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all multi-currency routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/multi-currency/balances/credit", h.Credit).Methods("POST")
	router.HandleFunc("/v1/multi-currency/balances/debit", h.Debit).Methods("POST")
	router.HandleFunc("/v1/multi-currency/fx/snapshots", h.Snapshot).Methods("POST")
	router.HandleFunc("/v1/multi-currency/convert", h.Convert).Methods("POST")
	router.HandleFunc("/v1/multi-currency/valuation", h.Valuation).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeMCError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sharedmc.ErrUnknownSnapshot),
		errors.Is(err, sharedmc.ErrMissingRate):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, sharedmc.ErrInsufficientFunds):
		respondError(w, http.StatusConflict, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

type balanceRequest struct {
	Account  string `json:"account"`
	Currency string `json:"currency"`
	Amount   int64  `json:"amount_minor"`
}

// Credit adds funds in one currency.
func (h *Handlers) Credit(w http.ResponseWriter, r *http.Request) {
	var req balanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Account == "" || req.Currency == "" {
		respondError(w, http.StatusBadRequest, "account and currency are required")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount_minor must be positive")
		return
	}
	if err := h.svc.Credit(req.Account, req.Currency, req.Amount); err != nil {
		writeMCError(w, err)
		return
	}
	h.logger.Info().Str("account", req.Account).Str("currency", req.Currency).Int64("amount", req.Amount).Msg("multi-currency credit")
	respondJSON(w, http.StatusCreated, h.svc.Balances(req.Account))
}

// Debit removes funds in one currency.
func (h *Handlers) Debit(w http.ResponseWriter, r *http.Request) {
	var req balanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Account == "" || req.Currency == "" {
		respondError(w, http.StatusBadRequest, "account and currency are required")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount_minor must be positive")
		return
	}
	if err := h.svc.Debit(req.Account, req.Currency, req.Amount); err != nil {
		writeMCError(w, err)
		return
	}
	h.logger.Info().Str("account", req.Account).Str("currency", req.Currency).Int64("amount", req.Amount).Msg("multi-currency debit")
	respondJSON(w, http.StatusOK, h.svc.Balances(req.Account))
}

type snapshotRequest struct {
	Rates map[string]float64 `json:"rates"`
}

// Snapshot captures an FX rate snapshot.
func (h *Handlers) Snapshot(w http.ResponseWriter, r *http.Request) {
	var req snapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Rates) == 0 {
		respondError(w, http.StatusBadRequest, "rates are required")
		return
	}
	id, err := h.svc.SnapshotRates(req.Rates, time.Now().UTC())
	if err != nil {
		writeMCError(w, err)
		return
	}
	h.logger.Info().Str("snapshot_id", id).Msg("fx snapshot captured")
	respondJSON(w, http.StatusCreated, map[string]string{"snapshot_id": id})
}

type convertRequest struct {
	Account    string `json:"account"`
	From       string `json:"from"`
	To         string `json:"to"`
	Amount     int64  `json:"amount_minor"`
	SnapshotID string `json:"snapshot_id"`
	SpreadBps  int64  `json:"spread_bps"`
}

// Convert moves funds across currencies with spread + audit.
func (h *Handlers) Convert(w http.ResponseWriter, r *http.Request) {
	var req convertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Account == "" || req.From == "" || req.To == "" || req.SnapshotID == "" {
		respondError(w, http.StatusBadRequest, "account, from, to and snapshot_id are required")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount_minor must be positive")
		return
	}
	converted, err := h.svc.Convert(req.Account, req.From, req.To, req.Amount, req.SnapshotID, req.SpreadBps)
	if err != nil {
		writeMCError(w, err)
		return
	}
	h.logger.Info().Str("account", req.Account).Str("from", req.From).Str("to", req.To).Int64("converted", converted).Msg("multi-currency conversion")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"converted_minor": converted,
		"balances":        h.svc.Balances(req.Account),
	})
}

// Valuation aggregates into the base currency pinned to a snapshot.
func (h *Handlers) Valuation(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	account, base, snap := q.Get("account"), q.Get("base"), q.Get("snapshot_id")
	if account == "" || base == "" || snap == "" {
		respondError(w, http.StatusBadRequest, "account, base and snapshot_id query parameters are required")
		return
	}
	total, err := h.svc.ValuateTotal(account, base, snap)
	if err != nil {
		writeMCError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"account": account, "base": base, "snapshot_id": snap, "total_minor": total,
	})
}
