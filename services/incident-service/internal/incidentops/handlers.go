package incidentops

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/incidentops"
)

// Handlers exposes the incident-ops API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/incident-ops.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/incident-ops/signals", h.IngestSignal).Methods("POST")
	router.HandleFunc("/v1/incident-ops/correlate", h.Correlate).Methods("POST")
	router.HandleFunc("/v1/incident-ops/triage", h.Triage).Methods("POST")
	router.HandleFunc("/v1/incident-ops/impact/calculate", h.CalculateImpact).Methods("POST")
	router.HandleFunc("/v1/incident-ops/comp-policies", h.AddPolicy).Methods("POST")
	router.HandleFunc("/v1/incident-ops/comp-evaluate", h.EvaluateComp).Methods("POST")
	router.HandleFunc("/v1/incident-ops/comp-awards", h.AwardComp).Methods("POST")
	router.HandleFunc("/v1/incident-ops/comp-awards/{id}/override", h.OverrideAward).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func isUnknown(err error) bool {
	return err == shared.ErrUnknownCandidate || err == shared.ErrUnknownPolicy || err == shared.ErrUnknownAward ||
		(err != nil && strings.Contains(err.Error(), "unknown "))
}

func isConflict(err error) bool {
	return err == shared.ErrDuplicateClaim || err == shared.ErrDuplicatePolicy
}

// ── Triage ──────────────────────────────────────────────────────────────────

type signalRequest struct {
	Service   string `json:"service"`
	Kind      string `json:"kind"`
	Message   string `json:"message,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	At        string `json:"at,omitempty"`
	AtSeconds *int64 `json:"at_seconds,omitempty"`
}

func (h *Handlers) IngestSignal(w http.ResponseWriter, r *http.Request) {
	var req signalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	at := time.Now().UTC()
	if req.At != "" {
		t, err := time.Parse(time.RFC3339, req.At)
		if err != nil {
			respondError(w, http.StatusBadRequest, "at must be RFC3339")
			return
		}
		at = t
	} else if req.AtSeconds != nil {
		at = time.Unix(*req.AtSeconds, 0).UTC()
	}
	sig, err := h.svc.Ingest(req.Service, req.Kind, req.Message, req.IsError, at)
	if err != nil {
		h.logger.Warn().Err(err).Str("service", req.Service).Msg("signal ingest failed")
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("signal_id", sig.ID).Str("service", req.Service).Msg("signal ingested")
	respondJSON(w, http.StatusCreated, sig)
}

type correlateRequest struct {
	Window        string  `json:"window,omitempty"`
	WindowSeconds float64 `json:"window_seconds,omitempty"`
	WindowMinutes float64 `json:"window_minutes,omitempty"`
}

func parseWindow(req correlateRequest) (time.Duration, error) {
	if req.Window != "" {
		return time.ParseDuration(req.Window)
	}
	if req.WindowSeconds > 0 {
		return time.Duration(req.WindowSeconds * float64(time.Second)), nil
	}
	if req.WindowMinutes > 0 {
		return time.Duration(req.WindowMinutes * float64(time.Minute)), nil
	}
	return 15 * time.Minute, nil
}

func (h *Handlers) Correlate(w http.ResponseWriter, r *http.Request) {
	var req correlateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	window, err := parseWindow(req)
	if err != nil || window <= 0 {
		respondError(w, http.StatusBadRequest, "window must be a valid positive duration")
		return
	}
	cands, err := h.svc.Correlate(window, time.Now().UTC())
	if err != nil {
		h.logger.Warn().Err(err).Msg("correlate failed")
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cands == nil {
		cands = []shared.Candidate{}
	}
	h.logger.Info().Int("candidates", len(cands)).Msg("signals correlated")
	respondJSON(w, http.StatusOK, cands)
}

func (h *Handlers) Triage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CandidateID string `json:"candidate_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.CandidateID) == "" {
		respondError(w, http.StatusBadRequest, "candidate_id is required")
		return
	}
	res, err := h.svc.Triage(req.CandidateID)
	if err != nil {
		h.logger.Warn().Err(err).Str("candidate_id", req.CandidateID).Msg("triage failed")
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("candidate_id", req.CandidateID).Str("severity", res.Severity).Msg("candidate triaged")
	respondJSON(w, http.StatusOK, res)
}

