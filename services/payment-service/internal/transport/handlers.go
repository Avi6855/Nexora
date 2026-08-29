package transport

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/payment-service/internal/domain"
	"github.com/nexora/nexora/services/payment-service/internal/service"
)

type Handlers struct {
	paymentService *service.PaymentService
	logger         zerolog.Logger
}

func NewHandlers(paymentService *service.PaymentService, logger zerolog.Logger) *Handlers {
	return &Handlers{
		paymentService: paymentService,
		logger:         logger,
	}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/payments", h.CreatePayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}", h.GetPayment).Methods("GET")
	router.HandleFunc("/v1/payments/{id}/cancel", h.CancelPayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}/process", h.ProcessPayment).Methods("POST")
	router.HandleFunc("/v1/accounts/{id}/payments", h.GetAccountPayments).Methods("GET")
	router.HandleFunc("/v1/webhooks/provider", h.HandleProviderWebhook).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, domain.ErrorResponse{Error: message})
}

func (h *Handlers) CreatePayment(w http.ResponseWriter, r *http.Request) {
	var req domain.CreatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.IdempotencyKey == "" {
		respondError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}

	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	if req.AccountID == "" {
		respondError(w, http.StatusBadRequest, "account_id is required")
		return
	}

	if req.PaymentType == "" {
		req.PaymentType = domain.PaymentTypeCard
	}

	if req.Currency == "" {
		req.Currency = "GBP"
	}

	payment, err := h.paymentService.CreatePayment(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusCreated, payment)
}

func (h *Handlers) GetPayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}

	payment, err := h.paymentService.GetPayment(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, payment)
}

func (h *Handlers) GetAccountPayments(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	accountID, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	payments, err := h.paymentService.GetPaymentsByAccount(r.Context(), accountID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if payments == nil {
		payments = make([]*domain.Payment, 0)
	}

	respondJSON(w, http.StatusOK, payments)
}

func (h *Handlers) CancelPayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}

	payment, err := h.paymentService.CancelPayment(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, payment)
}

func (h *Handlers) ProcessPayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}

	var req domain.ProcessPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req = domain.ProcessPaymentRequest{}
	}

	payment, err := h.paymentService.ProcessPayment(r.Context(), id, &req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, payment)
}

func (h *Handlers) HandleProviderWebhook(w http.ResponseWriter, r *http.Request) {
	var callback domain.ProviderCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&callback); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if callback.PaymentID == "" {
		respondError(w, http.StatusBadRequest, "payment_id is required")
		return
	}

	if callback.ProviderID == "" {
		respondError(w, http.StatusBadRequest, "provider_id is required")
		return
	}

	if callback.Status == "" {
		respondError(w, http.StatusBadRequest, "status is required")
		return
	}

	payment, err := h.paymentService.HandleProviderCallback(r.Context(), &callback)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, payment)
}

type HealthResponse struct {
	Status    string `json:"status"`
	Service   string `json:"service"`
	Timestamp string `json:"timestamp"`
}

func (h *Handlers) HealthCheck(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, HealthResponse{
		Status:    "ok",
		Service:   "payment-service",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}
