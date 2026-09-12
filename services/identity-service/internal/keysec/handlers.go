package keysec

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	shared "github.com/nexora/nexora/shared/keysec"
)

// Handlers exposes the keysec API.
type Handlers struct {
	svc *Service
}

// NewHandlers builds handlers over a service.
func NewHandlers(svc *Service) *Handlers { return &Handlers{svc: svc} }

// RegisterKeysecRoutes mounts /v1/keysec routes.
func (h *Handlers) RegisterKeysecRoutes(router *mux.Router) {
	router.HandleFunc("/v1/keysec/keys", h.createKey).Methods("POST")
	router.HandleFunc("/v1/keysec/keys/{id}/activate", h.activateKey).Methods("POST")
	router.HandleFunc("/v1/keysec/keys/{id}/rotate", h.rotateKey).Methods("POST")
	router.HandleFunc("/v1/keysec/keys/{id}/revoke", h.revokeKey).Methods("POST")
	router.HandleFunc("/v1/keysec/keys/{id}/destroy", h.destroyKey).Methods("POST")
	router.HandleFunc("/v1/keysec/services/{name}/active-key", h.activeKey).Methods("GET")
	router.HandleFunc("/v1/keysec/usage/observe", h.observeUsage).Methods("POST")
	router.HandleFunc("/v1/keysec/usage/anomalies", h.anomalies).Methods("GET")
	router.HandleFunc("/v1/keysec/scan", h.scan).Methods("POST")
	router.HandleFunc("/v1/keysec/policy-sim/run", h.policySimRun).Methods("POST")
}

// RegisterKeysecRoutes is the package-level helper used by cmd/main.go.
func RegisterKeysecRoutes(router *mux.Router, svc *Service) {
	NewHandlers(svc).RegisterKeysecRoutes(router)
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

func (h *Handlers) createKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Service string `json:"service"`
		Type    string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	k, err := h.svc.CreateKey(req.Service, req.Type)
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, k)
}

func (h *Handlers) activateKey(w http.ResponseWriter, r *http.Request) {
	k, err := h.svc.ActivateKey(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, k)
}

func (h *Handlers) rotateKey(w http.ResponseWriter, r *http.Request) {
	k, err := h.svc.RotateKey(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, k)
}

func (h *Handlers) revokeKey(w http.ResponseWriter, r *http.Request) {
	k, err := h.svc.RevokeKey(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, k)
}

func (h *Handlers) destroyKey(w http.ResponseWriter, r *http.Request) {
	k, err := h.svc.DestroyKey(mux.Vars(r)["id"])
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, k)
}

func (h *Handlers) activeKey(w http.ResponseWriter, r *http.Request) {
	k, err := h.svc.ActiveKey(mux.Vars(r)["name"])
	if err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, k)
}

func (h *Handlers) observeUsage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		KeyID     string `json:"key_id"`
		Day       string `json:"day"`
		Count     int    `json:"count"`
		Service   string `json:"service"`
		Region    string `json:"region"`
		Operation string `json:"operation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	day := time.Now().UTC()
	if strings.TrimSpace(req.Day) != "" {
		t, err := time.Parse(time.RFC3339, req.Day)
		if err != nil {
			if t2, err2 := time.Parse("2006-01-02", req.Day); err2 == nil {
				t = t2
			} else {
				respondError(w, http.StatusBadRequest, "day must be RFC3339 or YYYY-MM-DD")
				return
			}
		}
		day = t.UTC()
	}
	if err := h.svc.ObserveUsage(req.KeyID, day, req.Count, req.Service, req.Region, req.Operation); err != nil {
		respondError(w, statusFor(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "observed"})
}

func (h *Handlers) anomalies(w http.ResponseWriter, r *http.Request) {
	out := h.svc.Anomalies()
	if out == nil {
		out = []shared.Anomaly{}
	}
	respondJSON(w, http.StatusOK, out)
}

func (h *Handlers) scan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text   string `json:"text"`
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	text := req.Text
	if text == "" {
		text = req.Commit
	}
	verdict, findings := h.svc.Scan(text)
	if findings == nil {
		findings = []shared.Finding{}
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"verdict": verdict, "findings": findings})
}

func (h *Handlers) policySimRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Policy  shared.Policy         `json:"policy"`
		Traffic []shared.TrafficEvent `json:"traffic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res := h.svc.Simulate(req.Policy, req.Traffic)
	respondJSON(w, http.StatusOK, res)
}
