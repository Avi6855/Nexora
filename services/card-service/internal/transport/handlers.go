package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/card-service/internal/domain"
	"github.com/nexora/nexora/services/card-service/internal/service"
)

type Handlers struct {
	cardService *service.CardService
	logger      zerolog.Logger
}

func NewHandlers(cardService *service.CardService, logger zerolog.Logger) *Handlers {
	return &Handlers{cardService: cardService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/cards", h.GetCards).Methods("GET")
	router.HandleFunc("/v1/cards", h.CreateCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}", h.GetCard).Methods("GET")
	router.HandleFunc("/v1/cards/{id}/freeze", h.FreezeCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/unfreeze", h.UnfreezeCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/block", h.BlockCard).Methods("POST")
	router.HandleFunc("/v1/cards/{id}/limits", h.UpdateLimits).Methods("PUT")
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

	card, err := h.cardService.GetCard(r.Context(), id)
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
