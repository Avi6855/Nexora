package mldata

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/mldata"
)

// Handlers serves the /v1/mldata API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds mldata handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the mldata routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/mldata/graph/edges", h.AddEdge).Methods("POST")
	router.HandleFunc("/v1/mldata/impact/simulate", h.SimulateImpact).Methods("GET")
	router.HandleFunc("/v1/mldata/freshness/sla", h.SetSLA).Methods("PUT")
	router.HandleFunc("/v1/mldata/freshness/check", h.CheckFreshness).Methods("POST")
	router.HandleFunc("/v1/mldata/drift/baselines", h.SetBaseline).Methods("POST")
	router.HandleFunc("/v1/mldata/drift/check", h.CheckDrift).Methods("POST")
	router.HandleFunc("/v1/mldata/models/register", h.RegisterModel).Methods("POST")
	router.HandleFunc("/v1/mldata/models/{id}/verify-deps", h.VerifyDeps).Methods("POST")
	router.HandleFunc("/v1/mldata/models/rollback-check", h.RollbackCheck).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type edgeRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

func (h *Handlers) AddEdge(w http.ResponseWriter, r *http.Request) {
	var req edgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.From == "" || req.To == "" {
		respondError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if err := h.svc.AddEdge(req.From, req.To, req.Kind); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"from": req.From, "to": req.To})
}

func (h *Handlers) SimulateImpact(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	if node == "" {
		respondError(w, http.StatusBadRequest, "node query param is required")
		return
	}
	imp, err := h.svc.SimulateImpact(node)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, imp)
}

type slaRequest struct {
	Feature  string `json:"feature"`
	MaxAgeMs int64  `json:"max_age_ms"`
}

func (h *Handlers) SetSLA(w http.ResponseWriter, r *http.Request) {
	var req slaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Feature == "" || req.MaxAgeMs <= 0 {
		respondError(w, http.StatusBadRequest, "feature and positive max_age_ms are required")
		return
	}
	if err := h.svc.SetSLA(req.Feature, time.Duration(req.MaxAgeMs)*time.Millisecond); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "recorded", "feature": req.Feature})
}

type freshnessCheckRequest struct {
	Feature string `json:"feature"`
	AgeMs   int64  `json:"age_ms"`
}

func (h *Handlers) CheckFreshness(w http.ResponseWriter, r *http.Request) {
	var req freshnessCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Feature == "" || req.AgeMs < 0 {
		respondError(w, http.StatusBadRequest, "feature and non-negative age_ms are required")
		return
	}
	res, err := h.svc.CheckFreshness(req.Feature, time.Duration(req.AgeMs)*time.Millisecond)
	if err != nil {
		if errors.Is(err, shared.ErrUnknownFeature) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

type baselineRequest struct {
	Key    string  `json:"key"`
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
}

func (h *Handlers) SetBaseline(w http.ResponseWriter, r *http.Request) {
	var req baselineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Key == "" {
		respondError(w, http.StatusBadRequest, "key is required")
		return
	}
	if err := h.svc.SetBaseline(req.Key, shared.Distribution{Median: req.Median, P95: req.P95}); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"status": "recorded", "key": req.Key})
}

type driftCheckRequest struct {
	Key    string  `json:"key"`
	Median float64 `json:"median"`
	P95    float64 `json:"p95"`
}

func (h *Handlers) CheckDrift(w http.ResponseWriter, r *http.Request) {
	var req driftCheckRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Key == "" {
		respondError(w, http.StatusBadRequest, "key is required")
		return
	}
	res, err := h.svc.CheckDrift(req.Key, shared.Distribution{Median: req.Median, P95: req.P95})
	if err != nil {
		if errors.Is(err, shared.ErrNoBaseline) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

func (h *Handlers) RegisterModel(w http.ResponseWriter, r *http.Request) {
	var req shared.ModelDeps
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" {
		respondError(w, http.StatusBadRequest, "model id is required")
		return
	}
	if err := h.svc.RegisterModel(req); err != nil {
		if errors.Is(err, shared.ErrModelExists) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"id": req.ID})
}

type verifyRequest struct {
	DatasetHealth map[string]bool `json:"dataset_health"`
}

func (h *Handlers) VerifyDeps(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "model id is required")
		return
	}
	var req verifyRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	res, err := h.svc.VerifyDeps(id, req.DatasetHealth)
	if err != nil {
		if errors.Is(err, shared.ErrUnknownModel) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

type rollbackRequest struct {
	Expected map[string]string `json:"expected"`
	Current  map[string]string `json:"current"`
}

func (h *Handlers) RollbackCheck(w http.ResponseWriter, r *http.Request) {
	var req rollbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Expected) == 0 || len(req.Current) == 0 {
		respondError(w, http.StatusBadRequest, "expected and current schemas are required")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.CheckRollback(req.Expected, req.Current))
}
