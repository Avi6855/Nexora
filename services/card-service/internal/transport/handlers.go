package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/service"
	"github.com/nexora/nexora/shared/telemetry"
	"github.com/rs/zerolog"
)

type Handlers struct {
	cardService   *service.CardService
	authService   *service.AuthorizationService
	authDecisions *telemetry.CounterVec
	logger        zerolog.Logger
}

func NewHandlers(cardService *service.CardService, authService *service.AuthorizationService, logger zerolog.Logger, reg *telemetry.Registry) *Handlers {
	h := &Handlers{cardService: cardService, authService: authService, logger: logger}
	if reg != nil {
		h.authDecisions = reg.Counter("card_authorization_decisions_total", "Real-time card authorization decisions, by outcome and reason.", "outcome", "reason")
	}
	return h
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/cards", h.GetCards).Methods("GET")
	router.HandleFunc("/v1/cards", h.CreateCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}", h.GetCard).Methods("GET")
	router.HandleFunc("/v1/cards/{id}/freeze", h.FreezeCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/unfreeze", h.UnfreezeCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/block", h.BlockCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/limits", h.UpdateLimits).Methods("PUT")
	router.HandleFunc("/v1/cards/{id}/controls", h.UpdateControls).Methods("PUT")

	// Real-time authorisation lifecycle.
	router.HandleFunc("/v1/cards/{id}/authorize", h.AuthorizeCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/authorizations", h.ListAuthorizations).Methods("GET")
	router.HandleFunc("/v1/cards/{id}/authorizations/{auth_id}", h.GetAuthorization).Methods("GET")
	router.HandleFunc("/v1/cards/{id}/authorizations/{auth_id}/capture", h.CaptureAuthorization).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/authorizations/{auth_id}/void", h.VoidAuthorization).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, domain.ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
	})
}

func parseUUIDParam(r *http.Request, paramName string) (uuid.UUID, error) {
	vars := mux.Vars(r)
	return uuid.Parse(vars[paramName])
}

func getUserID(r *http.Request) (uuid.UUID, error) {
	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		return uuid.Nil, errors.New("X-User-ID header is required")
	}
	return uuid.Parse(userIDStr)
}

func (h *Handlers) GetCards(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	cards, err := h.cardService.GetCardsByUser(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, cards)
}

func (h *Handlers) CreateCard(w http.ResponseWriter, r *http.Request) {
	userID, err := getUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req domain.CreateCardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid account ID")
		return
	}

	if req.CardType != domain.CardTypePhysical && req.CardType != domain.CardTypeVirtual {
		respondError(w, http.StatusBadRequest, "card_type must be PHYSICAL or VIRTUAL")
		return
	}

	if req.SpendingLimit <= 0 {
		respondError(w, http.StatusBadRequest, "spending_limit must be positive")
		return
	}

	if req.DailyLimit <= 0 {
		respondError(w, http.StatusBadRequest, "daily_limit must be positive")
		return
	}

	if req.MonthlyLimit <= 0 {
		respondError(w, http.StatusBadRequest, "monthly_limit must be positive")
		return
	}

	if req.Currency == "" {
		req.Currency = "GBP"
	}

	card, err := h.cardService.CreateCard(r.Context(), userID, accountID, req.CardType, req.SpendingLimit, req.DailyLimit, req.MonthlyLimit, req.Currency)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, card)
}

func (h *Handlers) GetCard(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}

	// User JWT callers may only see their own cards; internal callers keep
	// full access (merchant presentments come from the network, not a user).
	var card *domain.Card
	if caller, uerr := getUserID(r); uerr == nil && r.Header.Get("X-Internal-Token") == "" {
		card, err = h.cardService.GetCardForUser(r.Context(), id, caller)
	} else {
		card, err = h.cardService.GetCard(r.Context(), id)
	}
	if err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, card)
}

