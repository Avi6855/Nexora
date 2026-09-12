package txpolicy

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	sharedtx "github.com/nexora/nexora/shared/txpolicy"
)

// Handlers serves the /v1/tx-policy API: versioned rules, evaluation,
// shadow simulation and the audit log.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers creates transaction-policy handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the transaction-policy routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/tx-policy/rules", h.PutRule).Methods("PUT")
	router.HandleFunc("/v1/tx-policy/rules/{id}/rollback", h.Rollback).Methods("POST")
	router.HandleFunc("/v1/tx-policy/evaluate", h.Evaluate).Methods("POST")
	router.HandleFunc("/v1/tx-policy/simulate", h.Simulate).Methods("POST")
	router.HandleFunc("/v1/tx-policy/audit", h.Audit).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// ruleStatus maps rule failures: 404 unknown ids, 409 with no prior version
// to roll back to, 400 invalid input.
func ruleStatus(err error) int {
	if errors.Is(err, sharedtx.ErrRuleNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, sharedtx.ErrNoPriorVersion) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

type predicateRequest struct {
	MinAmountMinor     *int64 `json:"min_amount_minor,omitempty"`
	BeneficiaryNew     *bool  `json:"beneficiary_new,omitempty"`
	MinDestinationRisk *int   `json:"min_destination_risk,omitempty"`
	Category           string `json:"category,omitempty"`
	Country            string `json:"country,omitempty"`
}

func (p predicateRequest) toShared() sharedtx.Predicate {
	return sharedtx.Predicate{
		MinAmountMinor:     p.MinAmountMinor,
		BeneficiaryNew:     p.BeneficiaryNew,
		MinDestinationRisk: p.MinDestinationRisk,
		Category:           p.Category,
		Country:            p.Country,
	}
}

type putRuleRequest struct {
	ID        string           `json:"id"`
	Priority  int              `json:"priority"`
	Predicate predicateRequest `json:"predicate"`
	Effect    string           `json:"effect"`
}

// PutRule stores a rule, bumping its version.
func (h *Handlers) PutRule(w http.ResponseWriter, r *http.Request) {
	var req putRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" {
		respondError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Effect == "" {
		respondError(w, http.StatusBadRequest, "effect is required")
		return
	}
	rule, err := h.svc.PutRule(sharedtx.Rule{
		ID:        req.ID,
		Priority:  req.Priority,
		Predicate: req.Predicate.toShared(),
		Effect:    sharedtx.Effect(req.Effect),
	})
	if err != nil {
		respondError(w, ruleStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rule)
}

// Rollback drops the latest version of a rule, restoring the prior one.
func (h *Handlers) Rollback(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "rule id is required")
		return
	}
	rule, err := h.svc.Rollback(id)
	if err != nil {
		respondError(w, ruleStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rule)
}

type contextRequest struct {
	AmountMinor     int64  `json:"amount_minor"`
	BeneficiaryNew  bool   `json:"beneficiary_new,omitempty"`
	DestinationRisk int    `json:"destination_risk,omitempty"`
	Category        string `json:"category,omitempty"`
	Country         string `json:"country,omitempty"`
}

func (c contextRequest) toShared() sharedtx.Context {
	return sharedtx.Context{
		AmountMinor:     c.AmountMinor,
		BeneficiaryNew:  c.BeneficiaryNew,
		DestinationRisk: c.DestinationRisk,
		Category:        c.Category,
		Country:         c.Country,
	}
}

// Evaluate matches a context against the current policy.
func (h *Handlers) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req contextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.Evaluate(req.toShared()))
}

type simulateRequest struct {
	CandidateRules []putRuleRequest `json:"candidate_rules"`
	Contexts       []contextRequest `json:"contexts"`
}

// Simulate runs a candidate rule set alongside the current policy.
func (h *Handlers) Simulate(w http.ResponseWriter, r *http.Request) {
	var req simulateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	candidate := make([]sharedtx.Rule, 0, len(req.CandidateRules))
	for _, cr := range req.CandidateRules {
		if cr.ID == "" {
			respondError(w, http.StatusBadRequest, "candidate rule id is required")
			return
		}
		candidate = append(candidate, sharedtx.Rule{
			ID:        cr.ID,
			Priority:  cr.Priority,
			Predicate: cr.Predicate.toShared(),
			Effect:    sharedtx.Effect(cr.Effect),
		})
	}
	contexts := make([]sharedtx.Context, 0, len(req.Contexts))
	for _, c := range req.Contexts {
		contexts = append(contexts, c.toShared())
	}
	res, err := h.svc.Simulate(candidate, contexts)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// Audit returns the evaluation log, oldest first.
func (h *Handlers) Audit(w http.ResponseWriter, r *http.Request) {
	entries := h.svc.Audit()
	if entries == nil {
		entries = []sharedtx.AuditEntry{}
	}
	respondJSON(w, http.StatusOK, entries)
}
