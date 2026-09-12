package paycycle

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	sharedpaycycle "github.com/nexora/nexora/shared/paycycle"
)

// Handlers serves the /v1/pay-cycle API: ETA quotes, the beneficiary trust
// lifecycle and approval chains.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers creates pay-cycle handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the pay-cycle routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/pay-cycle/eta/quote", h.QuoteETA).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/beneficiaries", h.AddBeneficiary).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/beneficiaries/{id}/verify", h.VerifyBeneficiary).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/beneficiaries/{id}/payments", h.RecordPayment).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/beneficiaries/{id}/details", h.UpdateDetails).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/beneficiaries/{id}/dormant", h.MarkDormant).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/beneficiaries/{id}", h.RemoveBeneficiary).Methods("DELETE")
	router.HandleFunc("/v1/pay-cycle/approval-rules", h.AddApprovalRule).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/payments/evaluate", h.EvaluatePayment).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/approvals", h.RequestApproval).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/approvals/{id}/decide", h.DecideApproval).Methods("POST")
	router.HandleFunc("/v1/pay-cycle/approvals/{id}", h.GetApproval).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// parseOptionalTime parses an optional RFC3339 timestamp; empty means zero.
func parseOptionalTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// beneficiaryStatus maps beneficiary failures: 404 unknown ids, 409
// conflicting state (duplicates, illegal transitions, cooling), 400 invalid
// input.
func beneficiaryStatus(err error) int {
	if errors.Is(err, sharedpaycycle.ErrBeneficiaryNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, sharedpaycycle.ErrBeneficiaryExists) ||
		errors.Is(err, sharedpaycycle.ErrBeneficiaryState) ||
		errors.Is(err, sharedpaycycle.ErrBeneficiaryCooling) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// approvalStatus maps approval failures: 404 unknown ids, 409 conflicting
// state (duplicate rules, decided/expired requests), 400 invalid input.
func approvalStatus(err error) int {
	if errors.Is(err, sharedpaycycle.ErrApprovalNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, sharedpaycycle.ErrApprovalExists) ||
		errors.Is(err, sharedpaycycle.ErrApprovalState) ||
		errors.Is(err, sharedpaycycle.ErrApprovalExpired) {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

type quoteETARequest struct {
	Rail        string `json:"rail"`
	AmountMinor int64  `json:"amount_minor"`
	Now         string `json:"now,omitempty"` // RFC3339, defaults to server time
}

// QuoteETA promises an arrival time on a rail.
func (h *Handlers) QuoteETA(w http.ResponseWriter, r *http.Request) {
	var req quoteETARequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Rail == "" {
		respondError(w, http.StatusBadRequest, "rail is required")
		return
	}
	if req.AmountMinor <= 0 {
		respondError(w, http.StatusBadRequest, "amount_minor must be positive")
		return
	}
	now, err := parseOptionalTime(req.Now)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid now, use RFC3339")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	quote, err := h.svc.QuoteETA(req.Rail, req.AmountMinor, now)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, quote)
}

type addBeneficiaryRequest struct {
	OwnerID       string `json:"owner_id,omitempty"`
	Name          string `json:"name"`
	SortCode      string `json:"sort_code"`
	AccountNumber string `json:"account_number"`
}

// AddBeneficiary saves a new CREATED beneficiary.
func (h *Handlers) AddBeneficiary(w http.ResponseWriter, r *http.Request) {
	var req addBeneficiaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.SortCode == "" || req.AccountNumber == "" {
		respondError(w, http.StatusBadRequest, "sort_code and account_number are required")
		return
	}
	b, err := h.svc.AddBeneficiary(req.OwnerID, req.Name, req.SortCode, req.AccountNumber, time.Now().UTC())
	if err != nil {
		respondError(w, beneficiaryStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, b)
}

// VerifyBeneficiary advances beneficiary trust one step.
func (h *Handlers) VerifyBeneficiary(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "beneficiary id is required")
		return
	}
	b, err := h.svc.VerifyBeneficiary(id)
	if err != nil {
		respondError(w, beneficiaryStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, b)
}

type recordPaymentRequest struct {
	Now string `json:"now,omitempty"` // RFC3339, defaults to server time
}

// RecordPayment records a successful payment to a beneficiary.
func (h *Handlers) RecordPayment(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "beneficiary id is required")
		return
	}
	var req recordPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now, err := parseOptionalTime(req.Now)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid now, use RFC3339")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	b, err := h.svc.RecordPayment(id, now)
	if err != nil {
		respondError(w, beneficiaryStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, b)
}

type updateDetailsRequest struct {
	SortCode      string `json:"sort_code"`
	AccountNumber string `json:"account_number"`
	Now           string `json:"now,omitempty"` // RFC3339, defaults to server time
}

// UpdateDetails replaces beneficiary account details.
func (h *Handlers) UpdateDetails(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "beneficiary id is required")
		return
	}
	var req updateDetailsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.SortCode == "" || req.AccountNumber == "" {
		respondError(w, http.StatusBadRequest, "sort_code and account_number are required")
		return
	}
	now, err := parseOptionalTime(req.Now)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid now, use RFC3339")
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	changed, b, err := h.svc.UpdateBeneficiaryDetails(id, req.SortCode, req.AccountNumber, now)
	if err != nil {
		respondError(w, beneficiaryStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"changed": changed, "beneficiary": b})
}

