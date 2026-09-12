package resilience

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
)

// Handlers exposes the resilience-scenario API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterResilienceRoutes mounts /v1/resilience routes.
func (h *Handlers) RegisterResilienceRoutes(router *mux.Router) {
	router.HandleFunc("/v1/resilience/scenarios", h.create).Methods("POST")
	router.HandleFunc("/v1/resilience/scenarios", h.list).Methods("GET")
	router.HandleFunc("/v1/resilience/scenarios/{id}/run", h.run).Methods("POST")
}

// RegisterResilienceRoutes is the package-level helper used by cmd/main.go.
func RegisterResilienceRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterResilienceRoutes(router)
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

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AccountID       string `json:"account_id"`
		Name            string `json:"name"`
		JobLoss         bool   `json:"job_loss"`
		RentHikeMonthly int64  `json:"rent_hike_monthly"`
		IncomeGapMonths int    `json:"income_gap_months"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sc, err := h.svc.CreateScenario(req.AccountID, req.Name, req.JobLoss, req.RentHikeMonthly, req.IncomeGapMonths)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, sc)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.ListScenarios(r.URL.Query().Get("account_id")))
}

func (h *Handlers) run(w http.ResponseWriter, r *http.Request) {
	var in RunwayInputs
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	out, err := h.svc.RunScenario(mux.Vars(r)["id"], in)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, out)
}
