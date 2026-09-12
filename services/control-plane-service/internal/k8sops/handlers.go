package k8sops

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/k8sops"
)

// Handlers serves the /v1/k8sops API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds k8sops handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the k8sops routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/k8sops/drain-safety", h.DrainSafety).Methods("POST")
	router.HandleFunc("/v1/k8sops/placement/advise", h.PlacementAdvise).Methods("POST")
	router.HandleFunc("/v1/k8sops/fragmentation/scan", h.FragmentationScan).Methods("POST")
	router.HandleFunc("/v1/k8sops/rightsizing/advise", h.RightsizingAdvise).Methods("POST")
	router.HandleFunc("/v1/k8sops/upgrade/scan", h.UpgradeScan).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) DrainSafety(w http.ResponseWriter, r *http.Request) {
	var req shared.DrainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d, err := h.svc.CheckDrainSafety(req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, d)
}

type placementRequest struct {
	Nodes []shared.NodeCandidate `json:"nodes"`
	Needs shared.WorkloadNeeds   `json:"needs"`
}

func (h *Handlers) PlacementAdvise(w http.ResponseWriter, r *http.Request) {
	var req placementRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ranked, err := h.svc.AdvisePlacement(req.Nodes, req.Needs)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, ranked)
}

type fragmentationRequest struct {
	Nodes []shared.NodeUsage `json:"nodes"`
}

func (h *Handlers) FragmentationScan(w http.ResponseWriter, r *http.Request) {
	var req fragmentationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.ScanFragmentation(req.Nodes))
}

func (h *Handlers) RightsizingAdvise(w http.ResponseWriter, r *http.Request) {
	var req shared.SizingInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	adv, err := h.svc.AdviseRightsizing(req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, adv)
}

type upgradeRequest struct {
	Components   []shared.Component   `json:"components"`
	Deprecations []shared.Deprecation `json:"deprecations"`
	Target       string               `json:"target"`
}

func (h *Handlers) UpgradeScan(w http.ResponseWriter, r *http.Request) {
	var req upgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rep, err := h.svc.ScanUpgrade(req.Components, req.Deprecations, req.Target)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rep)
}