// MarkDormant parks a beneficiary as DORMANT.
func (h *Handlers) MarkDormant(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "beneficiary id is required")
		return
	}
	b, err := h.svc.MarkDormant(id)
	if err != nil {
		respondError(w, beneficiaryStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, b)
}

// RemoveBeneficiary tombstones a beneficiary.
func (h *Handlers) RemoveBeneficiary(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "beneficiary id is required")
		return
	}
	b, err := h.svc.RemoveBeneficiary(id)
	if err != nil {
		respondError(w, beneficiaryStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, b)
}

type addApprovalRuleRequest struct {
	ID                 string `json:"id,omitempty"`
	MinAmountMinor     int64  `json:"min_amount_minor,omitempty"`
	Merchant           string `json:"merchant,omitempty"`
	Country            string `json:"country,omitempty"`
	NewBeneficiaryOnly bool   `json:"new_beneficiary_only,omitempty"`
	Decision           string `json:"decision"`
	DelaySeconds       int64  `json:"delay_seconds,omitempty"`
}

// AddApprovalRule stores an approval rule.
func (h *Handlers) AddApprovalRule(w http.ResponseWriter, r *http.Request) {
	var req addApprovalRuleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Decision == "" {
		respondError(w, http.StatusBadRequest, "decision is required")
		return
	}
	rule, err := h.svc.AddApprovalRule(sharedpaycycle.ApprovalRule{
		ID:                 req.ID,
		MinAmountMinor:     req.MinAmountMinor,
		Merchant:           req.Merchant,
		Country:            req.Country,
		NewBeneficiaryOnly: req.NewBeneficiaryOnly,
		Decision:           sharedpaycycle.Decision(req.Decision),
		DelaySeconds:       req.DelaySeconds,
	})
	if err != nil {
		respondError(w, approvalStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, rule)
}

type evaluatePaymentRequest struct {
	AmountMinor      int64  `json:"amount_minor"`
	Merchant         string `json:"merchant,omitempty"`
	Country          string `json:"country,omitempty"`
	IsNewBeneficiary bool   `json:"is_new_beneficiary,omitempty"`
}

// EvaluatePayment returns the approval decision for a payment intent.
func (h *Handlers) EvaluatePayment(w http.ResponseWriter, r *http.Request) {
	var req evaluatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	decision, ruleID := h.svc.EvaluatePayment(sharedpaycycle.PaymentIntent{
		AmountMinor:      req.AmountMinor,
		Merchant:         req.Merchant,
		Country:          req.Country,
		IsNewBeneficiary: req.IsNewBeneficiary,
	})
	respondJSON(w, http.StatusOK, map[string]interface{}{"decision": decision, "rule_id": ruleID})
}

type requestApprovalRequest struct {
	AmountMinor      int64  `json:"amount_minor"`
	Merchant         string `json:"merchant,omitempty"`
	Country          string `json:"country,omitempty"`
	IsNewBeneficiary bool   `json:"is_new_beneficiary,omitempty"`
	TTLSeconds       int64  `json:"ttl_seconds,omitempty"`
}

// RequestApproval opens a PENDING approval request.
func (h *Handlers) RequestApproval(w http.ResponseWriter, r *http.Request) {
	var req requestApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ttl := time.Duration(req.TTLSeconds) * time.Second
	approval, err := h.svc.RequestApproval(sharedpaycycle.PaymentIntent{
		AmountMinor:      req.AmountMinor,
		Merchant:         req.Merchant,
		Country:          req.Country,
		IsNewBeneficiary: req.IsNewBeneficiary,
	}, ttl, time.Now().UTC())
	if err != nil {
		respondError(w, approvalStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, approval)
}

type decideApprovalRequest struct {
	Approve bool `json:"approve"`
}

// DecideApproval approves or rejects a PENDING request.
func (h *Handlers) DecideApproval(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "approval id is required")
		return
	}
	var req decideApprovalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	approval, err := h.svc.DecideApproval(id, req.Approve, time.Now().UTC())
	if err != nil {
		respondError(w, approvalStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, approval)
}

// GetApproval returns one approval request.
func (h *Handlers) GetApproval(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "approval id is required")
		return
	}
	approval, err := h.svc.GetApproval(id, time.Now().UTC())
	if err != nil {
		respondError(w, approvalStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, approval)
}
