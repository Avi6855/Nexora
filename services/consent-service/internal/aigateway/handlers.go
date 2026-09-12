package aigateway

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/aigateway"
	"time"
)

// Handlers exposes the AI-gateway API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/ai-gateway.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/ai-gateway/scopes", h.GrantScope).Methods("POST")
	router.HandleFunc("/v1/ai-gateway/scopes", h.RevokeScope).Methods("DELETE")
	router.HandleFunc("/v1/ai-gateway/intents", h.SubmitIntent).Methods("POST")
	router.HandleFunc("/v1/ai-gateway/intents/{id}", h.GetIntent).Methods("GET")
	router.HandleFunc("/v1/ai-gateway/intents/{id}/confirm", h.ConfirmIntent).Methods("POST")
	router.HandleFunc("/v1/ai-gateway/intents/{id}/execute", h.ExecuteIntent).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func mapGatewayError(err error) int {
	switch err {
	case nil:
		return http.StatusOK
	case shared.ErrIntentNotFound, shared.ErrNoConsent:
		return http.StatusNotFound
	case shared.ErrOverLimit, shared.ErrRiskBlocked:
		return http.StatusConflict
	case shared.ErrConfirmRequired:
		return http.StatusConflict
	case shared.ErrConfirmExpired, shared.ErrIntentClosed:
		return http.StatusConflict
	case shared.ErrBadToken:
		return http.StatusBadRequest
	default:
		return http.StatusBadRequest
	}
}

type scopeRequest struct {
	Recipient  string `json:"recipient"`
	Action     string `json:"action"`
	LimitMinor int64  `json:"limit_minor"`
	MaxRisk    int    `json:"max_risk"`
}

func (h *Handlers) GrantScope(w http.ResponseWriter, r *http.Request) {
	var req scopeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.GrantScope(req.Recipient, req.Action, req.LimitMinor, req.MaxRisk, time.Now().UTC()); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().Str("recipient", req.Recipient).Str("action", req.Action).Msg("ai scope granted")
	respondJSON(w, http.StatusCreated, map[string]interface{}{
		"recipient": req.Recipient, "action": req.Action,
		"limit_minor": req.LimitMinor, "max_risk": req.MaxRisk,
	})
}

func (h *Handlers) RevokeScope(w http.ResponseWriter, r *http.Request) {
	var req scopeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Allow query-param revocation as well.
	if strings.TrimSpace(req.Recipient) == "" {
		req.Recipient = r.URL.Query().Get("recipient")
	}
	if strings.TrimSpace(req.Action) == "" {
		req.Action = r.URL.Query().Get("action")
	}
	if err := h.svc.RevokeScope(req.Recipient, req.Action); err != nil {
		respondError(w, mapGatewayError(err), err.Error())
		return
	}
	h.logger.Info().Str("recipient", req.Recipient).Str("action", req.Action).Msg("ai scope revoked")
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

type intentRequest struct {
	Action string            `json:"action"`
	Params map[string]string `json:"params"`
}

func (h *Handlers) SubmitIntent(w http.ResponseWriter, r *http.Request) {
	var req intentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in, err := h.svc.SubmitIntent(req.Action, req.Params, time.Now().UTC())
	if err != nil {
		respondError(w, mapGatewayError(err), err.Error())
		return
	}
	h.logger.Info().Str("intent_id", in.ID).Str("action", in.Action).Msg("ai intent submitted")
	respondJSON(w, http.StatusCreated, in)
}

func (h *Handlers) GetIntent(w http.ResponseWriter, r *http.Request) {
	in, err := h.svc.Get(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, mapGatewayError(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, in)
}

func (h *Handlers) ConfirmIntent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.Token) == "" {
		req.Token = r.URL.Query().Get("token")
	}
	if err := h.svc.Confirm(mux.Vars(r)["id"], req.Token, time.Now().UTC()); err != nil {
		respondError(w, mapGatewayError(err), err.Error())
		return
	}
	h.logger.Info().Str("intent_id", mux.Vars(r)["id"]).Msg("ai intent confirmed")
	in, _ := h.svc.Get(mux.Vars(r)["id"])
	respondJSON(w, http.StatusOK, in)
}

func (h *Handlers) ExecuteIntent(w http.ResponseWriter, r *http.Request) {
	outcome, reason, err := h.svc.Execute(mux.Vars(r)["id"], time.Now().UTC())
	if err != nil {
		respondError(w, mapGatewayError(err), err.Error())
		return
	}
	if outcome == "executed" {
		h.logger.Info().Str("intent_id", mux.Vars(r)["id"]).Str("outcome", outcome).Msg("ai intent executed")
	} else {
		h.logger.Warn().Str("intent_id", mux.Vars(r)["id"]).Str("outcome", outcome).Str("reason", reason).Msg("ai intent skipped")
	}
	respondJSON(w, http.StatusOK, map[string]string{"outcome": outcome, "reason": reason})
}
