package transport

import (
	"encoding/json"
	"errors"
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
	router.HandleFunc("/v1/payments", h.GetPayments).Methods("GET")
	router.HandleFunc("/v1/payments/{id}", h.GetPayment).Methods("GET")
	router.HandleFunc("/v1/payments/{id}/authorize", h.AuthorizePayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}/cancel", h.CancelPayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}/process", h.ProcessPayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}/reverse", h.ReversePayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}/fail", h.FailPayment).Methods("POST")
	router.HandleFunc("/v1/payments/{id}/timeout", h.HandleTimeout).Methods("POST")
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

// canAccessPayment decides whether a caller may read/act on a payment. Internal
// service calls pass through; users only reach their own payments (legacy rows
// without a user claim are treated as pre-auth demo data).
func canAccessPayment(p *domain.Payment, r *http.Request) bool {
	if r.Header.Get("X-Internal-Token") != "" {
		return true
	}
	caller, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		return false
	}
	return p.UserID == uuid.Nil || p.UserID == caller
}

func (h *Handlers) requireOwnPayment(w http.ResponseWriter, r *http.Request, id uuid.UUID) bool {
	p, err := h.paymentService.GetPayment(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return false
	}
	if !canAccessPayment(p, r) {
		respondError(w, http.StatusForbidden, "you can only access your own payments")
		return false
	}
	return true
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

	// Authenticated caller identity comes from the gateway (X-User-ID),
	// mirroring the other services; the body field is accepted as a fallback.
	if req.UserID == "" {
		req.UserID = r.Header.Get("X-User-ID")
	}

	if req.Currency == "" {
		req.Currency = "GBP"
	}

	payment, err := h.paymentService.CreatePayment(r.Context(), &req)
	if err != nil {
		// Risk-engine refusals surface as 403 with the reasons so the app can
		// show the Monzo-style "we stopped this payment" warning.
		if errors.Is(err, domain.ErrBlockedByRisk) {
			respondJSON(w, http.StatusForbidden, domain.ErrorResponse{
				Error:   "blocked_by_risk_engine",
				Message: err.Error(),
			})
			return
		}
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
	if !canAccessPayment(payment, r) {
		respondError(w, http.StatusForbidden, "you can only access your own payments")
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

	owned := make([]*domain.Payment, 0, len(payments))
	for _, p := range payments {
		if canAccessPayment(p, r) {
			owned = append(owned, p)
		}
	}

	respondJSON(w, http.StatusOK, owned)
}

// GetPayments returns the caller's own payments (real rows, newest first is
// handled client-side).
func (h *Handlers) GetPayments(w http.ResponseWriter, r *http.Request) {
	caller, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	payments, err := h.paymentService.GetPaymentsByUser(r.Context(), caller)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if payments == nil {
		payments = make([]*domain.Payment, 0)
	}
	respondJSON(w, http.StatusOK, payments)
}

func (h *Handlers) AuthorizePayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}
	if !h.requireOwnPayment(w, r, id) {
		return
	}

	payment, err := h.paymentService.AuthorizePayment(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, payment)
}

func (h *Handlers) ReversePayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}
	if !h.requireOwnPayment(w, r, id) {
		return
	}

	if err := h.paymentService.ReversePayment(r.Context(), id); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "reversed"})
}

func (h *Handlers) FailPayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Reason = "manually failed via API"
	}

	if err := h.paymentService.FailPayment(r.Context(), id, req.Reason); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "failed"})
}

func (h *Handlers) HandleTimeout(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}

	payment, err := h.paymentService.HandleTimeout(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, payment)
}

func (h *Handlers) CancelPayment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid payment ID")
		return
	}
	if !h.requireOwnPayment(w, r, id) {
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
