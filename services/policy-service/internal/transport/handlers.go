package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
	"github.com/nexora/nexora/services/policy-service/internal/service"
)

type Handlers struct {
	policyService *service.PolicyService
	flagService   *service.FlagService
	logger        zerolog.Logger
}

func NewHandlers(policyService *service.PolicyService, flagService *service.FlagService, logger zerolog.Logger) *Handlers {
	return &Handlers{policyService: policyService, flagService: flagService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/policies", h.CreatePolicy).Methods("POST")
	router.HandleFunc("/v1/policies/{id}", h.GetPolicy).Methods("GET")
	router.HandleFunc("/v1/policies/{id}", h.UpdatePolicy).Methods("PUT")
	router.HandleFunc("/v1/policies/{id}", h.DeletePolicy).Methods("DELETE")
	router.HandleFunc("/v1/policies/{id}/activate", h.ActivatePolicy).Methods("POST")
	router.HandleFunc("/v1/policies/{id}/disable", h.DisablePolicy).Methods("POST")
	router.HandleFunc("/v1/policies/{id}/testing", h.SetTesting).Methods("POST")
	router.HandleFunc("/v1/policies/{id}/shadow", h.SetShadow).Methods("POST")
	router.HandleFunc("/v1/policies/evaluate", h.EvaluatePayment).Methods("POST")
	router.HandleFunc("/v1/policies/shadow/compare", h.ShadowCompare).Methods("POST")

	// ── Feature flags: progressive rollout + auto-rollback ──
	router.HandleFunc("/v1/flags", h.CreateFlag).Methods("POST")
	router.HandleFunc("/v1/flags", h.ListFlags).Methods("GET")
	router.HandleFunc("/v1/flags/{id}", h.GetFlag).Methods("GET")
	router.HandleFunc("/v1/flags/{id}/rollout", h.AdvanceRollout).Methods("POST")
	router.HandleFunc("/v1/flags/{id}/metrics", h.RecordFlagMetrics).Methods("POST")
	router.HandleFunc("/v1/flags/evaluate", h.EvaluateFlag).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	var req domain.CreatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	policy, err := h.policyService.CreatePolicy(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, policy)
}

func (h *Handlers) GetPolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	policy, err := h.policyService.GetPolicy(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, policy)
}

func (h *Handlers) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	var req domain.CreatePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	policy, err := h.policyService.UpdatePolicy(r.Context(), id, &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, policy)
}

func (h *Handlers) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	if err := h.policyService.DeletePolicy(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "policy deleted"})
}

func (h *Handlers) ActivatePolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	if err := h.policyService.ActivatePolicy(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "policy activated"})
}

func (h *Handlers) DisablePolicy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	if err := h.policyService.DisablePolicy(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "policy disabled"})
}

func (h *Handlers) SetTesting(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	if err := h.policyService.SetTesting(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "policy set to testing"})
}

func (h *Handlers) SetShadow(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid policy ID")
		return
	}
	if err := h.policyService.SetShadow(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "policy set to shadow"})
}

func (h *Handlers) EvaluatePayment(w http.ResponseWriter, r *http.Request) {
	var req domain.EvaluatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := h.policyService.EvaluatePayment(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handlers) ShadowCompare(w http.ResponseWriter, r *http.Request) {
	var req domain.ShadowCompareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := h.policyService.RunShadowPolicy(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handlers) CreateFlag(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateFlagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	flag, err := h.flagService.CreateFlag(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, flag)
}

func (h *Handlers) ListFlags(w http.ResponseWriter, r *http.Request) {
	flags, err := h.flagService.ListFlags(r.Context(), 100)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, flags)
}

func (h *Handlers) GetFlag(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	flags, err := h.flagService.ListFlags(r.Context(), 1000)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, f := range flags {
		if f.FlagID == id {
			respondJSON(w, http.StatusOK, f)
			return
		}
	}
	respondError(w, http.StatusNotFound, "feature flag not found")
}

func (h *Handlers) AdvanceRollout(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	var req struct {
		RolloutPct int `json:"rollout_pct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	flag, err := h.flagService.AdvanceRollout(r.Context(), id, req.RolloutPct)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, flag)
}

func (h *Handlers) RecordFlagMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid flag ID")
		return
	}
	var req domain.RecordFlagMetricRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	flag, err := h.flagService.RecordMetrics(r.Context(), id, &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, flag)
}

func (h *Handlers) EvaluateFlag(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key        string            `json:"key"`
		UserID     string            `json:"user_id"`
		Attributes map[string]string `json:"attributes,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Key == "" || req.UserID == "" {
		respondError(w, http.StatusBadRequest, "key and user_id are required")
		return
	}
	resp, err := h.flagService.Evaluate(r.Context(), req.Key, req.UserID, req.Attributes)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}
