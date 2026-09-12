package schemes

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	sharedschemes "github.com/nexora/nexora/shared/schemes"
)

// Handlers exposes scheme simulation over /v1/schemes.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds scheme handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts /v1/schemes routes on the shared mux router.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/schemes/cert-runs", h.RunCert).Methods("POST")
	router.HandleFunc("/v1/schemes/cert-runs/{id}", h.GetCertRun).Methods("GET")
	router.HandleFunc("/v1/schemes/route", h.Route).Methods("POST")
	router.HandleFunc("/v1/schemes/settlement/next", h.NextSettlement).Methods("GET")
	router.HandleFunc("/v1/schemes/fees/attribute", h.AttributeFee).Methods("POST")
	router.HandleFunc("/v1/schemes/fees/summary", h.FeeSummary).Methods("GET")
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
	case errors.Is(err, sharedschemes.ErrCertRunNotFound),
		errors.Is(err, sharedschemes.ErrSchemeNotFound),
		errors.Is(err, sharedschemes.ErrNoRoute),
		errors.Is(err, sharedschemes.ErrRailNotFound),
		errors.Is(err, sharedschemes.ErrCertCaseNotFound):
		return http.StatusNotFound
	case errors.Is(err, sharedschemes.ErrRailExists):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

type runCertRequest struct {
	Cases []CertCaseInput `json:"cases,omitempty"`
}

func (h *Handlers) RunCert(w http.ResponseWriter, r *http.Request) {
	var req runCertRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	rep, err := h.svc.RunCert(req.Cases)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("run_id", rep.RunID).Msg("scheme cert run via API")
	respondJSON(w, http.StatusCreated, rep)
}

func (h *Handlers) GetCertRun(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "run id is required")
		return
	}
	rep, err := h.svc.GetCertRun(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rep)
}

type routeRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Destination string `json:"destination"`
}

func (h *Handlers) Route(w http.ResponseWriter, r *http.Request) {
	var req routeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	opts, err := h.svc.Route(req.AmountMinor, req.Currency, req.Destination)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"options": opts})
}

func (h *Handlers) NextSettlement(w http.ResponseWriter, r *http.Request) {
	scheme := r.URL.Query().Get("scheme")
	if scheme == "" {
		respondError(w, http.StatusBadRequest, "scheme query param is required")
		return
	}
	ts := time.Now().UTC()
	if raw := r.URL.Query().Get("ts"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			respondError(w, http.StatusBadRequest, "ts must be RFC3339")
			return
		}
		ts = parsed
	}
	next, err := h.svc.NextSettlement(scheme, ts)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"scheme": scheme, "at": ts.Format(time.RFC3339), "next_settlement": next.Format(time.RFC3339),
	})
}

func (h *Handlers) AttributeFee(w http.ResponseWriter, r *http.Request) {
	var b sharedschemes.FeeBreakdown
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tot, err := h.svc.AttributeFee(b)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	h.logger.Info().Str("tx_id", tot.TxID).Msg("scheme fee attributed via API")
	respondJSON(w, http.StatusCreated, tot)
}

func (h *Handlers) FeeSummary(w http.ResponseWriter, r *http.Request) {
	scheme := r.URL.Query().Get("scheme")
	if scheme == "" {
		respondJSON(w, http.StatusOK, map[string]interface{}{"summaries": h.svc.FeeSummaryAll()})
		return
	}
	respondJSON(w, http.StatusOK, h.svc.FeeSummary(scheme))
}
