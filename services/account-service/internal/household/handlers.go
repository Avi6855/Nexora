package household

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	sharedhh "github.com/nexora/nexora/shared/household"
	"github.com/rs/zerolog"
)

// Handlers exposes bills + mandates + closure under /v1/household.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers returns household handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all household routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/household/bills", h.AddBill).Methods("POST")
	router.HandleFunc("/v1/household/bills/compare", h.CompareBills).Methods("POST")
	router.HandleFunc("/v1/household/bills/{id}/action", h.BillAction).Methods("POST")
	router.HandleFunc("/v1/household/mandates", h.AddMandate).Methods("POST")
	router.HandleFunc("/v1/household/mandates/{id}/migrate", h.MigrateMandate).Methods("POST")
	router.HandleFunc("/v1/household/mandates/{id}/verify-first-collection", h.VerifyFirstCollection).Methods("POST")
	router.HandleFunc("/v1/household/closure/scan", h.ScanClosure).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeHHError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sharedhh.ErrBillNotFound),
		errors.Is(err, sharedhh.ErrMandateNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

type addBillRequest struct {
	Provider string `json:"provider"`
	Amount   int64  `json:"amount_minor"`
	Cadence  string `json:"cadence"`
}

// AddBill catalogues a plan.
func (h *Handlers) AddBill(w http.ResponseWriter, r *http.Request) {
	var req addBillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Provider == "" || req.Cadence == "" {
		respondError(w, http.StatusBadRequest, "provider and cadence are required")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount_minor must be positive")
		return
	}
	b, err := h.svc.AddBill(req.Provider, req.Amount, req.Cadence)
	if err != nil {
		writeHHError(w, err)
		return
	}
	h.logger.Info().Str("bill_id", b.ID).Str("provider", b.Provider).Msg("household bill added")
	respondJSON(w, http.StatusCreated, b)
}

type compareRequest struct {
	BillID string                `json:"bill_id"`
	Market []sharedhh.MarketPlan `json:"market"`
}

// CompareBills detects cheaper-comparable market plans.
func (h *Handlers) CompareBills(w http.ResponseWriter, r *http.Request) {
	var req compareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.BillID == "" {
		respondError(w, http.StatusBadRequest, "bill_id is required")
		return
	}
	out, err := h.svc.CompareBills(req.BillID, req.Market)
	if err != nil {
		writeHHError(w, err)
		return
	}
	if out == nil {
		out = make([]sharedhh.MarketPlan, 0)
	}
	respondJSON(w, http.StatusOK, out)
}

type billActionRequest struct {
	Action string `json:"action"`
}

// BillAction records cancel/switch/renew/ignore.
func (h *Handlers) BillAction(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid bill ID")
		return
	}
	var req billActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Action == "" {
		respondError(w, http.StatusBadRequest, "action is required")
		return
	}
	b, err := h.svc.BillAction(id, req.Action)
	if err != nil {
		writeHHError(w, err)
		return
	}
	h.logger.Info().Str("bill_id", id).Str("action", req.Action).Msg("household bill action")
	respondJSON(w, http.StatusOK, b)
}

type addMandateRequest struct {
	Merchant  string `json:"merchant"`
	Reference string `json:"reference"`
	Amount    int64  `json:"amount_minor"`
}

// AddMandate records one detected direct debit.
func (h *Handlers) AddMandate(w http.ResponseWriter, r *http.Request) {
	var req addMandateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Merchant == "" || req.Reference == "" {
		respondError(w, http.StatusBadRequest, "merchant and reference are required")
		return
	}
	m, err := h.svc.AddMandate(req.Merchant, req.Reference, req.Amount)
	if err != nil {
		writeHHError(w, err)
		return
	}
	h.logger.Info().Str("mandate_id", m.ID).Str("merchant", m.Merchant).Msg("household mandate detected")
	respondJSON(w, http.StatusCreated, m)
}

type migrateRequest struct {
	TargetAccount string `json:"target_account"`
}

// MigrateMandate starts a per-mandate migration task.
func (h *Handlers) MigrateMandate(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid mandate ID")
		return
	}
	var req migrateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.TargetAccount == "" {
		respondError(w, http.StatusBadRequest, "target_account is required")
		return
	}
	m, err := h.svc.MigrateMandate(id, req.TargetAccount)
	if err != nil {
		writeHHError(w, err)
		return
	}
	h.logger.Info().Str("mandate_id", id).Str("target", req.TargetAccount).Msg("household mandate migration started")
	respondJSON(w, http.StatusOK, m)
}

// VerifyFirstCollection completes the migration via first-collection proof.
func (h *Handlers) VerifyFirstCollection(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid mandate ID")
		return
	}
	m, err := h.svc.VerifyFirstCollection(id)
	if err != nil {
		writeHHError(w, err)
		return
	}
	h.logger.Info().Str("mandate_id", id).Msg("household mandate first collection verified")
	respondJSON(w, http.StatusOK, m)
}

// ScanClosure runs the pre-close safety scan.
func (h *Handlers) ScanClosure(w http.ResponseWriter, r *http.Request) {
	var req sharedhh.ClosureInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.ScanClosure(req))
}
