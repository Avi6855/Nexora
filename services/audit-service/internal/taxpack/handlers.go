package taxpack

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// Handlers exposes the tax-pack API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterTaxPackRoutes mounts /v1/tax routes.
func (h *Handlers) RegisterTaxPackRoutes(router *mux.Router) {
	router.HandleFunc("/v1/tax/entries", h.postEntry).Methods("POST")
	router.HandleFunc("/v1/tax/packs/build", h.build).Methods("POST")
	router.HandleFunc("/v1/tax/packs/{year}/finalize", h.finalize).Methods("POST")
	router.HandleFunc("/v1/tax/packs/{year}.json", h.renderJSON).Methods("GET")
	router.HandleFunc("/v1/tax/packs/{year}.csv", h.renderCSV).Methods("GET")
	router.HandleFunc("/v1/tax/packs/{year}/manifest", h.manifest).Methods("GET")
}

// RegisterTaxPackRoutes is the package-level helper used by cmd/main.go.
func RegisterTaxPackRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterTaxPackRoutes(router)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrFinalized):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func yearOf(r *http.Request) (int, error) {
	year, err := strconv.Atoi(mux.Vars(r)["year"])
	if err != nil {
		return 0, err
	}
	return year, nil
}

func (h *Handlers) postEntry(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Category    string `json:"category"`
		AmountMinor int64  `json:"amount_minor"`
		Amount      int64  `json:"amount"`
		Date        string `json:"date"`
		DocID       string `json:"doc_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Accept amount_minor (canonical) or amount for compatibility.
	amount := req.AmountMinor
	if amount == 0 {
		amount = req.Amount
	}
	date, err := ParseDate(req.Date)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	e, err := h.svc.PostEntry(req.Category, amount, date, req.DocID)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, e)
}

func (h *Handlers) build(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Year int `json:"year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p, err := h.svc.BuildPack(req.Year)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, p)
}

func (h *Handlers) finalize(w http.ResponseWriter, r *http.Request) {
	year, err := yearOf(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "year must be an integer tax-year start")
		return
	}
	p, err := h.svc.Finalize(year)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}

func (h *Handlers) renderJSON(w http.ResponseWriter, r *http.Request) {
	year, err := yearOf(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "year must be an integer tax-year start")
		return
	}
	raw, err := h.svc.RenderJSON(year)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (h *Handlers) renderCSV(w http.ResponseWriter, r *http.Request) {
	year, err := yearOf(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "year must be an integer tax-year start")
		return
	}
	out, err := h.svc.RenderCSV(year)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}

func (h *Handlers) manifest(w http.ResponseWriter, r *http.Request) {
	year, err := yearOf(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "year must be an integer tax-year start")
		return
	}
	m, err := h.svc.Manifest(year)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, m)
}
