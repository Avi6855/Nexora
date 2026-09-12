package failover

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/failover"
)

// Handlers serves the /v1/failover API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds failover handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the failover routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/failover/regions", h.RegisterRegion).Methods("POST")
	router.HandleFunc("/v1/failover/regions/{name}/health", h.ReportHealth).Methods("POST")
	router.HandleFunc("/v1/failover/route", h.GetRoute).Methods("GET")
	router.HandleFunc("/v1/failover/fence/advance", h.AdvanceFence).Methods("POST")
	router.HandleFunc("/v1/failover/ops/execute", h.ExecuteOp).Methods("POST")
	router.HandleFunc("/v1/failover/recovery/diff", h.RecoveryDiff).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type registerRegionRequest struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

func (h *Handlers) RegisterRegion(w http.ResponseWriter, r *http.Request) {
	var req registerRegionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Role == "" {
		respondError(w, http.StatusBadRequest, "name and role are required")
		return
	}
	if err := h.svc.RegisterRegion(req.Name, shared.Role(req.Role)); err != nil {
		if errors.Is(err, shared.ErrRegionExists) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"name": req.Name, "role": req.Role})
}

type healthRequest struct {
	Healthy bool  `json:"healthy"`
	LagMs   int64 `json:"lag_ms"`
}

func (h *Handlers) ReportHealth(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	if name == "" {
		respondError(w, http.StatusBadRequest, "region name is required")
		return
	}
	var req healthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.ReportHealth(name, req.Healthy, req.LagMs); err != nil {
		if errors.Is(err, shared.ErrUnknownRegion) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "recorded", "region": name})
}

func (h *Handlers) GetRoute(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.Route())
}

func (h *Handlers) AdvanceFence(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]uint64{"epoch": h.svc.AdvanceEpoch()})
}

type executeOpRequest struct {
	Region string `json:"region"`
	OpID   string `json:"op_id"`
	Epoch  uint64 `json:"epoch"`
}

func (h *Handlers) ExecuteOp(w http.ResponseWriter, r *http.Request) {
	var req executeOpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Region == "" || req.OpID == "" {
		respondError(w, http.StatusBadRequest, "region and op_id are required")
		return
	}
	executed, err := h.svc.ExecuteOp(req.Region, req.OpID, req.Epoch)
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrUnknownRegion):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrFenced):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if executed {
		respondJSON(w, http.StatusCreated, map[string]interface{}{"op_id": req.OpID, "executed": true, "epoch": req.Epoch})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"op_id": req.OpID, "executed": false, "deduped": true})
}

func (h *Handlers) RecoveryDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	primary, standby := q.Get("primary"), q.Get("standby")
	if primary == "" || standby == "" {
		respondError(w, http.StatusBadRequest, "primary and standby query params are required")
		return
	}
	rep, err := h.svc.RecoveryDiff(primary, standby)
	if err != nil {
		if errors.Is(err, shared.ErrUnknownRegion) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rep)
}
