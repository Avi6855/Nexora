package dataline

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	shared "github.com/nexora/nexora/shared/dataline"
)

// Handlers exposes the data-lineage, privacy and retention APIs.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterDataLineRoutes mounts /v1/data-lineage, /v1/privacy and
// /v1/retention routes.
func (h *Handlers) RegisterDataLineRoutes(router *mux.Router) {
	// Lineage.
	router.HandleFunc("/v1/data-lineage/nodes", h.recordNode).Methods("POST")
	router.HandleFunc("/v1/data-lineage/edges", h.recordEdge).Methods("POST")
	router.HandleFunc("/v1/data-lineage/explain", h.explain).Methods("GET")
	// Privacy.
	router.HandleFunc("/v1/privacy/tokenise", h.tokenise).Methods("POST")
	router.HandleFunc("/v1/privacy/rotate", h.rotate).Methods("POST")
	router.HandleFunc("/v1/privacy/cohorts/count", h.cohortCount).Methods("POST")
	router.HandleFunc("/v1/privacy/audit", h.audit).Methods("GET")
	// Retention.
	router.HandleFunc("/v1/retention/policies", h.putPolicy).Methods("PUT")
	router.HandleFunc("/v1/retention/classify", h.classify).Methods("POST")
	router.HandleFunc("/v1/retention/due", h.due).Methods("GET")
	router.HandleFunc("/v1/retention/transitions", h.applyTransition).Methods("POST")
	router.HandleFunc("/v1/retention/holds", h.placeHold).Methods("POST")
	router.HandleFunc("/v1/retention/holds/{id}", h.releaseHold).Methods("DELETE")
}

// RegisterDataLineRoutes is the package-level helper used by cmd/main.go.
func RegisterDataLineRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterDataLineRoutes(router)
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
	case errors.Is(err, ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// ── lineage ────────────────────────────────────────────────────────────────

func (h *Handlers) recordNode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      string `json:"id"`
		Kind    string `json:"kind"`
		ValueID string `json:"value_id"`
		At      string `json:"at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var at time.Time
	if req.At != "" {
		ts, err := ParseOptionalTime(req.At)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		at = ts
	}
	node, err := h.svc.RecordNode(shared.Node{ID: req.ID, Kind: req.Kind, ValueID: req.ValueID, At: at})
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, node)
}

func (h *Handlers) recordEdge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RecordEdge(req.From, req.To); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"from": req.From, "to": req.To})
}

func (h *Handlers) explain(w http.ResponseWriter, r *http.Request) {
	value := r.URL.Query().Get("value")
	if value == "" {
		respondError(w, http.StatusBadRequest, "value query param is required")
		return
	}
	chain, err := h.svc.Explain(value)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, chain)
}

// ── privacy ────────────────────────────────────────────────────────────────

func (h *Handlers) tokenise(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Purpose string `json:"purpose"`
		Field   string `json:"field"`
		Value   string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tok, err := h.svc.Tokenise(req.Purpose, req.Field, req.Value)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"token": tok})
}

func (h *Handlers) rotate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RotatePurpose(req.Purpose); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "purpose rotated"})
}

func (h *Handlers) cohortCount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Purpose string `json:"purpose"`
		Field   string `json:"field"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	n, err := h.svc.CohortCount(req.Purpose, req.Field)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"purpose": req.Purpose, "field": req.Field, "count": n})
}

func (h *Handlers) audit(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, h.svc.AuditLog())
}

// ── retention ──────────────────────────────────────────────────────────────

func (h *Handlers) putPolicy(w http.ResponseWriter, r *http.Request) {
	var p shared.Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.PutPolicy(p); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, p)
}

func (h *Handlers) classify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID        string `json:"id"`
		Category  string `json:"category"`
		CreatedAt string `json:"created_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var created time.Time
	if req.CreatedAt != "" {
		ts, err := ParseOptionalTime(req.CreatedAt)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		created = ts
	}
	it, err := h.svc.Classify(req.ID, req.Category, created)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, it)
}

func (h *Handlers) due(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("now")
	var now time.Time
	if raw != "" {
		ts, err := ParseOptionalTime(raw)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		now = ts
	}
	due, err := h.svc.DueTransitions(now)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	if due == nil {
		due = []shared.DueTransition{}
	}
	respondJSON(w, http.StatusOK, due)
}

func (h *Handlers) applyTransition(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID  string `json:"id"`
		Now string `json:"now"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	var now time.Time
	if req.Now != "" {
		ts, err := ParseOptionalTime(req.Now)
		if err != nil {
			respondError(w, http.StatusBadRequest, err.Error())
			return
		}
		now = ts
	}
	it, err := h.svc.ApplyTransition(req.ID, now)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, it)
}

func (h *Handlers) placeHold(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemID string `json:"item_id"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hold, err := h.svc.PlaceHold(req.ItemID, req.Reason)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, hold)
}

func (h *Handlers) releaseHold(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.ReleaseHold(id); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "hold released"})
}
