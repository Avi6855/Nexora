package dataplatform

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/dataplatform"
)

// Handlers exposes the data-platform governance API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds the handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes wires the routes under /v1/data.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/data/contracts/evaluate", h.EvaluateContract).Methods("POST")
	router.HandleFunc("/v1/data/datasets", h.RegisterDataset).Methods("POST")
	router.HandleFunc("/v1/data/datasets/{name}/health", h.DatasetHealth).Methods("GET")
	router.HandleFunc("/v1/data/datasets/{name}/incidents", h.OpenIncident).Methods("POST")
	router.HandleFunc("/v1/data/queries/evaluate", h.EvaluateQuery).Methods("POST")
	router.HandleFunc("/v1/data/access-sessions", h.StartAccessSession).Methods("POST")
	router.HandleFunc("/v1/data/access-sessions/{id}/events", h.RecordAccess).Methods("POST")
	router.HandleFunc("/v1/data/access-sessions/{id}/verify", h.VerifyAccessChain).Methods("GET")
	router.HandleFunc("/v1/data/migrations", h.SubmitMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/validate", h.ValidateMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/canary", h.CanaryMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/apply", h.ApplyMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/verify-failed", h.FailVerification).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/pause", h.PauseMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/resume", h.ResumeMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/rollback", h.RollbackMigration).Methods("POST")
	router.HandleFunc("/v1/data/migrations/{id}/stage", h.MigrationStage).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func isUnknown(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "unknown dataset") ||
		strings.Contains(err.Error(), "unknown migration") ||
		strings.Contains(err.Error(), "unknown access session"))
}

func isConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// ── Contracts ─────────────────────────────────────────────────────────────

type contractEvaluateRequest struct {
	Contract struct {
		Dataset             string  `json:"dataset"`
		Consumer            string  `json:"consumer"`
		MaxFreshness        string  `json:"max_freshness,omitempty"`
		MaxFreshnessSeconds float64 `json:"max_freshness_seconds,omitempty"`
		MaxNullRatePct      float64 `json:"max_null_rate_pct"`
		MaxDupRatePct       float64 `json:"max_dup_rate_pct"`
		MinRows             int64   `json:"min_rows"`
		MaxRows             int64   `json:"max_rows"`
	} `json:"contract"`
	Measurement struct {
		Dataset             string  `json:"dataset"`
		NewestRowAge        string  `json:"newest_row_age,omitempty"`
		NewestRowAgeSeconds float64 `json:"newest_row_age_seconds,omitempty"`
		NullRatePct         float64 `json:"null_rate_pct"`
		DupRatePct          float64 `json:"dup_rate_pct"`
		Rows                int64   `json:"rows"`
	} `json:"measurement"`
}

func (h *Handlers) EvaluateContract(w http.ResponseWriter, r *http.Request) {
	var req contractEvaluateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Contract.Dataset) == "" {
		respondError(w, http.StatusBadRequest, "contract.dataset is required")
		return
	}
	contract := shared.QualityContract{
		Dataset:        req.Contract.Dataset,
		Consumer:       req.Contract.Consumer,
		MaxNullRatePct: req.Contract.MaxNullRatePct,
		MaxDupRatePct:  req.Contract.MaxDupRatePct,
		MinRows:        req.Contract.MinRows,
		MaxRows:        req.Contract.MaxRows,
	}
	if req.Contract.MaxFreshness != "" {
		d, err := time.ParseDuration(req.Contract.MaxFreshness)
		if err != nil || d <= 0 {
			respondError(w, http.StatusBadRequest, "contract.max_freshness must be a valid positive duration")
			return
		}
		contract.MaxFreshness = d
	} else if req.Contract.MaxFreshnessSeconds > 0 {
		contract.MaxFreshness = time.Duration(req.Contract.MaxFreshnessSeconds * float64(time.Second))
	} else {
		respondError(w, http.StatusBadRequest, "contract.max_freshness or max_freshness_seconds is required")
		return
	}
	m := shared.QualityMeasurement{
		Dataset:     req.Measurement.Dataset,
		CheckedAt:   time.Now().UTC(),
		NullRatePct: req.Measurement.NullRatePct,
		DupRatePct:  req.Measurement.DupRatePct,
		Rows:        req.Measurement.Rows,
	}
	if m.Dataset == "" {
		m.Dataset = contract.Dataset
	}
	if req.Measurement.NewestRowAge != "" {
		d, err := time.ParseDuration(req.Measurement.NewestRowAge)
		if err != nil || d < 0 {
			respondError(w, http.StatusBadRequest, "measurement.newest_row_age must be a valid non-negative duration")
			return
		}
		m.NewestRowAge = d
	} else {
		m.NewestRowAge = time.Duration(req.Measurement.NewestRowAgeSeconds * float64(time.Second))
	}
	verdict, err := h.svc.PublishContractEvaluation(contract, m)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, verdict)
}

// ── Datasets ──────────────────────────────────────────────────────────────

type registerDatasetRequest struct {
	Name   string            `json:"name"`
	Owner  string            `json:"owner"`
	Tier   string            `json:"tier"`
	Schema map[string]string `json:"schema"`
}

func (h *Handlers) RegisterDataset(w http.ResponseWriter, r *http.Request) {
	var req registerDatasetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	p, err := h.svc.RegisterDataset(req.Name, req.Owner, req.Tier, req.Schema, time.Now().UTC())
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, p)
}

