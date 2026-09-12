package eventgov

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	shared "github.com/nexora/nexora/shared/eventgov"
)

// Handlers exposes the event-governance API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterEventGovRoutes mounts /v1/event-gov routes. The static diff
// route is registered before /runs/{id} so "diff" is not captured as an
// ID.
func (h *Handlers) RegisterEventGovRoutes(router *mux.Router) {
	router.HandleFunc("/v1/event-gov/capture", h.capture).Methods("POST")
	router.HandleFunc("/v1/event-gov/runs", h.startRun).Methods("POST")
	router.HandleFunc("/v1/event-gov/runs/diff", h.diffRuns).Methods("GET")
	router.HandleFunc("/v1/event-gov/runs/{id}", h.getRun).Methods("GET")
	router.HandleFunc("/v1/event-gov/schemas", h.registerSchema).Methods("POST")
	router.HandleFunc("/v1/event-gov/schemas/check", h.checkCompatibility).Methods("POST")
	router.HandleFunc("/v1/event-gov/schemas/deprecate", h.deprecateField).Methods("POST")
	router.HandleFunc("/v1/event-gov/schemas/migration-plan", h.migrationPlan).Methods("GET")
}

// RegisterEventGovRoutes is the package-level helper used by cmd/main.go.
func RegisterEventGovRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterEventGovRoutes(router)
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

func (h *Handlers) capture(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scenario      string `json:"scenario"`
		Type          string `json:"type"`
		PayloadHash   string `json:"payload_hash"`
		SchemaVersion int    `json:"schema_version"`
		At            string `json:"at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	at, err := ParseTime(req.At)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	env := shared.Envelope{Type: req.Type, PayloadHash: req.PayloadHash, SchemaVersion: req.SchemaVersion, At: at}
	if err := h.svc.Capture(req.Scenario, env); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]interface{}{"scenario": req.Scenario, "envelope": env})
}

func (h *Handlers) startRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scenario   string `json:"scenario"`
		Multiplier int    `json:"multiplier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	run, err := h.svc.StartRun(req.Scenario, req.Multiplier)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, run)
}

func (h *Handlers) getRun(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	run, err := h.svc.GetRun(id)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, run)
}

func (h *Handlers) diffRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base, cand := q.Get("base"), q.Get("candidate")
	if base == "" || cand == "" {
		respondError(w, http.StatusBadRequest, "base and candidate query params are required")
		return
	}
	diff, err := h.svc.DiffRuns(base, cand)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, diff)
}

func (h *Handlers) registerSchema(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic   string         `json:"topic"`
		Version int            `json:"version"`
		Fields  []shared.Field `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	schema, err := h.svc.RegisterSchema(req.Topic, req.Version, req.Fields)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, schema)
}

func (h *Handlers) checkCompatibility(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic      string `json:"topic"`
		OldVersion int    `json:"old_version"`
		NewVersion int    `json:"new_version"`
		Mode       string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.svc.CheckCompatibility(req.Topic, req.OldVersion, req.NewVersion, shared.CompatibilityMode(req.Mode))
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

func (h *Handlers) deprecateField(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Topic   string `json:"topic"`
		Version int    `json:"version"`
		Field   string `json:"field"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.svc.DeprecateField(req.Topic, req.Version, req.Field); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "field deprecated"})
}

func (h *Handlers) migrationPlan(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	topic := q.Get("topic")
	from, err1 := strconv.Atoi(q.Get("from"))
	to, err2 := strconv.Atoi(q.Get("to"))
	if topic == "" || err1 != nil || err2 != nil {
		respondError(w, http.StatusBadRequest, "topic, from and to query params are required (from/to integers)")
		return
	}
	plan, err := h.svc.MigrationPlan(topic, from, to)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, plan)
}
