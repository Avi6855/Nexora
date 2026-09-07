package transport

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nexora/nexora/services/policy-service/internal/domain"
	"github.com/nexora/nexora/services/policy-service/internal/service"
)

// TimeTravelHandlers expose the time-travel compliance engine.
type TimeTravelHandlers struct {
	tt *service.TimeTravelService
}

func NewTimeTravelHandlers(tt *service.TimeTravelService) *TimeTravelHandlers {
	return &TimeTravelHandlers{tt: tt}
}

// RegisterRoutes wires the /v1/compliance/time-travel API.
func (h *TimeTravelHandlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/compliance/time-travel", h.TimeTravel).Methods("POST")
	router.HandleFunc("/v1/compliance/decisions/{payment_id}", h.GetDecision).Methods("GET")
	router.HandleFunc("/v1/compliance/snapshots", h.CaptureSnapshot).Methods("POST")
}

func (h *TimeTravelHandlers) TimeTravel(w http.ResponseWriter, r *http.Request) {
	var req domain.TimeTravelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PaymentID == "" {
		respondError(w, http.StatusBadRequest, "payment_id is required")
		return
	}
	if req.AsOf.IsZero() {
		respondError(w, http.StatusBadRequest, "as_of is required (RFC3339)")
		return
	}
	if req.AsOf.After(time.Now().UTC()) {
		respondError(w, http.StatusBadRequest, "as_of may not be in the future")
		return
	}
	eval, err := h.tt.EvaluateAtTime(r.Context(), req.PaymentID, req.AsOf, domain.PaymentContext{
		PaymentID: uuid.MustParse(fallbackUUID(req.PaymentID)),
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, domain.TimeTravelResponse{Evaluation: *eval})
}

func (h *TimeTravelHandlers) GetDecision(w http.ResponseWriter, r *http.Request) {
	rec, err := h.tt.GetHistoricalDecision(r.Context(), mux.Vars(r)["payment_id"])
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rec)
}

func (h *TimeTravelHandlers) CaptureSnapshot(w http.ResponseWriter, r *http.Request) {
	var req domain.CaptureSnapshotRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	snap, err := h.tt.CaptureSnapshot(r.Context(), req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, snap)
}

func fallbackUUID(s string) string {
	if _, err := uuid.Parse(s); err == nil {
		return s
	}
	return "00000000-0000-0000-0000-000000000000"
}