func (h *Handlers) DatasetHealth(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	health, err := h.svc.DatasetHealth(name)
	if err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, _ := h.svc.GetDataset(name)
	resp := map[string]interface{}{"name": name, "health": string(health)}
	if p != nil {
		resp["owner"] = p.Owner
		resp["tier"] = p.Tier
		resp["schema_hash"] = p.SchemaHash
		resp["open_incidents"] = len(p.OpenIssues)
	}
	respondJSON(w, http.StatusOK, resp)
}

type openIncidentRequest struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

func (h *Handlers) OpenIncident(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	var req openIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Summary) == "" {
		respondError(w, http.StatusBadRequest, "id and summary are required")
		return
	}
	err := h.svc.OpenDatasetIncident(name, shared.Incident{
		ID: req.ID, OpenedAt: time.Now().UTC(), Severity: req.Severity, Summary: req.Summary,
	})
	if err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"status": "incident opened", "id": req.ID})
}

// ── Query gateway ─────────────────────────────────────────────────────────

func (h *Handlers) EvaluateQuery(w http.ResponseWriter, r *http.Request) {
	var req shared.QueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Dataset) == "" || strings.TrimSpace(req.Purpose) == "" || strings.TrimSpace(req.Environment) == "" {
		respondError(w, http.StatusBadRequest, "dataset, purpose and environment are required")
		return
	}
	verdict, reason := h.svc.EvaluateQuery(req)
	respondJSON(w, http.StatusOK, map[string]string{"verdict": string(verdict), "reason": reason})
}

// ── Access sessions ───────────────────────────────────────────────────────

type startSessionRequest struct {
	ID        string `json:"id"`
	Engineer  string `json:"engineer"`
	TicketRef string `json:"ticket_ref"`
}

func (h *Handlers) StartAccessSession(w http.ResponseWriter, r *http.Request) {
	var req startSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Engineer) == "" || strings.TrimSpace(req.TicketRef) == "" {
		respondError(w, http.StatusBadRequest, "id, engineer and ticket_ref are required")
		return
	}
	sess, err := h.svc.StartAccessSession(req.ID, req.Engineer, req.TicketRef, time.Now().UTC())
	if err != nil {
		if isConflict(err) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, sess)
}

func (h *Handlers) RecordAccess(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var ev shared.AccessEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(ev.Engineer) == "" || strings.TrimSpace(ev.Customer) == "" || len(ev.Fields) == 0 || strings.TrimSpace(ev.Reason) == "" {
		respondError(w, http.StatusBadRequest, "engineer, customer, fields and reason are required")
		return
	}
	if err := h.svc.RecordAccess(id, ev); err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "recorded", "session_id": id})
}

func (h *Handlers) VerifyAccessChain(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	valid, err := h.svc.VerifyAccessChain(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	sess, _ := h.svc.AccessSessionInfo(id)
	events := 0
	chain := ""
	if sess != nil {
		events = len(sess.Events)
		chain = sess.ChainHash
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"session_id": id, "valid": valid, "events": events, "chain_hash": chain})
}

// ── Migrations ────────────────────────────────────────────────────────────

type submitMigrationRequest struct {
	ID              string   `json:"id"`
	Target          string   `json:"target"`
	Statement       string   `json:"statement"`
	DropColumn      string   `json:"drop_column,omitempty"`
	ReadByConsumers []string `json:"read_by_consumers,omitempty"`
	EstRows         int64    `json:"est_rows,omitempty"`
}

func (h *Handlers) SubmitMigration(w http.ResponseWriter, r *http.Request) {
	var req submitMigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ID) == "" || strings.TrimSpace(req.Target) == "" {
		respondError(w, http.StatusBadRequest, "id and target are required")
		return
	}
	st, err := h.svc.SubmitMigration(shared.MigrationRequest{
		ID: req.ID, Target: req.Target, Statement: req.Statement,
		DropColumn: req.DropColumn, ReadByConsumers: req.ReadByConsumers, EstRows: req.EstRows,
	}, time.Now().UTC())
	if err != nil {
		if isConflict(err) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, st)
}

func (h *Handlers) migrationResult(w http.ResponseWriter, id string, err error) {
	if err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	stage, serr := h.svc.MigrationStage(id)
	if serr != nil {
		respondError(w, http.StatusNotFound, serr.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"id": id, "stage": string(stage)})
}

func (h *Handlers) ValidateMigration(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	h.migrationResult(w, id, h.svc.ValidateMigration(id))
}

type canaryRequest struct {
	Pct float64 `json:"pct"`
	Bad bool    `json:"bad,omitempty"`
}

func (h *Handlers) CanaryMigration(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req canaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Pct <= 0 || req.Pct > 100 {
		respondError(w, http.StatusBadRequest, "pct must be within (0, 100]")
		return
	}
	h.migrationResult(w, id, h.svc.CanaryMigration(id, req.Pct, req.Bad))
}

func (h *Handlers) ApplyMigration(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	h.migrationResult(w, id, h.svc.ApplyMigration(id))
}

func (h *Handlers) FailVerification(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	h.migrationResult(w, id, h.svc.FailVerification(id))
}

func (h *Handlers) PauseMigration(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	h.migrationResult(w, id, h.svc.PauseMigration(id))
}

func (h *Handlers) ResumeMigration(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	stage, err := h.svc.ResumeMigration(id)
	if err != nil {
		if isUnknown(err) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"id": id, "stage": string(stage)})
}

func (h *Handlers) RollbackMigration(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	h.migrationResult(w, id, h.svc.RollbackMigration(id))
}

func (h *Handlers) MigrationStage(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	stage, err := h.svc.MigrationStage(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"id": id, "stage": string(stage)})
}
