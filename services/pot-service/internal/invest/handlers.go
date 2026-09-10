package invest

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	sharedinvest "github.com/nexora/nexora/shared/investments"
	"github.com/rs/zerolog"
)

type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/investments/isa/contributions", h.ContributeISA).Methods("POST")
	router.HandleFunc("/v1/investments/isa/allowance", h.RemainingAllowance).Methods("GET")
	router.HandleFunc("/v1/investments/joint-goals", h.CreateJointGoal).Methods("POST")
	router.HandleFunc("/v1/investments/joint-goals/{id}/contribute", h.ContributeJointGoal).Methods("POST")
	router.HandleFunc("/v1/investments/tax-lots/open", h.OpenTaxLots).Methods("POST")
	router.HandleFunc("/v1/investments/tax-lots/sell", h.SellLots).Methods("POST")
	router.HandleFunc("/v1/investments/corporate-actions/apply", h.ApplyAction).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) ContributeISA(w http.ResponseWriter, r *http.Request) {
	var c sharedinvest.Contribution
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.ContributeISA(c); err != nil {
		if errors.Is(err, sharedinvest.ErrAllowanceExceeded) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"message": "contribution admitted"})
}

func (h *Handlers) RemainingAllowance(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("owner")
	if owner == "" {
		respondError(w, http.StatusBadRequest, "owner query param is required")
		return
	}
	taxYear := r.URL.Query().Get("tax_year")
	remaining, err := h.svc.RemainingAllowance(owner, taxYear)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if taxYear == "" {
		taxYear = sharedinvest.TaxYear(time.Now())
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"owner":                  owner,
		"tax_year":               taxYear,
		"remaining_minor":        remaining,
		"annual_allowance_minor": AnnualAllowanceMinor,
	})
}

type createGoalRequest struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	TargetMinor int64    `json:"target_minor"`
	Owners      []string `json:"owners"`
}

func (h *Handlers) CreateJointGoal(w http.ResponseWriter, r *http.Request) {
	var req createGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	g, err := h.svc.CreateJointGoal(req.ID, req.Name, req.TargetMinor, req.Owners)
	if err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, g)
}

type contributeGoalRequest struct {
	OwnerID     string `json:"owner_id"`
	AmountMinor int64  `json:"amount_minor"`
}

func (h *Handlers) ContributeJointGoal(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req contributeGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	g, err := h.svc.ContributeJointGoal(id, req.OwnerID, req.AmountMinor)
	if err != nil {
		if errors.Is(err, ErrGoalNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, sharedinvest.ErrGoalOverfunded) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, g)
}

type openLotsRequest struct {
	Symbol string                 `json:"symbol"`
	Method sharedinvest.LotMethod `json:"method"`
	Lots   []sharedinvest.Lot     `json:"lots"`
}

func (h *Handlers) OpenTaxLots(w http.ResponseWriter, r *http.Request) {
	var req openLotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.OpenTaxLots(req.Symbol, req.Method, req.Lots); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"message": "tax lots opened"})
}

type sellLotsRequest struct {
	Symbol            string `json:"symbol"`
	UnitsMilli        int64  `json:"units_milli"`
	PricePerUnitMilli int64  `json:"price_per_unit_milli"`
}

func (h *Handlers) SellLots(w http.ResponseWriter, r *http.Request) {
	var req sellLotsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Symbol == "" {
		respondError(w, http.StatusBadRequest, "symbol is required")
		return
	}
	res, err := h.svc.SellLots(req.Symbol, req.UnitsMilli, req.PricePerUnitMilli)
	if err != nil {
		if errors.Is(err, ErrEngineNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, sharedinvest.ErrInsufficientUnits) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

type applyActionRequest struct {
	Holding sharedinvest.Holding         `json:"holding"`
	Action  sharedinvest.CorporateAction `json:"action"`
}

func (h *Handlers) ApplyAction(w http.ResponseWriter, r *http.Request) {
	var req applyActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	out, res, err := h.svc.ApplyAction(req.Holding, req.Action)
	if err != nil {
		if errors.Is(err, sharedinvest.ErrUnknownHolding) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, sharedinvest.ErrUnknownAction) {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"holding": out,
		"result":  res,
		"audit":   sharedinvest.ActionAudit(res),
	})
}
