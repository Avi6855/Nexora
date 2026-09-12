package ledgerguard

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nexora/nexora/shared/ledgerguard"
	"github.com/rs/zerolog"
)

// Handlers exposes the ledger-guard safety overlay under /v1/ledger-guard.
type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

// NewHandlers returns guard handlers.
func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

// RegisterRoutes mounts all guard routes. The sweep route is registered
// before the {id} routes so /reservations/sweep is not captured as an ID.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/ledger-guard/postings", h.PostPosting).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/journals/{id}/freeze", h.FreezeJournal).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/journals/{id}/unfreeze", h.UnfreezeJournal).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/incidents", h.ListIncidents).Methods("GET")
	router.HandleFunc("/v1/ledger-guard/incidents/{id}/clear", h.ClearIncident).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/reservations/sweep", h.SweepReservations).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/reservations", h.AuthorizeReservation).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/reservations/{id}/topup", h.TopUpReservation).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/reservations/{id}/capture", h.CaptureReservation).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/reservations/{id}/reverse", h.ReverseReservation).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/reservations/{id}/settle-late", h.SettleLate).Methods("POST")
	router.HandleFunc("/v1/ledger-guard/balances", h.GetBalances).Methods("GET")
	router.HandleFunc("/v1/ledger-guard/balances/fund", h.FundBalance).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func writeGuardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ledgerguard.ErrJournalNotFound),
		errors.Is(err, ledgerguard.ErrIncidentNotFound),
		errors.Is(err, ledgerguard.ErrReservationNotFound):
		respondError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ledgerguard.ErrJournalFrozen),
		errors.Is(err, ledgerguard.ErrUnbalanced),
		errors.Is(err, ledgerguard.ErrDuplicateReservation),
		errors.Is(err, ledgerguard.ErrInsufficientFunds),
		errors.Is(err, ledgerguard.ErrReservationNotActive):
		respondError(w, http.StatusConflict, err.Error())
	default:
		respondError(w, http.StatusBadRequest, err.Error())
	}
}

type postPostingRequest struct {
	JournalID string              `json:"journal_id"`
	Entries   []ledgerguard.Entry `json:"entries"`
}

// PostPosting validates + appends a journal posting.
func (h *Handlers) PostPosting(w http.ResponseWriter, r *http.Request) {
	var req postPostingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.JournalID == "" {
		respondError(w, http.StatusBadRequest, "journal_id is required")
		return
	}
	if len(req.Entries) == 0 {
		respondError(w, http.StatusBadRequest, "at least one entry is required")
		return
	}
	res, err := h.svc.Post(req.JournalID, req.Entries)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, res)
}

// FreezeJournal locks a journal.
func (h *Handlers) FreezeJournal(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid journal ID")
		return
	}
	if err := h.svc.Freeze(id); err != nil {
		writeGuardError(w, err)
		return
	}
	j, err := h.svc.GetJournal(id)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, j)
}

// UnfreezeJournal re-opens a journal.
func (h *Handlers) UnfreezeJournal(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid journal ID")
		return
	}
	if err := h.svc.Unfreeze(id); err != nil {
		writeGuardError(w, err)
		return
	}
	j, err := h.svc.GetJournal(id)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, j)
}

// ListIncidents returns all filed incidents.
func (h *Handlers) ListIncidents(w http.ResponseWriter, r *http.Request) {
	incidents := h.svc.Incidents()
	if incidents == nil {
		incidents = make([]ledgerguard.Incident, 0)
	}
	respondJSON(w, http.StatusOK, incidents)
}

// ClearIncident closes an incident.
func (h *Handlers) ClearIncident(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "invalid incident ID")
		return
	}
	if err := h.svc.ClearIncident(id); err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "incident cleared"})
}

type authorizeRequest struct {
	ID        string `json:"id"`
	Account   string `json:"account"`
	Amount    int64  `json:"amount"`
	ExpiresAt string `json:"expires_at,omitempty"`
	TTLSecs   int64  `json:"ttl_seconds,omitempty"`
}

func parseExpiry(req authorizeRequest) (time.Time, error) {
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			return time.Time{}, errors.New("expires_at must be RFC3339")
		}
		return t, nil
	}
	if req.TTLSecs > 0 {
		return time.Now().UTC().Add(time.Duration(req.TTLSecs) * time.Second), nil
	}
	return time.Now().UTC().Add(24 * time.Hour), nil
}

// AuthorizeReservation places a hold.
func (h *Handlers) AuthorizeReservation(w http.ResponseWriter, r *http.Request) {
	var req authorizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" || req.Account == "" {
		respondError(w, http.StatusBadRequest, "id and account are required")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	expiresAt, err := parseExpiry(req)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.svc.Authorize(req.ID, req.Account, req.Amount, expiresAt)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, res)
}

type amountRequest struct {
	Amount int64 `json:"amount"`
}

// TopUpReservation incrementally authorises more funds.
func (h *Handlers) TopUpReservation(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" || id == "sweep" {
		respondError(w, http.StatusBadRequest, "invalid reservation ID")
		return
	}
	var req amountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	res, err := h.svc.TopUp(id, req.Amount)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// CaptureReservation settles part or all of a hold.
func (h *Handlers) CaptureReservation(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" || id == "sweep" {
		respondError(w, http.StatusBadRequest, "invalid reservation ID")
		return
	}
	var req amountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	res, err := h.svc.Capture(id, req.Amount)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// ReverseReservation releases the remaining hold.
func (h *Handlers) ReverseReservation(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" || id == "sweep" {
		respondError(w, http.StatusBadRequest, "invalid reservation ID")
		return
	}
	res, err := h.svc.Reverse(id)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

type sweepRequest struct {
	Now string `json:"now,omitempty"`
}

// SweepReservations expires holds past their TTL.
func (h *Handlers) SweepReservations(w http.ResponseWriter, r *http.Request) {
	var req sweepRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	now := time.Now().UTC()
	if req.Now != "" {
		t, err := time.Parse(time.RFC3339, req.Now)
		if err != nil {
			respondError(w, http.StatusBadRequest, "now must be RFC3339")
			return
		}
		now = t
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"expired": h.svc.SweepExpired(now)})
}

// SettleLate settles an expired hold once, flagging it for review.
func (h *Handlers) SettleLate(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" || id == "sweep" {
		respondError(w, http.StatusBadRequest, "invalid reservation ID")
		return
	}
	var req amountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Amount <= 0 {
		respondError(w, http.StatusBadRequest, "amount must be positive")
		return
	}
	res, err := h.svc.SettleLate(id, req.Amount)
	if err != nil {
		writeGuardError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, res)
}

// GetBalances reports ledger/reserved/available for ?account=.
func (h *Handlers) GetBalances(w http.ResponseWriter, r *http.Request) {
	account := r.URL.Query().Get("account")
	if account == "" {
		respondError(w, http.StatusBadRequest, "account query parameter is required")
		return
	}
	respondJSON(w, http.StatusOK, h.svc.Balances(account))
}

type fundRequest struct {
	Account string `json:"account"`
	Balance int64  `json:"balance"`
}

// FundBalance sets the engine ledger balance for an account.
func (h *Handlers) FundBalance(w http.ResponseWriter, r *http.Request) {
	var req fundRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Account == "" {
		respondError(w, http.StatusBadRequest, "account is required")
		return
	}
	if req.Balance < 0 {
		respondError(w, http.StatusBadRequest, "balance must be non-negative")
		return
	}
	h.svc.Fund(req.Account, req.Balance)
	respondJSON(w, http.StatusOK, h.svc.Balances(req.Account))
}
