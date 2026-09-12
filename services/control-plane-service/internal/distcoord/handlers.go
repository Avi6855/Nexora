package distcoord

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/distcoord"
)

// Handlers serves the /v1/distcoord API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds distcoord handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the distcoord routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/distcoord/correlations/issue", h.IssueCorrelation).Methods("POST")
	router.HandleFunc("/v1/distcoord/correlations/validate", h.ValidateChain).Methods("POST")
	router.HandleFunc("/v1/distcoord/causality/edges", h.AddCausalEdge).Methods("POST")
	router.HandleFunc("/v1/distcoord/causality/why", h.WhyHappened).Methods("GET")
	router.HandleFunc("/v1/distcoord/clocks/samples", h.RecordClockSample).Methods("POST")
	router.HandleFunc("/v1/distcoord/clocks/policy", h.GetClockPolicy).Methods("GET")
	router.HandleFunc("/v1/distcoord/hlc/issue", h.HLCIssue).Methods("POST")
	router.HandleFunc("/v1/distcoord/hlc/receive", h.HLCReceive).Methods("POST")
	router.HandleFunc("/v1/distcoord/locks/acquire", h.LockAcquire).Methods("POST")
	router.HandleFunc("/v1/distcoord/locks/release", h.LockRelease).Methods("POST")
	router.HandleFunc("/v1/distcoord/locks/stuck", h.StuckLocks).Methods("GET")
	router.HandleFunc("/v1/distcoord/locks/stats", h.LockStats).Methods("GET")
	router.HandleFunc("/v1/distcoord/contention/advise", h.AdviseContention).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type issueRequest struct {
	Action string `json:"action"`
}

func (h *Handlers) IssueCorrelation(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	c, err := h.svc.IssueCorrelation(req.Action)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, c)
}

type validateRequest struct {
	Correlations []shared.Correlation `json:"correlations"`
	Parent       *shared.Correlation  `json:"parent"`
	Action       string               `json:"action"`
}

func (h *Handlers) ValidateChain(w http.ResponseWriter, r *http.Request) {
	var req validateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Propagate helper: when parent+action are given, derive the child and
	// validate the extended chain (child causation = parent id).
	chain := req.Correlations
	if req.Parent != nil && req.Action != "" {
		child, err := h.svc.PropagateCorrelation(*req.Parent, req.Action)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		chain = append(chain, child)
		respondJSON(w, http.StatusCreated, child)
		return
	}
	if len(chain) == 0 {
		respondError(w, http.StatusBadRequest, "correlations are required")
		return
	}
	if err := h.svc.ValidateChain(chain); err != nil {
		respondJSON(w, http.StatusConflict, map[string]interface{}{"valid": false, "error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"valid": true})
}

type edgeRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (h *Handlers) AddCausalEdge(w http.ResponseWriter, r *http.Request) {
	var req edgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.From == "" || req.To == "" {
		respondError(w, http.StatusBadRequest, "from and to are required")
		return
	}
	if err := h.svc.AddCausalEdge(req.From, req.To); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"from": req.From, "to": req.To})
}

func (h *Handlers) WhyHappened(w http.ResponseWriter, r *http.Request) {
	node := r.URL.Query().Get("node")
	if node == "" {
		respondError(w, http.StatusBadRequest, "node query param is required")
		return
	}
	chain, err := h.svc.WhyHappened(node)
	if err != nil {
		if errors.Is(err, shared.ErrUnknownNode) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"node": node, "chain": chain})
}

type clockSampleRequest struct {
	Node   string `json:"node"`
	WallMs int64  `json:"wall_ms"`
}

func (h *Handlers) RecordClockSample(w http.ResponseWriter, r *http.Request) {
	var req clockSampleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RecordClockSample(req.Node, req.WallMs); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"status": "recorded", "node": req.Node})
}

func (h *Handlers) GetClockPolicy(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"policy": h.svc.ClockPolicy(),
		"matrix": h.svc.SkewMatrix(),
	})
}

type hlcIssueRequest struct {
	WallMs int64 `json:"wall_ms"`
}

func (h *Handlers) HLCIssue(w http.ResponseWriter, r *http.Request) {
	var req hlcIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.HLCIssue(req.WallMs))
}

type hlcReceiveRequest struct {
	Remote shared.Timestamp `json:"remote"`
	WallMs int64            `json:"wall_ms"`
}

func (h *Handlers) HLCReceive(w http.ResponseWriter, r *http.Request) {
	var req hlcReceiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.HLCReceive(req.Remote, req.WallMs))
}

type lockAcquireRequest struct {
	Resource string `json:"resource"`
	Owner    string `json:"owner"`
	TTLMs    int64  `json:"ttl_ms"`
}

func (h *Handlers) LockAcquire(w http.ResponseWriter, r *http.Request) {
	var req lockAcquireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	acquired, err := h.svc.LockAcquire(req.Resource, req.Owner, req.TTLMs, time.Now().UTC())
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if acquired {
		respondJSON(w, http.StatusCreated, map[string]interface{}{"resource": req.Resource, "acquired": true})
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"resource": req.Resource, "acquired": false, "queued": true})
}

type lockReleaseRequest struct {
	Resource string `json:"resource"`
	Owner    string `json:"owner"`
}

func (h *Handlers) LockRelease(w http.ResponseWriter, r *http.Request) {
	var req lockReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.LockRelease(req.Resource, req.Owner); err != nil {
		switch {
		case errors.Is(err, shared.ErrLockNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, shared.ErrLockNotOwner):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "released", "resource": req.Resource})
}

func (h *Handlers) StuckLocks(w http.ResponseWriter, r *http.Request) {
	k := 2.0
	if raw := r.URL.Query().Get("k"); raw != "" {
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || parsed <= 0 {
			respondError(w, http.StatusBadRequest, "k must be a positive number")
			return
		}
		k = parsed
	}
	stuck := h.svc.StuckLocks(k, time.Now().UTC())
	if stuck == nil {
		stuck = []shared.LockInfo{}
	}
	respondJSON(w, http.StatusOK, stuck)
}

func (h *Handlers) LockStats(w http.ResponseWriter, r *http.Request) {
	stats := h.svc.LockStats()
	if stats == nil {
		stats = []shared.ResourceStats{}
	}
	respondJSON(w, http.StatusOK, stats)
}

type adviseRequest struct {
	Edges []shared.WaitEdge `json:"edges"`
}

func (h *Handlers) AdviseContention(w http.ResponseWriter, r *http.Request) {
	var req adviseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	adv := h.svc.AdviseContention(req.Edges)
	if adv == nil {
		adv = []shared.ContentionAdvice{}
	}
	respondJSON(w, http.StatusOK, adv)
}
