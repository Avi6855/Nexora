package decisionops

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/decisionops"
)

// Handlers exposes the decision-ops API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/decision-ops.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/decision-ops/explain", h.Explain).Methods("POST")
	router.HandleFunc("/v1/decision-ops/cases", h.EnqueueCase).Methods("POST")
	router.HandleFunc("/v1/decision-ops/cases/{id}/lease", h.LeaseCase).Methods("POST")
	router.HandleFunc("/v1/decision-ops/cases/{id}/decide", h.DecideCase).Methods("POST")
	router.HandleFunc("/v1/decision-ops/cases/{id}/escalate", h.EscalateCase).Methods("POST")
	router.HandleFunc("/v1/decision-ops/cases/{id}/reassign", h.ReassignCase).Methods("POST")
	router.HandleFunc("/v1/decision-ops/sweep", h.Sweep).Methods("POST")
	router.HandleFunc("/v1/decision-ops/cases/{id}/audit", h.CaseAudit).Methods("GET")
	router.HandleFunc("/v1/decision-ops/model-records", h.RecordModel).Methods("POST")
	router.HandleFunc("/v1/decision-ops/model-records/{id}/verify", h.VerifyModel).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func mapQueueError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	switch err {
	case shared.ErrCaseNotFound:
		return http.StatusNotFound
	case shared.ErrLeaseConflict, shared.ErrCaseDecided:
		return http.StatusConflict
	default:
		if strings.Contains(err.Error(), "unknown decision") {
			return http.StatusBadRequest
		}
		return http.StatusBadRequest
	}
}

// ── Explain ─────────────────────────────────────────────────────────────────

type explainRequest struct {
	Signals []struct {
		Name   string  `json:"name"`
		Value  float64 `json:"value"`
		Weight float64 `json:"weight"`
	} `json:"signals"`
	PolicyID      string `json:"policy_id"`
	PolicyVersion string `json:"policy_version"`
	Outcome       string `json:"outcome"`
}

func (h *Handlers) Explain(w http.ResponseWriter, r *http.Request) {
	var req explainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d := shared.Decision{PolicyID: req.PolicyID, PolicyVersion: req.PolicyVersion, Outcome: req.Outcome}
	for _, s := range req.Signals {
		d.Signals = append(d.Signals, shared.Signal{Name: s.Name, Value: s.Value, Weight: s.Weight})
	}
	exp, err := h.svc.Explain(d)
	if err != nil {
		h.logger.Warn().Err(err).Str("policy_id", req.PolicyID).Msg("decision explain failed")
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("policy_id", req.PolicyID).Str("outcome", req.Outcome).Msg("decision explained")
	respondJSON(w, http.StatusOK, exp)
}

// ── Cases ───────────────────────────────────────────────────────────────────

type enqueueRequest struct {
	Subject    string  `json:"subject"`
	RiskScore  float64 `json:"risk_score"`
	SLASeconds float64 `json:"sla_seconds,omitempty"`
	SLAMinutes float64 `json:"sla_minutes,omitempty"`
	SLA        string  `json:"sla,omitempty"`
}

func (h *Handlers) EnqueueCase(w http.ResponseWriter, r *http.Request) {
	var req enqueueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sla := time.Hour
	switch {
	case req.SLA != "":
		d, err := time.ParseDuration(req.SLA)
		if err != nil || d <= 0 {
			respondError(w, http.StatusBadRequest, "sla must be a valid positive duration")
			return
		}
		sla = d
	case req.SLASeconds > 0:
		sla = time.Duration(req.SLASeconds * float64(time.Second))
	case req.SLAMinutes > 0:
		sla = time.Duration(req.SLAMinutes * float64(time.Minute))
	}
	c, err := h.svc.Enqueue(req.Subject, req.RiskScore, sla, time.Now().UTC())
	if err != nil {
		h.logger.Warn().Err(err).Str("subject", req.Subject).Msg("review case enqueue failed")
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("case_id", c.ID).Str("subject", req.Subject).Msg("review case enqueued")
	respondJSON(w, http.StatusCreated, c)
}

type leaseRequest struct {
	Analyst    string  `json:"analyst"`
	TTL        string  `json:"ttl,omitempty"`
	TTLSeconds float64 `json:"ttl_seconds,omitempty"`
}

func (h *Handlers) LeaseCase(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req leaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ttl := 10 * time.Minute
	if req.TTL != "" {
		d, err := time.ParseDuration(req.TTL)
		if err != nil || d <= 0 {
			respondError(w, http.StatusBadRequest, "ttl must be a valid positive duration")
			return
		}
		ttl = d
	} else if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds * float64(time.Second))
	}
	if err := h.svc.Lease(id, req.Analyst, ttl, time.Now().UTC()); err != nil {
		h.logger.Warn().Err(err).Str("case_id", id).Str("analyst", req.Analyst).Msg("review case lease failed")
		respondError(w, mapQueueError(err), err.Error())
		return
	}
	h.logger.Info().Str("case_id", id).Str("analyst", req.Analyst).Msg("review case leased")
	c, _ := h.svc.Get(id)
	respondJSON(w, http.StatusOK, c)
}

