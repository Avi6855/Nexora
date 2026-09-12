package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/dispute-service/internal/domain"
	"github.com/nexora/nexora/services/dispute-service/internal/service"
)

// OrchestrationHandlers exposes orchestrated disputes (TRANSFER /
// DIRECT_DEBIT / CASH alongside CARD) under /v1/disputes/orchestrated.
type OrchestrationHandlers struct {
	orchestrated *service.OrchestrationService
	logger       zerolog.Logger
}

// NewOrchestrationHandlers builds the handlers.
func NewOrchestrationHandlers(svc *service.OrchestrationService, logger zerolog.Logger) *OrchestrationHandlers {
	return &OrchestrationHandlers{orchestrated: svc, logger: logger}
}

// RegisterRoutes mounts orchestrated dispute routes.
func (h *OrchestrationHandlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/disputes/orchestrated", h.Create).Methods("POST")
	router.HandleFunc("/v1/disputes/orchestrated/{id}/evidence", h.AddEvidence).Methods("POST")
	router.HandleFunc("/v1/disputes/orchestrated/{id}/advance", h.Advance).Methods("POST")
	router.HandleFunc("/v1/disputes/orchestrated/{id}/adjustment", h.Adjustment).Methods("GET")
}

func writeOrchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrOrchestratedNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrOrchestratedInvalidKind),
		errors.Is(err, domain.ErrOrchestratedIneligible),
		errors.Is(err, domain.ErrOrchestratedEvidence),
		errors.Is(err, domain.ErrOrchestratedMissingEvidence),
		errors.Is(err, domain.ErrOrchestratedInvalidStatus):
		respondError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrOrchestratedNoAdjustment):
		respondError(w, http.StatusNotFound, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

type createOrchestratedRequest struct {
	Kind      string `json:"kind"`
	UserID    string `json:"user_id,omitempty"`
	AccountID string `json:"account_id"`
	Amount    int64  `json:"amount_minor"`
	Currency  string `json:"currency"`
	TxnAt     string `json:"txn_at,omitempty"`
}

// Create opens an orchestrated dispute with a kind.
func (h *OrchestrationHandlers) Create(w http.ResponseWriter, r *http.Request) {
	var req createOrchestratedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" || req.AccountID == "" {
		respondError(w, http.StatusBadRequest, "kind and account_id are required")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount_minor must be positive")
		return
	}
	if req.Currency == "" {
		respondError(w, http.StatusBadRequest, "currency is required")
	}
	userID := req.UserID
	if userID == "" {
		if uid, ok := callerID(r); ok {
			userID = uid.String()
		} else {
			userID = uuid.NewString()
		}
	}
	txnAt := time.Now().UTC()
	if req.TxnAt != "" {
		t, err := time.Parse(time.RFC3339, req.TxnAt)
		if err != nil {
			respondError(w, http.StatusBadRequest, "txn_at must be RFC3339")
			return
		}
		txnAt = t
	}
	d, err := h.orchestrated.Create(r.Context(), domain.OrchestratedKind(req.Kind), userID, req.AccountID, req.Amount, req.Currency, txnAt)
	if err != nil {
		writeOrchError(w, err)
		return
	}
	h.logger.Info().Str("dispute_id", d.DisputeID).Str("kind", req.Kind).Msg("orchestrated dispute created via HTTP")
	respondJSON(w, http.StatusCreated, d)
}

type orchEvidenceRequest struct {
	Type     string `json:"evidence_type"`
	Filename string `json:"filename,omitempty"`
}

// AddEvidence attaches evidence to an orchestrated dispute.
func (h *OrchestrationHandlers) AddEvidence(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid dispute ID")
		return
	}
	var req orchEvidenceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Type == "" {
		respondError(w, http.StatusBadRequest, "evidence_type is required")
		return
	}
	d, err := h.orchestrated.AddEvidence(r.Context(), id, req.Type, req.Filename)
	if err != nil {
		writeOrchError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, d)
}

type orchAdvanceRequest struct {
	Action string `json:"action"`
}

// Advance moves the orchestrated dispute (submit, resolve-won,
// resolve-lost, return).
func (h *OrchestrationHandlers) Advance(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid dispute ID")
		return
	}
	var req orchAdvanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Action == "" {
		respondError(w, http.StatusBadRequest, "action is required")
		return
	}
	d, err := h.orchestrated.Advance(r.Context(), id, req.Action)
	if err != nil {
		writeOrchError(w, err)
		return
	}
	h.logger.Info().Str("dispute_id", id).Str("action", req.Action).Msg("orchestrated dispute advanced via HTTP")
	respondJSON(w, http.StatusOK, d)
}

// Adjustment returns the ledger-adjustment proposal (never auto-posted).
func (h *OrchestrationHandlers) Adjustment(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid dispute ID")
		return
	}
	adj, err := h.orchestrated.Adjustment(r.Context(), id)
	if err != nil {
		writeOrchError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, adj)
}
