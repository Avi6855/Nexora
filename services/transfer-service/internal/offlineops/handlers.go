package offlineops

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/offline"
	"github.com/rs/zerolog"
)

// Handlers exposes offline-first intents + conflict resolution under
// /v1/offline.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers returns offline handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all offline routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/offline/devices", h.RegisterDevice).Methods("POST")
	router.HandleFunc("/v1/offline/intents/queue", h.QueueIntent).Methods("POST")
	router.HandleFunc("/v1/offline/sync", h.Sync).Methods("POST")
	router.HandleFunc("/v1/offline/conflicts/resolve", h.ResolveConflict).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

type registerDeviceRequest struct {
	DeviceID string `json:"device_id"`
	Secret   string `json:"secret"`
}

// RegisterDevice enrols a device secret.
func (h *Handlers) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DeviceID == "" || req.Secret == "" {
		respondError(w, http.StatusBadRequest, "device_id and secret are required")
		return
	}
	if err := h.svc.RegisterDevice(req.DeviceID, req.Secret); err != nil {
		if errors.Is(err, offline.ErrDeviceExists) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]string{"device_id": req.DeviceID})
}

type queueIntentRequest struct {
	DeviceID  string `json:"device_id"`
	RequestID string `json:"request_id"`
	Account   string `json:"account"`
	Amount    int64  `json:"amount"`
	Payee     string `json:"payee"`
	Seq       uint64 `json:"seq"`
	PrevHash  string `json:"prev_hash"`
	Signature string `json:"signature"`
	Blob      string `json:"blob,omitempty"`
}

// QueueIntent stores an offline intent blob. Validation of signatures and
// ordering happens at Sync time so offline clients always make progress.
func (h *Handlers) QueueIntent(w http.ResponseWriter, r *http.Request) {
	var req queueIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DeviceID == "" || req.RequestID == "" {
		respondError(w, http.StatusBadRequest, "device_id and request_id are required")
		return
	}
	if req.Seq == 0 {
		respondError(w, http.StatusBadRequest, "seq must be positive")
		return
	}
	intent := offline.SignedIntent{
		RequestID: req.RequestID, Account: req.Account, Amount: req.Amount,
		Payee: req.Payee, Seq: req.Seq, PrevHash: req.PrevHash,
		Signature: req.Signature, Blob: []byte(req.Blob),
	}
	if err := h.svc.QueueIntent(req.DeviceID, intent); err != nil {
		if errors.Is(err, offline.ErrUnknownDevice) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]string{"request_id": req.RequestID, "status": "QUEUED"})
}

type syncRequest struct {
	DeviceID string `json:"device_id"`
}

// Sync commits a device's queue in seq order.
func (h *Handlers) Sync(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DeviceID == "" {
		respondError(w, http.StatusBadRequest, "device_id is required")
		return
	}
	results, err := h.svc.Sync(req.DeviceID)
	if err != nil {
		if errors.Is(err, offline.ErrUnknownDevice) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if results == nil {
		results = make([]offline.SyncResult, 0)
	}
	respondJSON(w, http.StatusOK, results)
}

type resolveRequest struct {
	Local  offline.VersionedOp `json:"local"`
	Remote offline.VersionedOp `json:"remote"`
}

// ResolveConflict decides between two concurrent versioned ops.
func (h *Handlers) ResolveConflict(w http.ResponseWriter, r *http.Request) {
	var req resolveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Local.Entity == "" || req.Remote.Entity == "" {
		respondError(w, http.StatusBadRequest, "local.entity and remote.entity are required")
		return
	}
	res, err := h.svc.Resolve(req.Local, req.Remote)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}
