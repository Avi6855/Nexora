package fxtrack

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/fxtrack"
	"github.com/rs/zerolog"
)

// Handlers exposes international payment tracking under /v1/fx.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers returns fx handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all fx tracking routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/fx/transfers", h.StartTransfer).Methods("POST")
	router.HandleFunc("/v1/fx/transfers/{id}/advance", h.Advance).Methods("POST")
	router.HandleFunc("/v1/fx/transfers/{id}/eta", h.ETA).Methods("GET")
	router.HandleFunc("/v1/fx/transfers/{id}/timeline", h.Timeline).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeFxError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fxtrack.ErrTransferNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, fxtrack.ErrTransferExists):
		respondError(w, http.StatusConflict, err.Error())
	case errors.Is(err, fxtrack.ErrTransferComplete):
		respondError(w, http.StatusConflict, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

type startTransferRequest struct {
	ID       string `json:"id,omitempty"`
	Corridor string `json:"corridor"`
	Amount   int64  `json:"amount_minor"`
}

// StartTransfer begins tracking a corridor transfer.
func (h *Handlers) StartTransfer(w http.ResponseWriter, r *http.Request) {
	var req startTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Corridor == "" {
		respondError(w, http.StatusBadRequest, "corridor is required")
		return
	}
	tr, err := h.svc.StartTransfer(req.ID, req.Corridor, req.Amount, time.Now().UTC())
	if err != nil {
		writeFxError(w, err)
		return
	}
	h.logger.Info().Str("transfer_id", tr.ID).Str("corridor", tr.Corridor).Msg("fx transfer tracking started")
	respondJSON(w, http.StatusCreated, tr)
}

// Advance moves the transfer one corridor stage forward.
func (h *Handlers) Advance(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}
	tr, err := h.svc.Advance(id, time.Now().UTC())
	if err != nil {
		writeFxError(w, err)
		return
	}
	h.logger.Info().Str("transfer_id", id).Str("stage", string(tr.CurrentStage)).Msg("fx transfer advanced")
	respondJSON(w, http.StatusOK, tr)
}

// ETA returns the p50/p95 ETA window plus delay attribution.
func (h *Handlers) ETA(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}
	now := time.Now().UTC()
	earliest, latest, err := h.svc.ETAWindow(id, now)
	if err != nil {
		writeFxError(w, err)
		return
	}
	delay, err := h.svc.DelayedAt(id, now)
	if err != nil {
		writeFxError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"transfer_id": id,
		"earliest":    earliest.UTC().Format(time.RFC3339),
		"latest":      latest.UTC().Format(time.RFC3339),
		"delay":       delay,
	})
}

// Timeline returns the stage visit history.
func (h *Handlers) Timeline(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid transfer ID")
		return
	}
	tl, err := h.svc.Timeline(id)
	if err != nil {
		writeFxError(w, err)
		return
	}
	if tl == nil {
		tl = make([]fxtrack.StageVisit, 0)
	}
	respondJSON(w, http.StatusOK, tl)
}
