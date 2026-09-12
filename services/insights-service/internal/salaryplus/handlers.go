package salaryplus

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
)

// Handlers exposes the raise-plan API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterSalaryPlusRoutes mounts /v1/salary-plus routes.
func (h *Handlers) RegisterSalaryPlusRoutes(router *mux.Router) {
	router.HandleFunc("/v1/salary-plus/raise-plans", h.create).Methods("POST")
	router.HandleFunc("/v1/salary-plus/raise-plans", h.list).Methods("GET")
	router.HandleFunc("/v1/salary-plus/raise-plans/{id}/evaluate", h.evaluate).Methods("POST")
	router.HandleFunc("/v1/salary-plus/raise-plans/{id}", h.remove).Methods("DELETE")
}

// RegisterSalaryPlusRoutes is the package-level helper used by cmd/main.go.
func RegisterSalaryPlusRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterSalaryPlusRoutes(router)
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
		AccountID   string            `json:"account_id"`
		Allocations []RaiseAllocation `json:"allocations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	plan, err := h.svc.CreatePlan(req.AccountID, req.Allocations)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, plan)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.ListPlans(r.URL.Query().Get("account_id")))
}

func (h *Handlers) evaluate(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req struct {
		LastAmount int64 `json:"last_amount"`
		MonthlyAvg int64 `json:"monthly_avg"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ev, err := h.svc.EvaluatePlan(id, req.LastAmount, req.MonthlyAvg)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, ev)
}

func (h *Handlers) remove(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeletePlan(mux.Vars(r)["id"]); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "raise plan deleted"})
}
