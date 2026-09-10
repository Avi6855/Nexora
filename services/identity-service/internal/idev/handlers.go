package idev

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"

	"github.com/nexora/nexora/shared/identity"
)

// Handlers exposes the account-access platform over HTTP.
type Handlers struct {
	svc *Service
}

// NewHandlers constructs Handlers.
func NewHandlers(svc *Service) *Handlers {
	return &Handlers{svc: svc}
}

// RegisterRoutes mounts recovery, device-trust and passkey endpoints.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/recovery/sessions", h.startRecovery).Methods("POST")
	router.HandleFunc("/v1/recovery/sessions/{id}/signals", h.presentSignal).Methods("POST")
	router.HandleFunc("/v1/recovery/sessions/{id}/evaluate", h.evaluateRecovery).Methods("POST")

	router.HandleFunc("/v1/devices", h.registerDevice).Methods("POST")
	router.HandleFunc("/v1/devices/expire", h.expireDevices).Methods("POST")
	router.HandleFunc("/v1/devices/{id}/link", h.linkDevices).Methods("POST")
	router.HandleFunc("/v1/devices/{id}/trust", h.trustDevice).Methods("POST")
	router.HandleFunc("/v1/devices/{id}/flag", h.flagDevice).Methods("POST")
	router.HandleFunc("/v1/devices/{id}/revoke", h.revokeDevice).Methods("POST")
	router.HandleFunc("/v1/devices/{id}", h.deviceState).Methods("GET")

	router.HandleFunc("/v1/passkeys/challenges", h.issueChallenge).Methods("POST")
	router.HandleFunc("/v1/passkeys/challenges/{id}/consume", h.consumeChallenge).Methods("POST")
	router.HandleFunc("/v1/passkeys/recovery-credentials", h.enrolRecovery).Methods("POST")
	router.HandleFunc("/v1/passkeys/recovery-credentials/rotate", h.rotateRecovery).Methods("POST")
	router.HandleFunc("/v1/passkeys/recovery-credentials/use", h.useRecovery).Methods("POST")
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// domainError maps failures: 404 unknown IDs, 409 gated/conflicting state,
// 400 validation.
func domainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound) || errors.Is(err, identity.ErrUnknownDevice):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrConflict),
		errors.Is(err, identity.ErrInsufficientSignals),
		errors.Is(err, identity.ErrChallengeState),
		errors.Is(err, identity.ErrReplay),
		errors.Is(err, identity.ErrLastCredential):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

type sessionRequest struct {
	AccountID string `json:"account_id"`
}

type sessionResponse struct {
	SessionID string                `json:"session_id"`
	AccountID string                `json:"account_id"`
	Signals   []identity.SignalKind `json:"signals"`
	Score     int                   `json:"score"`
}

func toSessionResponse(id string, s *identity.RecoverySession) sessionResponse {
	signals := s.Signals
	if signals == nil {
		signals = []identity.SignalKind{}
	}
	return sessionResponse{SessionID: id, AccountID: s.AccountID, Signals: signals, Score: s.Score}
}

type signalRequest struct {
	Kind identity.SignalKind `json:"kind"`
}

type deviceRequest struct {
	DeviceID string `json:"device_id"`
}

type linkRequest struct {
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type expireRequest struct {
	MaxAgeHours *float64 `json:"max_age_hours,omitempty"`
}

type challengeRequest struct {
	AccountID string `json:"account_id"`
}

type consumeRequest struct {
	AccountID string `json:"account_id"`
}

type enrolRequest struct {
	AccountID    string `json:"account_id"`
	CredentialID string `json:"credential_id"`
}

type rotateRequest struct {
	AccountID       string `json:"account_id"`
	OldCredentialID string `json:"old_credential_id"`
	NewCredentialID string `json:"new_credential_id"`
}

type useRequest struct {
	AccountID    string `json:"account_id"`
	CredentialID string `json:"credential_id"`
}

func (h *Handlers) startRecovery(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	id, sess, err := h.svc.StartRecovery(req.AccountID)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSessionResponse(id, sess))
}

