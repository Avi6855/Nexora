package openbanking

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/openbanking"
)

// Handlers exposes the open-banking integration API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/open-banking.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/open-banking/failures/classify", h.ClassifyFailure).Methods("POST")
	router.HandleFunc("/v1/open-banking/connections/{id}/retry", h.ConnectionRetry).Methods("GET")
	router.HandleFunc("/v1/open-banking/connections/{id}/attempts", h.RecordAttempt).Methods("POST")
	router.HandleFunc("/v1/open-banking/connections/{id}/exhausted", h.ConnectionExhausted).Methods("GET")
	router.HandleFunc("/v1/open-banking/freshness/sla", h.SetSLA).Methods("PUT")
	router.HandleFunc("/v1/open-banking/freshness", h.GetFreshness).Methods("GET")
	router.HandleFunc("/v1/open-banking/freshness/overall", h.GetOverallFreshness).Methods("GET")
	router.HandleFunc("/v1/open-banking/providers", h.RegisterProvider).Methods("POST")
	router.HandleFunc("/v1/open-banking/providers/{name}/outages", h.DeclareOutage).Methods("POST")
	router.HandleFunc("/v1/open-banking/providers/{name}/auth-failures", h.RecordAuthFailure).Methods("POST")
	router.HandleFunc("/v1/open-banking/providers/{name}/successes", h.RecordSuccess).Methods("POST")
	router.HandleFunc("/v1/open-banking/providers/{name}/unsupported", h.MarkUnsupported).Methods("POST")
	router.HandleFunc("/v1/open-banking/providers/{name}/check", h.CheckCapability).Methods("GET")
	router.HandleFunc("/v1/open-banking/providers/matrix", h.CapabilityMatrix).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func isUnknownErr(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "unknown connection") ||
		strings.Contains(err.Error(), "unknown provider"))
}

// ── Recovery ──────────────────────────────────────────────────────────────

type classifyRequest struct {
	ConnectionID      string  `json:"connection_id"`
	Kind              string  `json:"kind"`
	RetryAfter        string  `json:"retry_after,omitempty"`
	RetryAfterSeconds float64 `json:"retry_after_seconds,omitempty"`
}

func (h *Handlers) ClassifyFailure(w http.ResponseWriter, r *http.Request) {
	var req classifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ConnectionID) == "" || strings.TrimSpace(req.Kind) == "" {
		respondError(w, http.StatusBadRequest, "connection_id and kind are required")
		return
	}
	var retryAfter time.Duration
	if req.RetryAfter != "" {
		d, err := time.ParseDuration(req.RetryAfter)
		if err != nil || d < 0 {
			respondError(w, http.StatusBadRequest, "retry_after must be a valid non-negative duration")
			return
		}
		retryAfter = d
	} else if req.RetryAfterSeconds != 0 {
		if req.RetryAfterSeconds < 0 {
			respondError(w, http.StatusBadRequest, "retry_after_seconds must not be negative")
			return
		}
		retryAfter = time.Duration(req.RetryAfterSeconds * float64(time.Second))
	}
	plan, err := h.svc.ReportFailure(req.ConnectionID, shared.FailureKind(req.Kind), retryAfter)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"connection_id": req.ConnectionID,
		"kind":          req.Kind,
		"plan": map[string]interface{}{
			"lane":            string(plan.Kind),
			"auto_retry":      plan.AutoRetry,
			"max_attempts":    plan.MaxAttempts,
			"backoff_seconds": plan.BackoffBase.Seconds(),
			"customer_msg":    plan.CustomerMsg,
		},
	})
}

func (h *Handlers) ConnectionRetry(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "connection id is required")
		return
	}
	ok, wait, err := h.svc.ShouldRetry(id, time.Now().UTC())
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"connection_id": id,
		"retry":         ok,
		"wait_seconds":  wait.Seconds(),
	})
}

func (h *Handlers) RecordAttempt(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "connection id is required")
		return
	}
	if err := h.svc.RecordAttempt(id, time.Now().UTC()); err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	exhausted, _ := h.svc.ConnectionExhausted(id)
	respondJSON(w, http.StatusOK, map[string]interface{}{"connection_id": id, "status": "recorded", "exhausted": exhausted})
}

func (h *Handlers) ConnectionExhausted(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "connection id is required")
		return
	}
	exhausted, err := h.svc.ConnectionExhausted(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"connection_id": id, "exhausted": exhausted})
}

// ── Freshness ─────────────────────────────────────────────────────────────

type slaRequest struct {
	Dataset         string  `json:"dataset"`
	MaxAge          string  `json:"max_age,omitempty"`
	MaxAgeSeconds   float64 `json:"max_age_seconds,omitempty"`
	LastGood        string  `json:"last_good,omitempty"`
	RefreshAt       string  `json:"refresh_at,omitempty"`
	LastGoodSeconds *int64  `json:"-"`
}

func (h *Handlers) SetSLA(w http.ResponseWriter, r *http.Request) {
	var req slaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Dataset) == "" {
		respondError(w, http.StatusBadRequest, "dataset is required")
		return
	}
	var maxAge time.Duration
	switch {
	case req.MaxAge != "":
		d, err := time.ParseDuration(req.MaxAge)
		if err != nil || d <= 0 {
			respondError(w, http.StatusBadRequest, "max_age must be a valid positive duration")
			return
		}
		maxAge = d
	case req.MaxAgeSeconds > 0:
		maxAge = time.Duration(req.MaxAgeSeconds * float64(time.Second))
	default:
		respondError(w, http.StatusBadRequest, "max_age or max_age_seconds is required")
		return
	}
	if err := h.svc.SetSLA(req.Dataset, maxAge); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"dataset": req.Dataset, "max_age_seconds": maxAge.Seconds()})
}

