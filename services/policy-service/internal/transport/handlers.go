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
	logger        zerolog.Logger
}

func NewHandlers(policyService *service.PolicyService, logger zerolog.Logger) *Handlers {
	return &Handlers{policyService: policyService, logger: logger}
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