type decideRequest struct {
	Analyst  string `json:"analyst"`
	Decision string `json:"decision"`
	Note     string `json:"note,omitempty"`
}

func (h *Handlers) DecideCase(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req decideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.Decide(id, req.Analyst, req.Decision, req.Note, time.Now().UTC()); err != nil {
		h.logger.Warn().Err(err).Str("case_id", id).Str("decision", req.Decision).Msg("review case decide failed")
		respondError(w, mapQueueError(err), err.Error())
		return
	}
	h.logger.Info().Str("case_id", id).Str("decision", req.Decision).Msg("review case decided")
	c, _ := h.svc.Get(id)
	respondJSON(w, http.StatusOK, c)
}

type moveRequest struct {
	Actor string `json:"actor,omitempty"`
	To    string `json:"to"`
}

func (h *Handlers) EscalateCase(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.Escalate(id, req.Actor, req.To, time.Now().UTC()); err != nil {
		h.logger.Warn().Err(err).Str("case_id", id).Str("to", req.To).Msg("review case escalate failed")
		respondError(w, mapQueueError(err), err.Error())
		return
	}
	h.logger.Info().Str("case_id", id).Str("to", req.To).Msg("review case escalated")
	c, _ := h.svc.Get(id)
	respondJSON(w, http.StatusOK, c)
}

func (h *Handlers) ReassignCase(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req moveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.Reassign(id, req.Actor, req.To, time.Now().UTC()); err != nil {
		h.logger.Warn().Err(err).Str("case_id", id).Str("to", req.To).Msg("review case reassign failed")
		respondError(w, mapQueueError(err), err.Error())
		return
	}
	h.logger.Info().Str("case_id", id).Str("to", req.To).Msg("review case reassigned")
	c, _ := h.svc.Get(id)
	respondJSON(w, http.StatusOK, c)
}

func (h *Handlers) Sweep(w http.ResponseWriter, r *http.Request) {
	swept := h.svc.SweepTimeouts(time.Now().UTC())
	if swept == nil {
		swept = []string{}
	}
	h.logger.Info().Int("swept", len(swept)).Msg("review queue sweep completed")
	respondJSON(w, http.StatusOK, map[string]interface{}{"swept": swept})
}

func (h *Handlers) CaseAudit(w http.ResponseWriter, r *http.Request) {
	trail, err := h.svc.Audit(mux.Vars(r)["id"])
	if err != nil {
		h.logger.Warn().Err(err).Str("case_id", mux.Vars(r)["id"]).Msg("review case audit failed")
		respondError(w, mapQueueError(err), err.Error())
		return
	}
	if trail == nil {
		trail = []shared.AuditEntry{}
	}
	respondJSON(w, http.StatusOK, trail)
}

// ── Model records ───────────────────────────────────────────────────────────

type recordModelRequest struct {
	ModelVersion    string  `json:"model_version"`
	FeatureSnapshot string  `json:"feature_snapshot"`
	PolicyVersion   string  `json:"policy_version"`
	Decision        string  `json:"decision"`
	Confidence      float64 `json:"confidence"`
	HumanOverride   bool    `json:"human_override,omitempty"`
	OverrideBy      string  `json:"override_by,omitempty"`
}

func (h *Handlers) RecordModel(w http.ResponseWriter, r *http.Request) {
	var req recordModelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rec, err := h.svc.RecordModel(req.ModelVersion, req.FeatureSnapshot, req.PolicyVersion, req.Decision, req.Confidence, req.HumanOverride, req.OverrideBy, time.Now().UTC())
	if err != nil {
		h.logger.Warn().Err(err).Str("model_version", req.ModelVersion).Msg("model record failed")
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("record_id", rec.ID).Str("model_version", req.ModelVersion).Msg("model record stored")
	respondJSON(w, http.StatusCreated, rec)
}

func (h *Handlers) VerifyModel(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	snapshot := r.URL.Query().Get("snapshot")
	var body struct {
		Snapshot string `json:"snapshot"`
	}
	if snapshot == "" && r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
		snapshot = body.Snapshot
	}
	if strings.TrimSpace(snapshot) == "" {
		respondError(w, http.StatusBadRequest, "snapshot is required (query param or body)")
		return
	}
	verdict, err := h.svc.VerifyModel(id, snapshot)
	if err != nil {
		h.logger.Warn().Err(err).Str("record_id", id).Msg("model verify failed")
		respondError(w, mapQueueError(err), err.Error())
		return
	}
	h.logger.Info().Str("record_id", id).Str("verdict", verdict).Msg("model verified")
	rec, _ := h.svc.GetModel(id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"id": id, "verdict": verdict, "record": rec})
}
