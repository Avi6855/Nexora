package releaseops

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/releaseops"
)

// Handlers exposes the release-ops API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/release-ops.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/release-ops/forecasts", h.Forecast).Methods("POST")
	router.HandleFunc("/v1/release-ops/deps", h.RegisterDep).Methods("POST")
	router.HandleFunc("/v1/release-ops/health", h.ReportHealth).Methods("POST")
	router.HandleFunc("/v1/release-ops/deploy-gate", h.DeployGate).Methods("POST")
	router.HandleFunc("/v1/release-ops/canaries", h.StartCanary).Methods("POST")
	router.HandleFunc("/v1/release-ops/canaries/{id}/advance", h.AdvanceCanary).Methods("POST")
	router.HandleFunc("/v1/release-ops/canaries/{id}", h.GetCanary).Methods("GET")
	router.HandleFunc("/v1/release-ops/usage", h.RecordUsage).Methods("POST")
	router.HandleFunc("/v1/release-ops/fields/stats", h.FieldStats).Methods("GET")
	router.HandleFunc("/v1/release-ops/fields/safe-to-remove", h.SafeToRemove).Methods("GET")
	router.HandleFunc("/v1/release-ops/fields/deprecate", h.DeprecateField).Methods("POST")
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
	return err == shared.ErrUnknownCanary || err == shared.ErrUnknownField ||
		(err != nil && strings.Contains(err.Error(), "unknown "))
}

func isBlocked(err error) bool {
	return err == shared.ErrDeployBlocked
}

// ── Forecasts ───────────────────────────────────────────────────────────────

type forecastRequest struct {
	Service string    `json:"service"`
	Samples []float64 `json:"samples,omitempty"`
	Uplifts []struct {
		Name       string  `json:"name"`
		Multiplier float64 `json:"multiplier"`
	} `json:"uplifts,omitempty"`
	PerReplicaRPS float64 `json:"per_replica_rps"`
}

func (h *Handlers) Forecast(w http.ResponseWriter, r *http.Request) {
	var req forecastRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	uplifts := make([]shared.EventUplift, 0, len(req.Uplifts))
	for _, u := range req.Uplifts {
		uplifts = append(uplifts, shared.EventUplift{Name: u.Name, Multiplier: u.Multiplier})
	}
	fc, err := h.svc.Forecast(req.Service, req.Samples, uplifts, req.PerReplicaRPS)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, fc)
}

// ── Release manager ─────────────────────────────────────────────────────────

type depRequest struct {
	Service   string `json:"service"`
	DependsOn string `json:"depends_on"`
}

func (h *Handlers) RegisterDep(w http.ResponseWriter, r *http.Request) {
	var req depRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RegisterDep(req.Service, req.DependsOn); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, req)
}

type healthRequest struct {
	Service string `json:"service"`
	Status  string `json:"status"`
}

func (h *Handlers) ReportHealth(w http.ResponseWriter, r *http.Request) {
	var req healthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.ReportHealth(req.Service, strings.ToUpper(strings.TrimSpace(req.Status))); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"service": req.Service, "status": strings.ToUpper(strings.TrimSpace(req.Status))})
}

func (h *Handlers) DeployGate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Service) == "" {
		respondError(w, http.StatusBadRequest, "service is required")
		return
	}
	allowed, reason := h.svc.GateDeploy(req.Service)
	respondJSON(w, http.StatusOK, map[string]interface{}{"service": req.Service, "allowed": allowed, "reason": reason})
}

func (h *Handlers) StartCanary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.svc.StartCanary(req.Service, req.Version)
	if err != nil {
		if isBlocked(err) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

func (h *Handlers) AdvanceCanary(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.AdvanceCanary(mux.Vars(r)["id"])
	if err != nil {
		switch {
		case isUnknown(err):
			respondError(w, http.StatusNotFound, err.Error())
		case isBlocked(err):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handlers) GetCanary(w http.ResponseWriter, r *http.Request) {
	c, err := h.svc.GetCanary(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, c)
}

// ── Contract observatory ────────────────────────────────────────────────────

type usageRequest struct {
	Endpoint string `json:"endpoint"`
	Field    string `json:"field"`
	Used     bool   `json:"used"`
}

func (h *Handlers) RecordUsage(w http.ResponseWriter, r *http.Request) {
	var req usageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RecordUsage(req.Endpoint, req.Field, req.Used); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, req)
}

func (h *Handlers) FieldStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	st, err := h.svc.FieldStats(q.Get("endpoint"), q.Get("field"))
	if err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, st)
}

func (h *Handlers) SafeToRemove(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	threshold := 5.0
	if raw := strings.TrimSpace(q.Get("threshold_pct")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			respondError(w, http.StatusBadRequest, "threshold_pct must be a non-negative number")
			return
		}
		threshold = v
	}
	safe, st, err := h.svc.SafeToRemove(q.Get("endpoint"), q.Get("field"), threshold)
	if err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"endpoint": st.Endpoint, "field": st.Field, "usage_pct": st.UsagePct,
		"threshold_pct": threshold, "safe_to_remove": safe, "deprecated": st.Deprecated,
	})
}

func (h *Handlers) DeprecateField(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
		Field    string `json:"field"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.Deprecate(req.Endpoint, req.Field); err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deprecated", "endpoint": req.Endpoint, "field": req.Field})
}
