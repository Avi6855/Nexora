package docintel

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"

	shared "github.com/nexora/nexora/shared/docintel"
)

// Handlers exposes the document-intelligence API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterDocIntelRoutes mounts /v1/documents routes.
func (h *Handlers) RegisterDocIntelRoutes(router *mux.Router) {
	router.HandleFunc("/v1/documents/ingest", h.ingest).Methods("POST")
	router.HandleFunc("/v1/documents/search", h.search).Methods("POST")
	router.HandleFunc("/v1/documents/totals", h.totals).Methods("GET")
	router.HandleFunc("/v1/documents/{id}/classification", h.classification).Methods("GET")
}

// RegisterDocIntelRoutes is the package-level helper used by cmd/main.go.
func RegisterDocIntelRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterDocIntelRoutes(router)
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
	default:
		return http.StatusBadRequest
	}
}

func (h *Handlers) ingest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind        string `json:"kind"`
		Filename    string `json:"filename"`
		ContentText string `json:"content_text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	doc, err := h.svc.Ingest(req.Kind, req.Filename, req.ContentText)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, doc)
}

func (h *Handlers) classification(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "document id is required")
		return
	}
	c, err := h.svc.Classify(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, c)
}

func (h *Handlers) search(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text           string `json:"text"`
		Kind           string `json:"kind"`
		MinAmountMinor int64  `json:"min_amount_minor"`
		Year           int    `json:"year"`
		TaxYear        int    `json:"tax_year"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.svc.Search(shared.Query{
		Text:           req.Text,
		Kind:           req.Kind,
		MinAmountMinor: req.MinAmountMinor,
		Year:           req.Year,
		TaxYear:        req.TaxYear,
	})
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

func (h *Handlers) totals(w http.ResponseWriter, r *http.Request) {
	if kind := r.URL.Query().Get("kind"); kind != "" {
		t, err := h.svc.TotalByKind(kind)
		if err != nil {
			respondError(w, statusFor(err), err.Error())
			return
		}
		respondJSON(w, http.StatusOK, t)
		return
	}
	respondJSON(w, http.StatusOK, h.svc.Totals())
}
