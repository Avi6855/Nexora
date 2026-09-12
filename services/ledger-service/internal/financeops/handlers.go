package financeops

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/financeops"
)

// Handlers serves the /v1/finance-ops API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds finance-ops handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all finance-ops routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/finance-ops/adjustments", h.CreateAdjustment).Methods("POST")
	router.HandleFunc("/v1/finance-ops/adjustments/{id}/approve", h.ApproveAdjustment).Methods("POST")
	router.HandleFunc("/v1/finance-ops/adjustments/{id}", h.GetAdjustment).Methods("GET")
	router.HandleFunc("/v1/finance-ops/backdated-events", h.PostBackdatedEvent).Methods("POST")
	router.HandleFunc("/v1/finance-ops/balances/as-of", h.BalanceAsOf).Methods("GET")
	router.HandleFunc("/v1/finance-ops/close/check", h.CheckClose).Methods("POST")
	router.HandleFunc("/v1/finance-ops/periods/lock", h.LockPeriod).Methods("POST")
	router.HandleFunc("/v1/finance-ops/periods/corrections", h.PostCorrection).Methods("POST")
	router.HandleFunc("/v1/finance-ops/posting-rules", h.PutRule).Methods("PUT")
	router.HandleFunc("/v1/finance-ops/posting-rules/{type}/rollback", h.RollbackRule).Methods("POST")
	router.HandleFunc("/v1/finance-ops/posting-rules/resolve", h.ResolveRule).Methods("GET")
	router.HandleFunc("/v1/finance-ops/subledgers/post", h.PostSubledger).Methods("POST")
	router.HandleFunc("/v1/finance-ops/subledgers/trial", h.TrialBalance).Methods("GET")
	router.HandleFunc("/v1/finance-ops/consolidated", h.Consolidated).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type adjustmentRequest struct {
	OriginalID    string `json:"original_id,omitempty"`
	DebitAccount  string `json:"debit_account,omitempty"`
	CreditAccount string `json:"credit_account,omitempty"`
	Amount        int64  `json:"amount,omitempty"`
	Period        string `json:"period,omitempty"`
	Reason        string `json:"reason"`
}

// CreateAdjustment opens an adjustment. When original_id is omitted, an
// original entry is created first from debit/credit/amount/period.
func (h *Handlers) CreateAdjustment(w http.ResponseWriter, r *http.Request) {
	var req adjustmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Reason == "" {
		respondError(w, http.StatusBadRequest, "reason is required")
		return
	}
	originalID := req.OriginalID
	if originalID == "" {
		if req.DebitAccount == "" || req.CreditAccount == "" || req.Amount <= 0 || req.Period == "" {
			respondError(w, http.StatusBadRequest, "original_id or debit_account/credit_account/amount/period is required")
			return
		}
		orig, err := h.svc.CreateEntry(req.DebitAccount, req.CreditAccount, req.Amount, req.Period)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		originalID = orig.ID
	}
	adj, err := h.svc.RequestAdjustment(originalID, req.Reason)
	if err != nil {
		if errors.Is(err, shared.ErrEntryNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	orig, _ := h.svc.GetEntry(originalID)
	respondJSON(w, http.StatusCreated, map[string]interface{}{"adjustment": adj, "original": orig})
}

// ApproveAdjustment posts the compensating entry and verifies balance.
func (h *Handlers) ApproveAdjustment(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "adjustment id is required")
		return
	}
	adj, comp, err := h.svc.ApproveAdjustment(id)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrAdjustmentNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrAdjustmentState):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	balanced, msg, _ := h.svc.VerifyBalanced(id)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"adjustment": adj, "compensating": comp, "balanced": balanced, "verify": msg,
	})
}

// GetAdjustment returns an adjustment with its entries and verification.
func (h *Handlers) GetAdjustment(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "adjustment id is required")
		return
	}
	adj, err := h.svc.GetAdjustment(id)
	if err != nil {
		if errors.Is(err, shared.ErrAdjustmentNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	orig, _ := h.svc.GetEntry(adj.OriginalID)
	var comp interface{}
	if adj.CompensatingID != "" {
		if e, err := h.svc.GetEntry(adj.CompensatingID); err == nil {
			comp = e
		}
	}
	balanced, msg, _ := h.svc.VerifyBalanced(id)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"adjustment": adj, "original": orig, "compensating": comp, "balanced": balanced, "verify": msg,
	})
}

type backdatedRequest struct {
	Account     string `json:"account"`
	Amount      int64  `json:"amount"`
	EffectiveAt string `json:"effective_at"`
}

