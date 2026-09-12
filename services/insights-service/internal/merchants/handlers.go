package merchants

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	sharedm "github.com/nexora/nexora/shared/merchants"
	"github.com/rs/zerolog"
)

// Handlers exposes the merchant graph under /v1/merchants.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers returns merchant handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all merchant routes. Static routes are registered
// before the {id} route so /subscriptions and /resolve are never captured
// as IDs.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/merchants/observe", h.Observe).Methods("POST")
	router.HandleFunc("/v1/merchants/resolve", h.Resolve).Methods("GET")
	router.HandleFunc("/v1/merchants/aliases", h.AddAlias).Methods("POST")
	router.HandleFunc("/v1/merchants/risk-flags", h.FlagRisk).Methods("POST")
	router.HandleFunc("/v1/merchants/subscriptions", h.Subscriptions).Methods("GET")
	router.HandleFunc("/v1/merchants/{id}/refunds", h.Refunds).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeMerchantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sharedm.ErrMerchantNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

type observeRequest struct {
	Name     string `json:"name"`
	Location string `json:"location,omitempty"`
	Amount   int64  `json:"amount_minor"`
	Refunded bool   `json:"refunded"`
}

// Observe records an alias observation plus its charge.
func (h *Handlers) Observe(w http.ResponseWriter, r *http.Request) {
	var req observeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	n, err := h.svc.Observe(req.Name, req.Location, req.Amount, req.Refunded)
	if err != nil {
		writeMerchantError(w, err)
		return
	}
	h.logger.Info().Str("merchant_id", n.ID).Str("canonical", n.Canonical).Msg("merchant observed")
	respondJSON(w, http.StatusCreated, n)
}

// Resolve follows alias → canonical → brand → category → parent.
func (h *Handlers) Resolve(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		respondError(w, http.StatusBadRequest, "name query parameter is required")
		return
	}
	n, err := h.svc.Resolve(name)
	if err != nil {
		writeMerchantError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, n)
}

type aliasRequest struct {
	MerchantID string `json:"merchant_id"`
	Alias      string `json:"alias"`
}

// AddAlias links a name variant to a canonical node.
func (h *Handlers) AddAlias(w http.ResponseWriter, r *http.Request) {
	var req aliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MerchantID == "" || req.Alias == "" {
		respondError(w, http.StatusBadRequest, "merchant_id and alias are required")
		return
	}
	n, err := h.svc.AddAlias(req.MerchantID, req.Alias)
	if err != nil {
		writeMerchantError(w, err)
		return
	}
	h.logger.Info().Str("merchant_id", n.ID).Str("alias", req.Alias).Msg("merchant alias added")
	respondJSON(w, http.StatusCreated, n)
}

type riskRequest struct {
	MerchantID string `json:"merchant_id"`
	Flag       string `json:"flag"`
}

// FlagRisk attaches a risk flag.
func (h *Handlers) FlagRisk(w http.ResponseWriter, r *http.Request) {
	var req riskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.MerchantID == "" || req.Flag == "" {
		respondError(w, http.StatusBadRequest, "merchant_id and flag are required")
		return
	}
	n, err := h.svc.FlagRisk(req.MerchantID, req.Flag)
	if err != nil {
		writeMerchantError(w, err)
		return
	}
	h.logger.Info().Str("merchant_id", n.ID).Str("flag", req.Flag).Msg("merchant risk flagged")
	respondJSON(w, http.StatusOK, n)
}

// Refunds returns refund-rate stats for one merchant.
func (h *Handlers) Refunds(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid merchant ID")
		return
	}
	stats, err := h.svc.RefundStats(id)
	if err != nil {
		writeMerchantError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, stats)
}

// Subscriptions lists detected recurring merchant charges.
func (h *Handlers) Subscriptions(w http.ResponseWriter, r *http.Request) {
	subs := h.svc.Subscriptions()
	if subs == nil {
		subs = make([]sharedm.Subscription, 0)
	}
	respondJSON(w, http.StatusOK, subs)
}
