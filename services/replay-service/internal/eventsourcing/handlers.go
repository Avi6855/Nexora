package eventsourcing

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
)

// Handlers exposes the event-store + debug-replay API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterEventStoreRoutes mounts /v1/event-store routes.
func (h *Handlers) RegisterEventStoreRoutes(router *mux.Router) {
	router.HandleFunc("/v1/event-store/append", h.append).Methods("POST")
	router.HandleFunc("/v1/event-store/streams/{id}", h.loadStream).Methods("GET")
	router.HandleFunc("/v1/event-store/streams/{id}/project", h.project).Methods("POST")
	router.HandleFunc("/v1/event-store/projectors", h.registerProjector).Methods("POST")
	router.HandleFunc("/v1/event-store/debug-replay", h.debugReplay).Methods("POST")
}

// RegisterEventStoreRoutes is the package-level helper used by cmd/main.go.
func RegisterEventStoreRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterEventStoreRoutes(router)
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

func (h *Handlers) append(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AggregateID string          `json:"aggregate_id"`
		ExpectedSeq int             `json:"expected_seq"`
		Type        string          `json:"type"`
		Payload     json.RawMessage `json:"payload"`
		At          string          `json:"at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	at, err := ParseOptionalTime(req.At)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	ev, err := h.svc.Append(req.AggregateID, req.ExpectedSeq, req.Type, req.Payload, at)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, ev)
}

func (h *Handlers) loadStream(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	events, err := h.svc.LoadStream(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, events)
}

func (h *Handlers) project(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	toSeq := 0
	if raw := r.URL.Query().Get("to_seq"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			respondError(w, http.StatusBadRequest, "to_seq must be a non-negative integer")
			return
		}
		toSeq = n
	} else {
		var req struct {
			ToSeq *int `json:"to_seq"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.ToSeq != nil {
				if *req.ToSeq < 0 {
					respondError(w, http.StatusBadRequest, "to_seq must be >= 0")
					return
				}
				toSeq = *req.ToSeq
			}
		}
	}
	state, err := h.svc.ProjectTo(id, toSeq)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, state)
}

func (h *Handlers) registerProjector(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string   `json:"name"`
		EventTypes []string `json:"event_types"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.RegisterNamedProjector(req.Name, req.EventTypes); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]interface{}{"name": req.Name, "event_types": req.EventTypes})
}

func (h *Handlers) debugReplay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CustomerID   string            `json:"customer_id"`
		AggregateID  string            `json:"aggregate_id"`
		At           string            `json:"at"`
		RuleVersions map[string]string `json:"rule_versions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id := req.AggregateID
	if id == "" {
		id = req.CustomerID
	}
	if id == "" {
		respondError(w, http.StatusBadRequest, "customer_id (or aggregate_id) is required")
		return
	}
	if req.At == "" {
		respondError(w, http.StatusBadRequest, "at timestamp is required (RFC3339)")
		return
	}
	at, err := ParseOptionalTime(req.At)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.svc.ReplayAt(id, PinnedReplay{At: at, RuleVersions: req.RuleVersions})
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}
