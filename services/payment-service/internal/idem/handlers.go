package idem

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	shared "github.com/nexora/nexora/shared/idempotency"
)

// Handlers serves the /v1/idem API.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers builds idempotency handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts the idempotency routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/idem/execute", h.Execute).Methods("POST")
	router.HandleFunc("/v1/idem/requests/{key}", h.GetRequest).Methods("GET")
	router.HandleFunc("/v1/idem/sweep", h.Sweep).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type executeRequest struct {
	Key    string `json:"key"`
	Method string `json:"method"`
	Path   string `json:"path"`
	// BodyBase64 carries an opaque payload whose hash feeds the fingerprint.
	BodyBase64 string `json:"body_base64,omitempty"`
	// ResponseBase64 is the simulated downstream response stored on success.
	ResponseBase64 string `json:"response_base64,omitempty"`
}

// Execute dedupes by key: PROCESSING → 409, SUCCEEDED → replay, EXPIRED →
// re-execute.
func (h *Handlers) Execute(w http.ResponseWriter, r *http.Request) {
	var req executeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Key == "" {
		respondError(w, http.StatusBadRequest, "key is required")
		return
	}
	method, path := req.Method, req.Path
	if method == "" {
		method = "POST"
	}
	if path == "" {
		path = "/v1/payments"
	}
	body, err := base64.StdEncoding.DecodeString(req.BodyBase64)
	if err != nil && req.BodyBase64 != "" {
		respondError(w, http.StatusBadRequest, "body_base64 must be valid base64")
		return
	}
	want, err := base64.StdEncoding.DecodeString(req.ResponseBase64)
	if err != nil && req.ResponseBase64 != "" {
		respondError(w, http.StatusBadRequest, "response_base64 must be valid base64")
		return
	}
	if want == nil {
		want = []byte(`{"status":"ok"}`)
	}
	resp, replayed, err := h.svc.Execute(req.Key, method, path, body, func() ([]byte, error) {
		return want, nil
	})
	if err != nil {
		switch {
		case errors.Is(err, shared.ErrConflictRetry):
			respondError(w, http.StatusConflict, err.Error())
		case errors.Is(err, shared.ErrConflict):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{
		"key":      req.Key,
		"replayed": replayed,
		"response": base64.StdEncoding.EncodeToString(resp),
	})
}

func (h *Handlers) GetRequest(w http.ResponseWriter, r *http.Request) {
	key := mux.Vars(r)["key"]
	if key == "" {
		respondError(w, http.StatusBadRequest, "key is required")
		return
	}
	rec, err := h.svc.Get(key)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, rec)
}

func (h *Handlers) Sweep(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]int{"expired": h.svc.Sweep()})
}