// PostBackdatedEvent records an event with a past effective date.
func (h *Handlers) PostBackdatedEvent(w http.ResponseWriter, r *http.Request) {
	var req backdatedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	effective, err := time.Parse(time.RFC3339, req.EffectiveAt)
	if err != nil {
		respondError(w, http.StatusBadRequest, "effective_at must be RFC3339")
		return
	}
	ev, err := h.svc.PostBackdatedEvent(req.Account, req.Amount, effective)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, ev)
}

// BalanceAsOf recomputes the derived balance as of ?account=&as_of=RFC3339.
func (h *Handlers) BalanceAsOf(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	asOfRaw := r.URL.Query().Get("as_of")
	if account == "" || asOfRaw == "" {
		respondError(w, http.StatusBadRequest, "account and as_of query parameters are required")
		return
	}
	asOf, err := time.Parse(time.RFC3339, asOfRaw)
	if err != nil {
		respondError(w, http.StatusBadRequest, "as_of must be RFC3339")
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"account": account, "as_of": asOfRaw, "balance": h.svc.BalanceAsOf(account, asOf),
	})
}

// CheckClose evaluates the close checklist.
func (h *Handlers) CheckClose(w http.ResponseWriter, r *http.Request) {
	var req shared.Checklist
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.CheckClose(req))
}

type lockRequest struct {
	Period string `json:"period"`
}

// LockPeriod closes a period.
func (h *Handlers) LockPeriod(w http.ResponseWriter, r *http.Request) {
	var req lockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Period == "" {
		respondError(w, http.StatusBadRequest, "period is required")
		return
	}
	if err := h.svc.Lock(req.Period); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"period": req.Period, "locked": true})
}

type correctionRequest struct {
	Period        string `json:"period"`
	DebitAccount  string `json:"debit_account"`
	CreditAccount string `json:"credit_account"`
	Amount        int64  `json:"amount"`
}

// PostCorrection posts a correction, redirecting out of locked periods.
func (h *Handlers) PostCorrection(w http.ResponseWriter, r *http.Request) {
	var req correctionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.svc.PostCorrection(req.Period, req.DebitAccount, req.CreditAccount, req.Amount)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

type putRuleRequest struct {
	Type          string `json:"type"`
	DebitAccount  string `json:"debit_account"`
	CreditAccount string `json:"credit_account"`
}

// PutRule stores a new posting-rule version.
func (h *Handlers) PutRule(w http.ResponseWriter, r *http.Request) {
	var req putRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule, err := h.svc.PutRule(req.Type, req.DebitAccount, req.CreditAccount)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rule)
}

// RollbackRule drops the latest rule version.
func (h *Handlers) RollbackRule(w http.ResponseWriter, r *http.Request) {
	txType := mux.Vars(r)["type"]
	if txType == "" {
		respondError(w, http.StatusBadRequest, "type is required")
		return
	}
	rule, err := h.svc.Rollback(txType)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrRuleNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrNoPriorVersion):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, rule)
}

// ResolveRule resolves type+version (?type=&version=, version 0 = latest).
func (h *Handlers) ResolveRule(w http.ResponseWriter, r *http.Request) {
	txType := r.URL.Query().Get("type")
	if txType == "" {
		respondError(w, http.StatusBadRequest, "type query parameter is required")
		return
	}
	version := 0
	if raw := r.URL.Query().Get("version"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			respondError(w, http.StatusBadRequest, "version must be an integer")
			return
		}
		version = v
	}
	rule, err := h.svc.Resolve(txType, version)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrRuleNotFound),
			errors.Is(err, shared.ErrRuleVersionNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, rule)
}

type subledgerPostRequest struct {
	Domain        string `json:"domain"`
	DebitAccount  string `json:"debit_account"`
	CreditAccount string `json:"credit_account"`
	Amount        int64  `json:"amount"`
}

// PostSubledger appends to an isolated domain ledger.
func (h *Handlers) PostSubledger(w http.ResponseWriter, r *http.Request) {
	var req subledgerPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	e, err := h.svc.PostSubledger(req.Domain, req.DebitAccount, req.CreditAccount, req.Amount)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, e)
}

// TrialBalance returns one domain trial (?domain=).
func (h *Handlers) TrialBalance(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	if domain == "" {
		respondError(w, http.StatusBadRequest, "domain query parameter is required")
		return
	}
	tb, err := h.svc.TrialBalance(domain)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, tb)
}

// Consolidated folds every domain into the general view.
func (h *Handlers) Consolidated(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.Consolidated())
}
