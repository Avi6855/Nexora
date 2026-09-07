package transport

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/services/control-plane-service/internal/regimpact"
	"github.com/nexora/nexora/services/control-plane-service/internal/regreport"
)

// ── Regulatory Change Impact Analyzer ───────────────────────────────────────

type regImpactRequest struct {
	RuleID        string   `json:"rule_id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Domain        string   `json:"domain"`
	ProductScopes []string `json:"product_scopes,omitempty"`
	EffectiveFrom string   `json:"effective_from"` // RFC3339
}

func (h *Handlers) RegImpact(w http.ResponseWriter, r *http.Request) {
	var req regImpactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RuleID == "" || req.Domain == "" {
		respondError(w, http.StatusBadRequest, "rule_id and domain are required")
		return
	}
	eff, err := time.Parse(time.RFC3339, req.EffectiveFrom)
	if err != nil {
		respondError(w, http.StatusBadRequest, "effective_from must be RFC3339")
		return
	}
	analysis := h.regImpact.Analyze(regimpact.Rule{
		RuleID:        req.RuleID,
		Title:         req.Title,
		Description:   req.Description,
		Domain:        regimpact.Domain(req.Domain),
		ProductScopes: req.ProductScopes,
		EffectiveFrom: eff,
	})
	respondJSON(w, http.StatusOK, analysis)
}

func (h *Handlers) RegRegisterService(w http.ResponseWriter, r *http.Request) {
	var reg regimpact.ServiceRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if reg.Service == "" {
		respondError(w, http.StatusBadRequest, "service is required")
		return
	}
	h.regImpact.Register(&reg)
	respondJSON(w, http.StatusCreated, map[string]string{"status": "registered", "service": reg.Service})
}

// ── Regulatory Reporting Pipeline ───────────────────────────────────────────

func (h *Handlers) RegCreateReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReportID string `json:"report_id"`
		Regime   string `json:"regime"`
		Period   string `json:"period"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ReportID == "" || req.Regime == "" || req.Period == "" {
		respondError(w, http.StatusBadRequest, "report_id, regime and period are required")
		return
	}
	rep, err := h.regReport.CreateReport(req.ReportID, req.Regime, req.Period)
	if err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, rep)
}

func (h *Handlers) RegAddFigure(w http.ResponseWriter, r *http.Request) {
	var p regreport.Provenance
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.regReport.AddFigure(mux.Vars(r)["id"], p); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"status": "figure added", "chain_hash": p.ChainHash})
}

func (h *Handlers) RegValidateReport(w http.ResponseWriter, r *http.Request) {
	// Standard gates, in documented order.
	gates := []regreport.ValidationGate{
		regreport.ReconciliationGate{MaxLossRatio: 0.01},
	}
	var req struct {
		Required       []string         `json:"required_figures,omitempty"`
		Prior          map[string]int64 `json:"prior_figures,omitempty"`
		MaxVarianceBps int64            `json:"max_variance_bps,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // optional body
	}
	if len(req.Required) > 0 {
		gates = append(gates, regreport.CompletenessGate{Required: req.Required})
	}
	if len(req.Prior) > 0 {
		gates = append(gates, regreport.VarianceGate{Prior: req.Prior, MaxPctBps: req.MaxVarianceBps})
	}
	if err := h.regReport.Validate(mux.Vars(r)["id"], gates); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, regreport.ErrValidationFailed) {
			status = http.StatusUnprocessableEntity
		}
		respondError(w, status, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "VALIDATED"})
}

func (h *Handlers) RegSubmitReport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SubmissionRef string `json:"submission_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SubmissionRef == "" {
		respondError(w, http.StatusBadRequest, "submission_ref is required")
		return
	}
	if err := h.regReport.Submit(mux.Vars(r)["id"], req.SubmissionRef); err != nil {
		respondError(w, http.StatusConflict, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "SUBMITTED", "submission_ref": req.SubmissionRef})
}

func (h *Handlers) RegGetReport(w http.ResponseWriter, r *http.Request) {
	rep, err := h.regReport.Get(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rep)
}

func (h *Handlers) RegEvidence(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	p, err := h.regReport.EvidenceFor(vars["id"], vars["key"])
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}