// ── Impact ──────────────────────────────────────────────────────────────────

type impactRequest struct {
	Services []string `json:"services"`
	Ledger   []struct {
		Service      string `json:"service"`
		Customers    int    `json:"customers"`
		Transactions int    `json:"transactions"`
		AmountMinor  int64  `json:"amount_minor"`
	} `json:"ledger"`
}

func (h *Handlers) CalculateImpact(w http.ResponseWriter, r *http.Request) {
	var req impactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	snap := make([]shared.ServiceLedger, 0, len(req.Ledger))
	for _, row := range req.Ledger {
		snap = append(snap, shared.ServiceLedger{
			Service: row.Service, Customers: row.Customers,
			Transactions: row.Transactions, AmountMinor: row.AmountMinor,
		})
	}
	imp, err := h.svc.Calculate(req.Services, snap)
	if err != nil {
		h.logger.Warn().Err(err).Msg("impact calculate failed")
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Int("services", len(req.Services)).Int("customers", imp.Customers).Msg("impact calculated")
	respondJSON(w, http.StatusOK, imp)
}

// ── Compensation ────────────────────────────────────────────────────────────

type policyRequest struct {
	ID               string `json:"id"`
	MinOutageMinutes int    `json:"min_outage_minutes"`
	Eligibility      string `json:"eligibility"`
	AmountMinor      int64  `json:"amount_minor"`
}

func (h *Handlers) AddPolicy(w http.ResponseWriter, r *http.Request) {
	var req policyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.AddPolicy(req.ID, req.MinOutageMinutes, req.Eligibility, req.AmountMinor); err != nil {
		h.logger.Warn().Err(err).Str("policy_id", req.ID).Msg("comp policy add failed")
		if isConflict(err) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("policy_id", req.ID).Msg("comp policy added")
	respondJSON(w, http.StatusCreated, req)
}

type evaluateRequest struct {
	Account       string `json:"account"`
	OutageMinutes int    `json:"outage_minutes"`
	Tier          string `json:"tier"`
}

func (h *Handlers) EvaluateComp(w http.ResponseWriter, r *http.Request) {
	var req evaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ok, amount, pid := h.svc.Evaluate(req.Account, req.OutageMinutes, req.Tier)
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"eligible": ok, "amount_minor": amount, "policy_id": pid,
	})
}

type awardRequest struct {
	ClaimKey      string `json:"claim_key"`
	Account       string `json:"account"`
	OutageMinutes int    `json:"outage_minutes"`
	Tier          string `json:"tier"`
}

func (h *Handlers) AwardComp(w http.ResponseWriter, r *http.Request) {
	var req awardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := h.svc.Award(req.ClaimKey, req.Account, req.OutageMinutes, req.Tier, time.Now().UTC())
	if err != nil {
		h.logger.Warn().Err(err).Str("claim_key", req.ClaimKey).Msg("comp award failed")
		if isConflict(err) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("award_id", a.ID).Str("account", req.Account).Msg("comp award granted")
	respondJSON(w, http.StatusCreated, a)
}

func (h *Handlers) OverrideAward(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		Approver    string `json:"approver"`
		AmountMinor *int64 `json:"amount_minor,omitempty"`
		Status      string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := h.svc.Override(id, req.Approver, req.AmountMinor, req.Status, time.Now().UTC())
	if err != nil {
		h.logger.Warn().Err(err).Str("award_id", id).Msg("comp override failed")
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("award_id", id).Msg("comp award overridden")
	respondJSON(w, http.StatusOK, a)
}
