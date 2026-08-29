package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/reconciliation-service/internal/domain"
	"github.com/nexora/nexora/services/reconciliation-service/internal/service"
)

type Handlers struct {
	reconService *service.ReconciliationService
	logger       zerolog.Logger
}

func NewHandlers(reconService *service.ReconciliationService, logger zerolog.Logger) *Handlers {
	return &Handlers{reconService: reconService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/reconciliation/records", h.CreateRecord).Methods("POST")
	router.HandleFunc("/v1/reconciliation/records/{id}", h.GetRecord).Methods("GET")
	router.HandleFunc("/v1/reconciliation/records/{id}/match", h.MatchRecord).Methods("POST")
	router.HandleFunc("/v1/reconciliation/records/{id}/discrepancy", h.FlagDiscrepancy).Methods("POST")
	router.HandleFunc("/v1/reconciliation/records/{id}/resolve", h.ResolveRecord).Methods("POST")
	router.HandleFunc("/v1/reconciliation/pending", h.GetPendingRecords).Methods("GET")
	router.HandleFunc("/v1/reconciliation/reconcile", h.ReconcilePayment).Methods("POST")
	router.HandleFunc("/v1/reconciliation/reconcile-unknown", h.ReconcileUnknownPayment).Methods("POST")
	router.HandleFunc("/v1/reconciliation/run-scheduled", h.RunScheduledReconciliation).Methods("POST")
	router.HandleFunc("/v1/reconciliation/record-discrepancy", h.RecordDiscrepancy).Methods("POST")
	router.HandleFunc("/v1/reconciliation/cases", h.GetPendingCases).Methods("GET")
	router.HandleFunc("/v1/reconciliation/cases/discrepancy", h.GetDiscrepancyCases).Methods("GET")
	router.HandleFunc("/v1/reconciliation/cases/{id}", h.GetCase).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) CreateRecord(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateReconciliationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	record, err := h.reconService.CreateRecord(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, record)
}

func (h *Handlers) GetRecord(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid record ID")
		return
	}
	record, err := h.reconService.GetRecord(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, record)
}

func (h *Handlers) MatchRecord(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid record ID")
		return
	}
	if err := h.reconService.MatchRecord(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "record matched"})
}

func (h *Handlers) FlagDiscrepancy(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid record ID")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if err := h.reconService.FlagDiscrepancy(r.Context(), id, req.Reason); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "discrepancy flagged"})
}

func (h *Handlers) ResolveRecord(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid record ID")
		return
	}
	if err := h.reconService.ResolveRecord(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "record resolved"})
}

func (h *Handlers) GetPendingRecords(w http.ResponseWriter, r *http.Request) {
	records, err := h.reconService.GetPendingRecords(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, records)
}

func (h *Handlers) ReconcilePayment(w http.ResponseWriter, r *http.Request) {
	var req domain.ReconcilePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	paymentID, err := uuid.Parse(req.PaymentID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}
	providerStatus := &domain.ProviderPaymentStatus{
		PaymentID:      req.PaymentID,
		Status:         req.ProviderResponse,
		Amount:         0,
		Currency:       "GBP",
		TransactionRef: "",
	}
	result, err := h.reconService.ReconcilePayment(r.Context(), paymentID, providerStatus)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handlers) ReconcileUnknownPayment(w http.ResponseWriter, r *http.Request) {
	var req domain.ReconcileUnknownPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	paymentID, err := uuid.Parse(req.PaymentID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}
	result, err := h.reconService.ReconcileUnknownPayment(r.Context(), paymentID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handlers) RunScheduledReconciliation(w http.ResponseWriter, r *http.Request) {
	var req domain.RunScheduledReconciliationRequest
	json.NewDecoder(r.Body).Decode(&req)
	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}
	results, err := h.reconService.RunScheduledReconciliation(r.Context(), batchSize)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"results": results,
		"count":   len(results),
	})
}

func (h *Handlers) RecordDiscrepancy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TransactionID  string `json:"transaction_id"`
		InternalState  string `json:"internal_state"`
		ExternalState  string `json:"external_state"`
		Resolution     string `json:"resolution"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	txID, err := uuid.Parse(req.TransactionID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid transaction ID")
		return
	}
	resolution := domain.ResolutionType(req.Resolution)
	if err := h.reconService.RecordDiscrepancy(r.Context(), txID, req.InternalState, req.ExternalState, resolution); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "discrepancy recorded"})
}

func (h *Handlers) GetPendingCases(w http.ResponseWriter, r *http.Request) {
	cases, err := h.reconService.GetPendingCases(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cases)
}

func (h *Handlers) GetDiscrepancyCases(w http.ResponseWriter, r *http.Request) {
	cases, err := h.reconService.GetDiscrepancyCases(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cases)
}

func (h *Handlers) GetCase(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid case ID")
		return
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	_ = limit
	caseData, err := h.reconService.GetCase(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, caseData)
}
