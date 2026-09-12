package cardtokens

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	sharedtokens "github.com/nexora/nexora/shared/cardtokens"
)

// Handlers exposes PAN card tokens over /v1/card-tokens.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds card-token handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts /v1/card-tokens routes on the shared mux router.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/card-tokens/issue", h.Issue).Methods("POST")
	router.HandleFunc("/v1/card-tokens/sweep-expired", h.SweepExpired).Methods("POST")
	router.HandleFunc("/v1/card-tokens/{id}", h.Get).Methods("GET")
	router.HandleFunc("/v1/card-tokens/{id}/suspend", h.Suspend).Methods("POST")
	router.HandleFunc("/v1/card-tokens/{id}/resume", h.Resume).Methods("POST")
	router.HandleFunc("/v1/card-tokens/{id}/rotate", h.Rotate).Methods("POST")
	router.HandleFunc("/v1/card-tokens/{id}/revoke", h.Revoke).Methods("POST")
	router.HandleFunc("/v1/card-tokens/{id}/authorize", h.Authorize).Methods("POST")
	router.HandleFunc("/v1/card-tokens/{id}/scope", h.NarrowScope).Methods("PUT")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, sharedtokens.ErrIssuerTokenNotFound):
		return http.StatusNotFound
	case errors.Is(err, sharedtokens.ErrIllegalIssuerMove),
		errors.Is(err, sharedtokens.ErrIssuerScopeWiden):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

type issueRequest struct {
	PANFingerprint string             `json:"pan_fingerprint"`
	Scope          sharedtokens.Scope `json:"scope"`
	TTLSeconds     int64              `json:"ttl_seconds,omitempty"`
	Dependents     []string           `json:"dependents,omitempty"`
}

func (h *Handlers) Issue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PANFingerprint == "" {
		respondError(w, http.StatusBadRequest, "pan_fingerprint is required")
		return
	}
	var ttl time.Duration
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	tok, err := h.svc.Issue(req.PANFingerprint, req.Scope, ttl, req.Dependents)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("token_id", tok.ID).Msg("card token issued via API")
	respondJSON(w, http.StatusCreated, tok)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	tok, err := h.svc.Get(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, tok)
}

func (h *Handlers) Suspend(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	tok, err := h.svc.Suspend(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("token_id", id).Msg("card token suspended via API")
	respondJSON(w, http.StatusOK, tok)
}

func (h *Handlers) Resume(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	tok, err := h.svc.Resume(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("token_id", id).Msg("card token resumed via API")
	respondJSON(w, http.StatusOK, tok)
}

func (h *Handlers) Rotate(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	tok, err := h.svc.Rotate(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("old_token", id).Str("new_token", tok.ID).Msg("card token rotated via API")
	respondJSON(w, http.StatusCreated, tok)
}

func (h *Handlers) Revoke(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	tok, err := h.svc.Revoke(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("token_id", id).Msg("card token revoked via API")
	respondJSON(w, http.StatusOK, tok)
}

func (h *Handlers) SweepExpired(w http.ResponseWriter, r *http.Request) {
	n := h.svc.SweepExpired()
	h.logger.Info().Int("swept", n).Msg("card token expiry sweep via API")
	respondJSON(w, http.StatusOK, map[string]interface{}{"swept": n})
}

func (h *Handlers) Authorize(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	var ctx sharedtokens.AuthContext
	if err := json.NewDecoder(r.Body).Decode(&ctx); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	decision, reason, err := h.svc.Authorize(id, ctx)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"decision": decision, "reason": reason})
}

func (h *Handlers) NarrowScope(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "token id is required")
		return
	}
	var scope sharedtokens.Scope
	if err := json.NewDecoder(r.Body).Decode(&scope); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tok, err := h.svc.NarrowScope(id, scope)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("token_id", id).Msg("card token scope narrowed via API")
	respondJSON(w, http.StatusOK, tok)
}
