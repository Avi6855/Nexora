package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/consent-service/internal/domain"
	"github.com/nexora/nexora/services/consent-service/internal/service"
)

// Handlers exposes the consent API.
type Handlers struct {
	consent *service.ConsentService
	logger  zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(consent *service.ConsentService, logger zerolog.Logger) *Handlers {
	return &Handlers{consent: consent, logger: logger}
}

// RegisterRoutes wires the routes. /evaluate is internal-only in practice
// (the middleware strips user identity for internal-token callers).
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/consent/grants", h.CreateGrant).Methods("POST")
	router.HandleFunc("/v1/consent/grants", h.ListGrants).Methods("GET")
	router.HandleFunc("/v1/consent/grants/{id}/revoke", h.RevokeGrant).Methods("POST")
	router.HandleFunc("/v1/consent/grants/{id}/audit", h.ListAudit).Methods("GET")
	router.HandleFunc("/v1/consent/evaluate", h.Evaluate).Methods("POST")
	router.HandleFunc("/v1/consent/health", h.Health).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func callerID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.Header.Get("X-User-ID"))
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handlers) CreateGrant(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req domain.CreateGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	grant, err := h.consent.CreateGrant(r.Context(), ownerID, &req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, grant)
}

func (h *Handlers) ListGrants(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	grants, err := h.consent.ListGrants(r.Context(), ownerID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if grants == nil {
		grants = make([]*domain.DelegationGrant, 0)
	}
	respondJSON(w, http.StatusOK, grants)
}

func (h *Handlers) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	grantID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid grant ID")
		return
	}
	if err := h.consent.RevokeGrant(r.Context(), ownerID, grantID); err != nil {
		if err == domain.ErrGrantNotFound {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if err == domain.ErrNotOwner {
			respondError(w, http.StatusForbidden, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (h *Handlers) ListAudit(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	grantID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid grant ID")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	records, err := h.consent.ListAudit(r.Context(), ownerID, grantID, limit)
	if err != nil {
		if err == domain.ErrNotOwner {
			respondError(w, http.StatusForbidden, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if records == nil {
		records = make([]*domain.AuditRecord, 0)
	}
	respondJSON(w, http.StatusOK, records)
}

func (h *Handlers) Evaluate(w http.ResponseWriter, r *http.Request) {
	var req service.EvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.GrantID == uuid.Nil || req.OwnerUserID == uuid.Nil || req.Scope == "" {
		respondError(w, http.StatusBadRequest, "grant_id, owner_user_id and scope are required")
		return
	}
	resp, err := h.consent.Evaluate(r.Context(), &req)
	if err != nil {
		if err == domain.ErrGrantNotFound {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
