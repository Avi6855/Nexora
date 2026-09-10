package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/cards"
)

type Handlers struct {
	p *Platform
}

func NewHandlers(p *Platform) *Handlers { return &Handlers{p: p} }

func (h *Handlers) RegisterPlatformRoutes(router *mux.Router) {
	router.HandleFunc("/v1/vcards", h.createVCard).Methods("POST")
	router.HandleFunc("/v1/vcards/{id}/authorize", h.authorizeMerchant).Methods("POST")
	router.HandleFunc("/v1/vcards/{id}/rules", h.setRules).Methods("PUT")
	router.HandleFunc("/v1/vcards/{id}/evaluate", h.evaluateSpend).Methods("POST")
	router.HandleFunc("/v1/vcards/tokens", h.registerToken).Methods("POST")
	router.HandleFunc("/v1/vcards/tokens/replace", h.replaceCredentials).Methods("POST")
	router.HandleFunc("/v1/vcards/tokens/{id}/suspend", h.suspendToken).Methods("POST")
	router.HandleFunc("/v1/vcards/lifecycle", h.startLifecycle).Methods("POST")
	router.HandleFunc("/v1/vcards/lifecycle/{id}/advance", h.advanceLifecycle).Methods("POST")
	router.HandleFunc("/v1/vcards/lifecycle/{id}/partner-failure", h.partnerFailure).Methods("POST")
	router.HandleFunc("/v1/vcards/delivery/classify", h.classifyDelivery).Methods("POST")
}

func RegisterPlatformRoutes(router *mux.Router, p *Platform) {
	NewHandlers(p).RegisterPlatformRoutes(router)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) createVCard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LinkedCardID string `json:"linked_card_id"`
		LockedGroup  string `json:"locked_group"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	vc, err := h.p.CreateLockedCard(req.LinkedCardID, req.LockedGroup)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, vc)
}

func (h *Handlers) authorizeMerchant(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		MerchantID   string `json:"merchant_id"`
		MerchantName string `json:"merchant_name"`
		Group        string `json:"group"`
		AmountMinor  int64  `json:"amount_minor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MerchantName == "" && req.MerchantID == "" {
		respondError(w, http.StatusBadRequest, "merchant_id or merchant_name is required")
		return
	}
	m := cards.MerchantIdentity{ID: req.MerchantID, Name: req.MerchantName, Group: req.Group}
	if err := h.p.AuthorizeMerchant(id, m, req.AmountMinor); err != nil {
		if errors.Is(err, ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, cards.ErrMerchantLocked) || errors.Is(err, cards.ErrCardNotActive) {
			respondJSON(w, http.StatusPaymentRequired, map[string]interface{}{"allowed": false, "reason": err.Error()})
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"allowed": true, "card_id": id})
}

func (h *Handlers) setRules(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		Rules []cards.AuthRule `json:"rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.p.SetRules(id, req.Rules); err != nil {
		if errors.Is(err, ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"card_id": id, "rules": req.Rules})
}

func (h *Handlers) evaluateSpend(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var att cards.AuthAttempt
	if err := json.NewDecoder(r.Body).Decode(&att); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if att.At.IsZero() {
		att.At = time.Now().UTC()
	}
	decision, reason, err := h.p.EvaluateSpend(id, att)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	status := http.StatusOK
	if decision == cards.ActionBlock {
		status = http.StatusPaymentRequired
	}
	respondJSON(w, status, map[string]interface{}{"decision": decision, "reason": reason})
}

func (h *Handlers) registerToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TokenID  string `json:"token_id"`
		CardID   string `json:"card_id"`
		Merchant string `json:"merchant"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := h.p.RegisterToken(req.TokenID, req.CardID, req.Merchant, time.Now().UTC())
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, t)
}

func (h *Handlers) replaceCredentials(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldCardID string `json:"old_card_id"`
		NewCardID string `json:"new_card_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.OldCardID == "" || req.NewCardID == "" {
		respondError(w, http.StatusBadRequest, "old_card_id and new_card_id are required")
		return
	}
	n := h.p.ReplaceCardCredentials(req.OldCardID, req.NewCardID)
	respondJSON(w, http.StatusOK, map[string]interface{}{"remapped": n})
}

func (h *Handlers) suspendToken(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.p.SuspendToken(id); err != nil {
		if errors.Is(err, cards.ErrTokenUnknown) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "token suspended"})
}

func (h *Handlers) startLifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CardID string `json:"card_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s, err := h.p.StartLifecycle(req.CardID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, ErrConflict) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, s)
}

func (h *Handlers) advanceLifecycle(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	s, err := h.p.AdvanceLifecycle(id, time.Now().UTC())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, cards.ErrIllegalStage) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, s)
}

func (h *Handlers) partnerFailure(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	s, err := h.p.ReportPartnerFailure(id, time.Now().UTC(), req.Reason)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, s)
}

func (h *Handlers) classifyDelivery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason    string `json:"reason"`
		RawReason string `json:"raw_reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	raw := req.Reason
	if raw == "" {
		raw = req.RawReason
	}
	wf, err := h.p.ClassifyDelivery(raw)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, wf)
}
