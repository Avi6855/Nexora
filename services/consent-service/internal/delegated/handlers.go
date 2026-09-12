package delegated

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
)

// Handlers exposes the Delegated Permissions API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/delegated.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/delegated/grants", h.CreateGrant).Methods("POST")
	router.HandleFunc("/v1/delegated/grants/{id}/use", h.UseGrant).Methods("POST")
	router.HandleFunc("/v1/delegated/grants/{id}", h.RevokeGrant).Methods("DELETE")
	router.HandleFunc("/v1/delegated/grants/{id}/audit", h.GetAudit).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type grantRequest struct {
	Grantee      string   `json:"grantee"`
	Capabilities []string `json:"capabilities"`
	Start        string   `json:"start,omitempty"`
	End          string   `json:"end"`
	AccountSet   string   `json:"account_set"`
}

func parseWindow(startRaw, endRaw string, now time.Time) (time.Time, time.Time, error) {
	start := now
	if strings.TrimSpace(startRaw) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(startRaw))
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("start must be RFC3339")
		}
		start = t.UTC()
	}
	if strings.TrimSpace(endRaw) == "" {
		return time.Time{}, time.Time{}, errors.New("end is required (RFC3339)")
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(endRaw))
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("end must be RFC3339")
	}
	return start, end.UTC(), nil
}

func (h *Handlers) CreateGrant(w http.ResponseWriter, r *http.Request) {
	var req grantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	now := time.Now().UTC()
	start, end, err := parseWindow(req.Start, req.End, now)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := h.svc.Grant(req.Grantee, req.Capabilities, start, end, req.AccountSet, now)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.logger.Info().
		Str("grant_id", g.ID).
		Str("grantee", g.Grantee).
		Str("account_set", g.AccountSet).
		Msg("delegated grant created")
	respondJSON(w, http.StatusCreated, g)
}

type useRequest struct {
	Capability string `json:"capability"`
	AccountSet string `json:"account_set"`
}

func (h *Handlers) UseGrant(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req useRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Capability) == "" {
		respondError(w, http.StatusBadRequest, "capability is required")
		return
	}
	if strings.TrimSpace(req.AccountSet) == "" {
		respondError(w, http.StatusBadRequest, ErrAccountSetRequired.Error())
		return
	}
	allowed, reason, err := h.svc.Use(id, req.Capability, req.AccountSet, time.Now().UTC())
	if err != nil {
		if errors.Is(err, ErrGrantNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	decision := "DENY"
	if allowed {
		decision = "ALLOW"
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"grant_id":   id,
		"decision":   decision,
		"reason":     reason,
		"capability": normalise(req.Capability),
	})
}

func (h *Handlers) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.Revoke(id, time.Now().UTC()); err != nil {
		switch {
		case errors.Is(err, ErrGrantNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrAlreadyRevoked):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	h.logger.Info().Str("grant_id", id).Msg("delegated grant revoked")
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked", "grant_id": id})
}

func (h *Handlers) GetAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := h.svc.Audit(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	if entries == nil {
		entries = []UsageEntry{}
	}
	respondJSON(w, http.StatusOK, entries)
}