func (h *Handlers) presentSignal(w http.ResponseWriter, r *http.Request) {
	var req signalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind is required")
		return
	}
	sess, err := h.svc.PresentSignal(mux.Vars(r)["id"], req.Kind)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSessionResponse(mux.Vars(r)["id"], sess))
}

func (h *Handlers) evaluateRecovery(w http.ResponseWriter, r *http.Request) {
	verified, reason, err := h.svc.EvaluateRecovery(mux.Vars(r)["id"])
	if err != nil {
		// Unknown session → 404; insufficient signals → 409 with the reason.
		if errors.Is(err, ErrNotFound) {
			domainError(w, err)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]interface{}{"verified": false, "reason": reason})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"verified": verified, "reason": reason})
}

func (h *Handlers) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req deviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.DeviceID == "" {
		writeError(w, http.StatusBadRequest, "device_id is required")
		return
	}
	dev, err := h.svc.RegisterDevice(req.DeviceID)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"device_id": dev.ID, "state": dev.State})
}

func (h *Handlers) linkDevices(w http.ResponseWriter, r *http.Request) {
	var req linkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.To == "" || req.Kind == "" {
		writeError(w, http.StatusBadRequest, "to and kind are required")
		return
	}
	if err := h.svc.LinkDevices(mux.Vars(r)["id"], req.To, req.Kind); err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "linked"})
}

func (h *Handlers) trustDevice(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.TrustDevice(id); err != nil {
		domainError(w, err)
		return
	}
	st, _ := h.svc.DeviceState(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"device_id": id, "state": st})
}

func (h *Handlers) flagDevice(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.FlagDevice(id); err != nil {
		domainError(w, err)
		return
	}
	st, _ := h.svc.DeviceState(id)
	writeJSON(w, http.StatusOK, map[string]interface{}{"device_id": id, "state": st})
}

func (h *Handlers) revokeDevice(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.RevokeDevice(id); err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"device_id": id, "state": identity.DeviceRevoked})
}

func (h *Handlers) expireDevices(w http.ResponseWriter, r *http.Request) {
	var req expireRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	maxAge := 90 * 24 * time.Hour
	if req.MaxAgeHours != nil && *req.MaxAgeHours > 0 {
		maxAge = time.Duration(*req.MaxAgeHours * float64(time.Hour))
	}
	expired := h.svc.ExpireDevices(maxAge)
	if expired == nil {
		expired = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"expired": expired})
}

func (h *Handlers) deviceState(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	st, err := h.svc.DeviceState(id)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"device_id": id, "state": st})
}

func (h *Handlers) issueChallenge(w http.ResponseWriter, r *http.Request) {
	var req challengeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	id, err := h.svc.IssueChallenge(req.AccountID)
	if err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"challenge_id": id})
}

func (h *Handlers) consumeChallenge(w http.ResponseWriter, r *http.Request) {
	var req consumeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AccountID == "" {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	if err := h.svc.ConsumeChallenge(mux.Vars(r)["id"], req.AccountID); err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "consumed"})
}

func (h *Handlers) enrolRecovery(w http.ResponseWriter, r *http.Request) {
	var req enrolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AccountID == "" || req.CredentialID == "" {
		writeError(w, http.StatusBadRequest, "account_id and credential_id are required")
		return
	}
	if err := h.svc.EnrolRecovery(req.AccountID, req.CredentialID); err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"valid_count": h.svc.ValidRecoveryCount(req.AccountID)})
}

func (h *Handlers) rotateRecovery(w http.ResponseWriter, r *http.Request) {
	var req rotateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AccountID == "" || req.OldCredentialID == "" || req.NewCredentialID == "" {
		writeError(w, http.StatusBadRequest, "account_id, old_credential_id and new_credential_id are required")
		return
	}
	if err := h.svc.RotateRecovery(req.AccountID, req.OldCredentialID, req.NewCredentialID); err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"valid_count": h.svc.ValidRecoveryCount(req.AccountID)})
}

func (h *Handlers) useRecovery(w http.ResponseWriter, r *http.Request) {
	var req useRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.AccountID == "" || req.CredentialID == "" {
		writeError(w, http.StatusBadRequest, "account_id and credential_id are required")
		return
	}
	if err := h.svc.UseRecovery(req.AccountID, req.CredentialID); err != nil {
		domainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "used"})
}
