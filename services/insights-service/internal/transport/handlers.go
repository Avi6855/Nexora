package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/rs/zerolog"

	"github.com/nexora/nexora/services/insights-service/internal/service"
)

// Handlers exposes the insights read API.
type Handlers struct {
	insights *service.IntelligenceService
	logger   zerolog.Logger
}

// NewHandlers builds the HTTP handlers.
func NewHandlers(insights *service.IntelligenceService, logger zerolog.Logger) *Handlers {
	return &Handlers{insights: insights, logger: logger}
}

// RegisterRoutes wires the routes.
func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/insights/subscriptions", h.Subscriptions).Methods("GET")
	router.HandleFunc("/v1/insights/safe-to-spend", h.SafeToSpend).Methods("GET")
	router.HandleFunc("/v1/insights/alerts", h.Alerts).Methods("GET")
	router.HandleFunc("/v1/insights/salary/status", h.SalaryStatus).Methods("GET")
	router.HandleFunc("/v1/insights/runway", h.Runway).Methods("GET")
	router.HandleFunc("/v1/insights/health", h.Health).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// callerID resolves the authenticated user (auth middleware) and the
// account_id query parameter required by every insights endpoint.
func callerID(r *http.Request) (userID, accountID string, ok bool) {
	userID = r.Header.Get("X-User-ID")
	accountID = r.URL.Query().Get("account_id")
	if userID == "" || accountID == "" {
		return "", "", false
	}
	return userID, accountID, true
}

func (h *Handlers) Subscriptions(w http.ResponseWriter, r *http.Request) {
	userID, accountID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusBadRequest, "X-User-ID header and account_id query parameter are required")
		return
	}
	_ = userID

	subs, err := h.insights.GetSubscriptions(r.Context(), mustParse(accountID))
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subs == nil {
		subs = make([]*domainSubscription, 0)
	}
	respondJSON(w, http.StatusOK, subs)
}

func (h *Handlers) SafeToSpend(w http.ResponseWriter, r *http.Request) {
	userID, accountID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusBadRequest, "X-User-ID header and account_id query parameter are required")
		return
	}
	_ = userID

	sts, err := h.insights.GetSafeToSpend(r.Context(), mustParse(accountID))
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, sts)
}

func (h *Handlers) Alerts(w http.ResponseWriter, r *http.Request) {
	userID, accountID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusBadRequest, "X-User-ID header and account_id query parameter are required")
		return
	}
	_ = userID
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	alerts, err := h.insights.GetAlerts(r.Context(), mustParse(accountID), limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if alerts == nil {
		alerts = make([]*domainAlert, 0)
	}
	respondJSON(w, http.StatusOK, alerts)
}

// SalaryStatus reports the income lifecycle: growth, expected payday,
// lateness. Returns 404 when no salary has been detected yet.
func (h *Handlers) SalaryStatus(w http.ResponseWriter, r *http.Request) {
	userID, accountID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusBadRequest, "X-User-ID header and account_id query parameter are required")
		return
	}
	_ = userID

	status, err := h.insights.GetSalaryStatus(r.Context(), mustParse(accountID))
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if status == nil {
		respondError(w, http.StatusNotFound, "no salary detected for this account yet")
		return
	}
	respondJSON(w, http.StatusOK, status)
}

// Runway is the what-if simulator: how long the balance lasts with zero
// income, essentials-only, and under an optional scenario (extra_monthly,
// horizon_months query params).
func (h *Handlers) Runway(w http.ResponseWriter, r *http.Request) {
	userID, accountID, ok := callerID(r)
	if !ok {
		respondError(w, http.StatusBadRequest, "X-User-ID header and account_id query parameter are required")
		return
	}
	_ = userID

	extraMonthly, _ := strconv.ParseInt(r.URL.Query().Get("extra_monthly"), 10, 64)
	horizonMonths, _ := strconv.Atoi(r.URL.Query().Get("horizon_months"))
	if extraMonthly < 0 || horizonMonths < 0 {
		respondError(w, http.StatusBadRequest, "extra_monthly and horizon_months must be non-negative")
		return
	}

	result, err := h.insights.GetRunway(r.Context(), mustParse(accountID), extraMonthly, horizonMonths)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func mustParse(s string) uuid.UUID {
	id, _ := uuid.Parse(s)
	return id
}