// UpdateControls applies the user's channel spending toggles (online, ATM,
// gambling block) to one of their own cards.
func (h *Handlers) UpdateControls(lw http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(lw, http.StatusBadRequest, "invalid card ID")
		return
	}

	caller, err := getUserID(r)
	if err != nil {
		respondError(lw, http.StatusBadRequest, err.Error())
		return
	}

	var req domain.UpdateControlsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(lw, http.StatusBadRequest, "invalid request body")
		return
	}

	card, err := h.cardService.UpdateChannelControls(r.Context(), id, caller, req.OnlineEnabled, req.ATMEnabled, req.GamblingBlockEnabled)
	if err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(lw, http.StatusNotFound, err.Error())
			return
		}
		respondError(lw, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(lw, http.StatusOK, card)
}

func (h *Handlers) FreezeCard(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}

	if err := h.cardService.FreezeCard(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrInvalidCardState) || errors.Is(err, domain.ErrCardAlreadyFrozen) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "card frozen"})
}

func (h *Handlers) UnfreezeCard(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}

	if err := h.cardService.UnfreezeCard(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrInvalidCardState) || errors.Is(err, domain.ErrCardAlreadyActive) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "card unfrozen"})
}

func (h *Handlers) BlockCard(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}

	if err := h.cardService.BlockCard(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrInvalidCardState) || errors.Is(err, domain.ErrCardAlreadyBlocked) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "card blocked"})
}

func (h *Handlers) AuthorizeCard(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}

	var req domain.AuthorizeCardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	auth, err := h.authService.AuthorizeCard(r.Context(), id, &req)
	if err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	status := http.StatusOK
	if auth.Status == domain.AuthStatusDeclined {
		status = http.StatusPaymentRequired // 402 signals a declined presentment
	}
	h.recordDecision(auth)
	respondJSON(w, status, auth)
}

// recordDecision exposes approved/declined authorization outcomes (with the
// real decline reason) on /metrics for the Prometheus dashboards.
func (h *Handlers) recordDecision(auth *domain.CardAuthorization) {
	if h.authDecisions == nil {
		return
	}
	reason := auth.DeclineReason
	if reason == "" {
		reason = "approved"
	}
	h.authDecisions.With(map[string]string{
		"outcome": string(auth.Decision),
		"reason":  reason,
	}).Inc()
}
func (h *Handlers) ListAuthorizations(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}
	auths, err := h.authService.GetAuthorizations(r.Context(), id, 50)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, auths)
}

func (h *Handlers) GetAuthorization(w http.ResponseWriter, r *http.Request) {
	cardID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}
	authID, err := parseUUIDParam(r, "auth_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid authorization ID")
		return
	}
	auth, err := h.authService.GetAuthorization(r.Context(), authID)
	if err != nil {
		if errors.Is(err, domain.ErrAuthNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if auth.CardID != cardID {
		respondError(w, http.StatusNotFound, "authorization not found for this card")
		return
	}
	respondJSON(w, http.StatusOK, auth)
}

func (h *Handlers) CaptureAuthorization(w http.ResponseWriter, r *http.Request) {
	cardID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}
	authID, err := parseUUIDParam(r, "auth_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid authorization ID")
		return
	}
	auth, err := h.authService.CaptureAuthorization(r.Context(), cardID, authID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAuthNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, domain.ErrAuthCardMismatch):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, domain.ErrAuthNotCapturable):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, auth)
}

func (h *Handlers) VoidAuthorization(w http.ResponseWriter, r *http.Request) {
	cardID, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}
	authID, err := parseUUIDParam(r, "auth_id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid authorization ID")
		return
	}
	auth, err := h.authService.VoidAuthorization(r.Context(), cardID, authID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrAuthNotFound):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, domain.ErrAuthCardMismatch):
			respondError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, domain.ErrAuthNotVoidable):
			respondError(w, http.StatusConflict, err.Error())
		default:
			respondError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, auth)
}

func (h *Handlers) UpdateLimits(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid card ID")
		return
	}

	var req domain.UpdateLimitsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.DailyLimit <= 0 {
		respondError(w, http.StatusBadRequest, "daily_limit must be positive")
		return
	}

	if req.MonthlyLimit <= 0 {
		respondError(w, http.StatusBadRequest, "monthly_limit must be positive")
		return
	}

	if err := h.cardService.UpdateSpendingLimits(r.Context(), id, req.DailyLimit, req.MonthlyLimit); err != nil {
		if errors.Is(err, domain.ErrCardNotFound) {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		if errors.Is(err, domain.ErrInvalidCardState) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "spending limits updated"})
}
