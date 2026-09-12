package consentcentre

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// Handlers exposes the Consent Centre API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/consent-centre.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/consent-centre/providers", h.ConnectProvider).Methods("POST")
	router.HandleFunc("/v1/consent-centre/providers/{name}/data/{datatype}", h.SetToggle).Methods("PUT")
	router.HandleFunc("/v1/consent-centre/providers/{name}/check", h.CheckAccess).Methods("GET")
	router.HandleFunc("/v1/consent-centre/providers/{name}", h.RevokeProvider).Methods("DELETE")
	router.HandleFunc("/v1/consent-centre/providers/{name}/access-log", h.GetAccessLog).Methods("GET")
	router.HandleFunc("/v1/consent-centre/sweep", h.Sweep).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type connectRequest struct {
	Name      string          `json:"name"`
	Purpose   string          `json:"purpose"`
	Datatypes map[string]bool `json:"datatypes,omitempty"`
	ExpiresAt string          `json:"expires_at,omitempty"`
}

func (h *Handlers) ConnectProvider(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respondError(w, http.StatusBadRequest, ErrNameRequired.Error())
		return
	}
	if strings.TrimSpace(req.Purpose) == "" {
		respondError(w, http.StatusBadRequest, ErrPurposeRequired.Error())
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(90 * 24 * time.Hour)
	if strings.TrimSpace(req.ExpiresAt) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt))
		if err != nil {
			respondError(w, http.StatusBadRequest, "expires_at must be RFC3339")
			return
		}
		expiresAt = t.UTC()
	}
	p, err := h.svc.Connect(req.Name, req.Purpose, req.Datatypes, expiresAt, now)
	if err != nil {
		switch {
		case errors.Is(err, ErrProviderExists):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	h.logger.Info().Str("provider", p.Name).Str("purpose", p.Purpose).Msg("consent-centre provider connected")
	respondJSON(w, http.StatusCreated, p)
}

type toggleRequest struct {
	Enabled bool `json:"enabled"`
}

func (h *Handlers) SetToggle(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	var req toggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.SetToggle(vars["name"], vars["datatype"], req.Enabled, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, ErrProviderNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrProviderRevoked), errors.Is(err, ErrProviderExpired):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	h.logger.Info().
		Str("provider", vars["name"]).
		Str("datatype", normaliseDatatype(vars["datatype"])).
		Bool("enabled", req.Enabled).
		Msg("consent-centre datatype toggle updated")
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"provider": vars["name"],
		"datatype": normaliseDatatype(vars["datatype"]),
		"enabled":  req.Enabled,
	})
}

func (h *Handlers) CheckAccess(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	datatype := strings.TrimSpace(r.URL.Query().Get("datatype"))
	if datatype == "" {
		respondError(w, http.StatusBadRequest, "datatype query param is required")
		return
	}
	q := r.URL.Query()
	allowed, reason, err := h.svc.Check(
		vars["name"], datatype,
		q.Get("who"), q.Get("purpose"), q.Get("ticket"),
		time.Now().UTC(),
	)
	if err != nil {
		switch {
		case errors.Is(err, ErrProviderNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	decision := "DENY"
	if allowed {
		decision = "ALLOW"
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"provider": vars["name"],
		"datatype": normaliseDatatype(datatype),
		"decision": decision,
		"reason":   reason,
	})
}

func (h *Handlers) RevokeProvider(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if err := h.svc.Revoke(name, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, ErrProviderNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrAlreadyRevoked):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	h.logger.Info().Str("provider", name).Msg("consent-centre provider revoked")
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked", "provider": name})
}

func (h *Handlers) GetAccessLog(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.AccessLog(mux.Vars(r)["name"])
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	if entries == nil {
		entries = []AccessEntry{}
	}
	respondJSON(w, http.StatusOK, entries)
}

func (h *Handlers) Sweep(w http.ResponseWriter, r *http.Request) {
	swept := h.svc.Sweep(time.Now().UTC())
	if swept == nil {
		swept = []string{}
	}
	h.logger.Info().Int("swept", len(swept)).Msg("consent-centre expiry sweep completed")
	respondJSON(w, http.StatusOK, map[string]interface{}{"swept": len(swept), "providers": swept})
}
