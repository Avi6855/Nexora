package moneymove

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/moneymove"
)

// Handlers serves the /v1/money-move API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds money-move handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all money-move routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/money-move/intents", h.RecordIntent).Methods("POST")
	router.HandleFunc("/v1/money-move/intents/{id}/execute", h.ExecuteIntent).Methods("POST")
	router.HandleFunc("/v1/money-move/intents/{id}/divergence", h.Divergence).Methods("GET")
	router.HandleFunc("/v1/money-move/instructions", h.CreateInstruction).Methods("POST")
	router.HandleFunc("/v1/money-move/instructions/{id}", h.UpdateInstruction).Methods("PUT")
	router.HandleFunc("/v1/money-move/instructions/{id}/history", h.InstructionHistory).Methods("GET")
	router.HandleFunc("/v1/money-move/preconditions/evaluate", h.EvaluatePreconditions).Methods("POST")
	router.HandleFunc("/v1/money-move/reservations", h.Reserve).Methods("POST")
	router.HandleFunc("/v1/money-move/reservations/{id}/release", h.ReleaseReservation).Methods("POST")
	router.HandleFunc("/v1/money-move/reservations/{id}/consume", h.ConsumeReservation).Methods("POST")
	router.HandleFunc("/v1/money-move/leases/acquire", h.AcquireLease).Methods("POST")
	router.HandleFunc("/v1/money-move/leases/{id}/renew", h.RenewLease).Methods("POST")
	router.HandleFunc("/v1/money-move/leases/{id}/execute", h.ExecuteUnderLease).Methods("POST")
	router.HandleFunc("/v1/money-move/overdraft/decide", h.DecideOverdraft).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type recordIntentRequest struct {
	CustomerStatement string   `json:"customer_statement"`
	Amount            int64    `json:"amount"`
	Payee             string   `json:"payee"`
	Steps             []string `json:"steps"`
}

// RecordIntent stores an intent with its execution plan.
func (h *Handlers) RecordIntent(w http.ResponseWriter, r *http.Request) {
	var req recordIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rec, err := h.svc.RecordIntent(req.CustomerStatement, req.Amount, req.Payee, req.Steps)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, rec)
}

type executeIntentRequest struct {
	Legs []string `json:"legs"`
}

// ExecuteIntent records actual legs, flagging divergence.
func (h *Handlers) ExecuteIntent(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "intent id is required")
		return
	}
	var req executeIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rec, err := h.svc.ExecuteIntent(id, req.Legs)
	if err != nil {
		if errors.Is(err, shared.ErrIntentNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rec)
}

// Divergence reports the DIVERGED flag and reason.
func (h *Handlers) Divergence(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "intent id is required")
		return
	}
	diverged, reason, err := h.svc.Divergence(id)
	if err != nil {
		if errors.Is(err, shared.ErrIntentNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"intent_id": id, "diverged": diverged, "reason": reason})
}

type instructionRequest struct {
	Amount int64  `json:"amount"`
	Payee  string `json:"payee"`
}

// CreateInstruction stores a new v1 instruction.
func (h *Handlers) CreateInstruction(w http.ResponseWriter, r *http.Request) {
	var req instructionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ins, err := h.svc.CreateInstruction(req.Amount, req.Payee)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, ins)
}

type updateInstructionRequest struct {
	ExpectedVersion int    `json:"expected_version"`
	Amount          int64  `json:"amount"`
	Payee           string `json:"payee"`
}

// UpdateInstruction applies an optimistic-concurrency mutation.
func (h *Handlers) UpdateInstruction(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "instruction id is required")
		return
	}
	var req updateInstructionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ins, err := h.svc.UpdateInstruction(id, req.ExpectedVersion, req.Amount, req.Payee)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrInstructionNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrVersionConflict):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, ins)
}

// InstructionHistory returns v1..vN snapshots.
func (h *Handlers) InstructionHistory(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "instruction id is required")
		return
	}
	history, err := h.svc.History(id)
	if err != nil {
		if errors.Is(err, shared.ErrInstructionNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if history == nil {
		history = make([]shared.Instruction, 0)
	}
	respondJSON(w, http.StatusOK, history)
}

type evaluateRequest struct {
	Payment shared.PreconditionPayment `json:"payment"`
	State   shared.PreconditionState   `json:"state"`
	Now     string                     `json:"now,omitempty"`
}

// EvaluatePreconditions checks P(payment,state,time). It never executes.
func (h *Handlers) EvaluatePreconditions(w http.ResponseWriter, r *http.Request) {
	var req evaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now := time.Now().UTC()
	if req.Now != "" {
		t, err := time.Parse(time.RFC3339, req.Now)
		if err != nil {
			respondError(w, http.StatusBadRequest, "now must be RFC3339")
			return
		}
		now = t
	}
	respondJSON(w, http.StatusOK, h.svc.Evaluate(req.Payment, req.State, now))
}

type reserveRequest struct {
	Account string `json:"account"`
	Amount  int64  `json:"amount"`
}

// Reserve atomically holds funds.
func (h *Handlers) Reserve(w http.ResponseWriter, r *http.Request) {
	var req reserveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.svc.Reserve(req.Account, req.Amount)
	if err != nil {
		if errors.Is(err, shared.ErrInsufficientFunds) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, res)
}

// ReleaseReservation frees a hold.
func (h *Handlers) ReleaseReservation(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "reservation id is required")
		return
	}
	res, err := h.svc.Release(id)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrReservationNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrReservationNotActive):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// ConsumeReservation settles a hold.
func (h *Handlers) ConsumeReservation(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "reservation id is required")
		return
	}
	res, err := h.svc.Consume(id)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrReservationNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrReservationNotActive),
			errors.Is(err, shared.ErrInsufficientFunds):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, res)
}

type acquireLeaseRequest struct {
	Resource   string `json:"resource"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// AcquireLease takes a fencing lease.
func (h *Handlers) AcquireLease(w http.ResponseWriter, r *http.Request) {
	var req acquireLeaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Resource == "" {
		respondError(w, http.StatusBadRequest, "resource is required")
		return
	}
	if req.TTLSeconds <= 0 {
		respondError(w, http.StatusBadRequest, "ttl_seconds must be positive")
		return
	}
	lease, err := h.svc.Acquire(req.Resource, time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		if errors.Is(err, shared.ErrLeaseHeld) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, lease)
}

type leaseTokenRequest struct {
	Token      uint64 `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds,omitempty"`
}

// RenewLease extends a lease for the current holder.
func (h *Handlers) RenewLease(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "lease id is required")
		return
	}
	var req leaseTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	lease, err := h.svc.Renew(id, req.Token, ttl)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrLeaseNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrStaleToken),
			errors.Is(err, shared.ErrLeaseExpired):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, lease)
}

// ExecuteUnderLease validates the fencing token before executing.
func (h *Handlers) ExecuteUnderLease(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "lease id is required")
		return
	}
	var req leaseTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	lease, err := h.svc.ExecuteUnderLease(id, req.Token)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrLeaseNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrStaleToken),
			errors.Is(err, shared.ErrLeaseExpired):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"executed": true, "lease": lease})
}

type overdraftRequest struct {
	Available int64          `json:"available"`
	Claims    []shared.Claim `json:"claims"`
}

// DecideOverdraft allocates liquidity across competing claims.
func (h *Handlers) DecideOverdraft(w http.ResponseWriter, r *http.Request) {
	var req overdraftRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Claims == nil {
		respondError(w, http.StatusBadRequest, "claims are required")
		return
	}
	decisions, err := h.svc.Decide(req.Available, req.Claims)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, decisions)
}