func freshnessToJSON(rep shared.FreshnessReport) map[string]interface{} {
	out := map[string]interface{}{
		"dataset":     rep.Dataset,
		"verdict":     string(rep.Verdict),
		"age_seconds": rep.Age.Seconds(),
		"sla_seconds": rep.SLA.Seconds(),
		"has_data":    rep.HasData,
	}
	if !rep.LastGood.IsZero() {
		out["last_good"] = rep.LastGood.Format(time.RFC3339)
	}
	return out
}

func (h *Handlers) GetFreshness(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dataset := strings.TrimSpace(q.Get("dataset"))
	now := time.Now().UTC()
	if dataset == "" {
		reports := h.svc.ListFreshness(now)
		out := make([]map[string]interface{}, 0, len(reports))
		for _, rep := range reports {
			out = append(out, freshnessToJSON(rep))
		}
		respondJSON(w, http.StatusOK, out)
		return
	}
	if lg := strings.TrimSpace(q.Get("last_good")); lg != "" {
		t, err := time.Parse(time.RFC3339, lg)
		if err != nil {
			respondError(w, http.StatusBadRequest, "last_good must be RFC3339")
			return
		}
		respondJSON(w, http.StatusOK, freshnessToJSON(h.svc.DatasetFreshnessAt(dataset, t, now)))
		return
	}
	respondJSON(w, http.StatusOK, freshnessToJSON(h.svc.DatasetFreshness(dataset, now)))
}

func (h *Handlers) GetOverallFreshness(w http.ResponseWriter, r *http.Request) {
	verdict, reports := h.svc.OverallFreshness(time.Now().UTC())
	out := make([]map[string]interface{}, 0, len(reports))
	for _, rep := range reports {
		out = append(out, freshnessToJSON(rep))
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"verdict": string(verdict), "reports": out})
}

// ── Providers ─────────────────────────────────────────────────────────────

type providerRequest struct {
	Name     string   `json:"name"`
	Declared []string `json:"declared"`
}

func (h *Handlers) RegisterProvider(w http.ResponseWriter, r *http.Request) {
	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Declared) == 0 {
		respondError(w, http.StatusBadRequest, "name and declared capabilities are required")
		return
	}
	declared := make([]shared.Capability, 0, len(req.Declared))
	for _, c := range req.Declared {
		declared = append(declared, shared.Capability(c))
	}
	if err := h.svc.RegisterProvider(req.Name, declared); err != nil {
		if strings.Contains(err.Error(), "already registered") {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]interface{}{"name": req.Name, "declared": req.Declared})
}

type capabilityRequest struct {
	Capability string `json:"capability"`
	Until      string `json:"until,omitempty"`
}

func capabilityFromBody(r *http.Request) (shared.Capability, time.Time, error) {
	var req capabilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return "", time.Time{}, err
	}
	if strings.TrimSpace(req.Capability) == "" {
		return "", time.Time{}, errMissingCapability
	}
	var until time.Time
	if req.Until != "" {
		t, err := time.Parse(time.RFC3339, req.Until)
		if err != nil {
			return "", time.Time{}, errBadUntil
		}
		until = t
	}
	return shared.Capability(req.Capability), until, nil
}

var (
	errMissingCapability = errString("capability is required")
	errBadUntil          = errString("until must be RFC3339")
)

type errString string

func (e errString) Error() string { return string(e) }

func (h *Handlers) DeclareOutage(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	cap, until, err := capabilityFromBody(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if until.IsZero() {
		respondError(w, http.StatusBadRequest, "until is required")
		return
	}
	if err := h.svc.DeclareOutage(name, cap, until); err != nil {
		if isUnknownErr(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "outage declared", "provider": name})
}

func (h *Handlers) RecordAuthFailure(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	cap, _, err := capabilityFromBody(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.RecordAuthFailure(name, cap); err != nil {
		if isUnknownErr(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "auth failure recorded", "provider": name})
}

func (h *Handlers) RecordSuccess(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	cap, _, err := capabilityFromBody(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.RecordSuccess(name, cap); err != nil {
		if isUnknownErr(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "success recorded", "provider": name})
}

func (h *Handlers) MarkUnsupported(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	cap, _, err := capabilityFromBody(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.svc.MarkUnsupported(name, cap); err != nil {
		if isUnknownErr(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "marked unsupported", "provider": name})
}

func (h *Handlers) CheckCapability(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	cap := shared.Capability(strings.TrimSpace(r.URL.Query().Get("capability")))
	if cap == "" {
		respondError(w, http.StatusBadRequest, "capability query param is required")
		return
	}
	avail, err := h.svc.CheckCapability(name, cap, time.Now().UTC())
	if err != nil {
		if isUnknownErr(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"provider":     name,
		"capability":   string(cap),
		"availability": string(avail),
	})
}

func (h *Handlers) CapabilityMatrix(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"matrix": h.svc.CapabilityMatrix(time.Now().UTC())})
}
