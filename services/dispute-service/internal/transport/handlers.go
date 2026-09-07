package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/dispute-service/internal/domain"
	"github.com/nexora/nexora/services/dispute-service/internal/service"
)

// Handlers exposes the dispute API.
type Handlers struct {
	disputes *service.DisputeService
	logger   zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(disputes *service.DisputeService, logger zerolog.Logger) *Handlers {
	return &Handlers{disputes: disputes, logger: logger}
}

// RegisterRoutes wires the routes. Resolve + tick are internal (internal
// token callers only; the auth middleware strips X-User-ID for them).
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/disputes", h.Create).Methods("POST")
	router.HandleFunc("/v1/disputes", h.List).Methods("GET")
	router.HandleFunc("/v1/disputes/{id}", h.Get).Methods("GET")
	router.HandleFunc("/v1/disputes/{id}/evidence", h.AddEvidence).Methods("POST")
	router.HandleFunc("/v1/disputes/{id}/evidence", h.ListEvidence).Methods("GET")
	router.HandleFunc("/v1/disputes/{id}/events", h.ListEvents).Methods("GET")
	router.HandleFunc("/v1/disputes/{id}/resolve", h.Resolve).Methods("POST")
	router.HandleFunc("/v1/disputes/tick", h.Tick).Methods("POST")
	router.HandleFunc("/v1/disputes/health", h.Health).Methods("GET")
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

func writeDomainError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrCaseNotFound:
		respondError(w, http.StatusNotFound, err.Error())
	case domain.ErrNotOwner:
		respondError(w, http.StatusForbidden, err.Error())
	case domain.ErrDuplicateDispute:
		respondError(w, http.StatusConflict, err.Error())
	case domain.ErrCaseNotOpen, domain.ErrInvalidEvidence:
		respondError(w, http.StatusBadRequest, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req domain.CreateDisputeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.disputes.CreateDispute(r.Context(), userID, &req)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cases, err := h.disputes.ListCases(r.Context(), userID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cases == nil {
		cases = make([]*domain.DisputeCase, 0)
	}
	respondJSON(w, http.StatusOK, cases)
}

func (h *Handlers) Get(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	caseID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid case ID")
		return
	}
	c, err := h.disputes.GetCase(r.Context(), userID, caseID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handlers) AddEvidence(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	caseID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid case ID")
		return
	}
	var ev domain.Evidence
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.disputes.AddEvidence(r.Context(), userID, caseID, &ev)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

func (h *Handlers) ListEvidence(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	caseID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid case ID")
		return
	}
	evidence, err := h.disputes.ListEvidence(r.Context(), userID, caseID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if evidence == nil {
		evidence = make([]*domain.Evidence, 0)
	}
	respondJSON(w, http.StatusOK, evidence)
}

func (h *Handlers) ListEvents(w http.ResponseWriter, r *http.Request) {
	userID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	caseID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid case ID")
		return
	}
	events, err := h.disputes.ListEvents(r.Context(), userID, caseID)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if events == nil {
		events = make([]*domain.CaseEvent, 0)
	}
	respondJSON(w, http.StatusOK, events)
}

// Resolve is an internal endpoint (ops console / reconciliation worker).
func (h *Handlers) Resolve(w http.ResponseWriter, r *http.Request) {
	caseID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid case ID")
		return
	}
	var req struct {
		Resolution string `json:"resolution"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actor := "analyst"
	if uid, ok := callerID(r); ok {
		actor = "user:" + uid.String()
	}
	c, err := h.disputes.ResolveDispute(r.Context(), actor, caseID, domain.Resolution(req.Resolution), req.Note)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, c)
}

// Tick drives timeout progression (compose schedules it; manual via curl).
func (h *Handlers) Tick(w http.ResponseWriter, r *http.Request) {
	advanced, err := h.disputes.ProcessDeadlines(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]int{"advanced": advanced})
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
