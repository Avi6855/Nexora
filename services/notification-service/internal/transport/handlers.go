package transport

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/notification-service/internal/domain"
	"github.com/nexora/nexora/services/notification-service/internal/service"
)

type Handlers struct {
	notifService *service.NotificationService
	logger       zerolog.Logger
}

func NewHandlers(notifService *service.NotificationService, logger zerolog.Logger) *Handlers {
	return &Handlers{notifService: notifService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/notifications", h.SendNotification).Methods("POST")
	router.HandleFunc("/v1/notifications", h.GetNotifications).Methods("GET")
	router.HandleFunc("/v1/notifications/{id}", h.GetNotification).Methods("GET")
	router.HandleFunc("/v1/notifications/{id}/read", h.MarkAsRead).Methods("POST")
	router.HandleFunc("/v1/notifications/events/payment", h.ConsumePaymentEvent).Methods("POST")
	router.HandleFunc("/v1/notifications/events/fraud", h.ConsumeFraudEvent).Methods("POST")
	router.HandleFunc("/v1/notifications/events/security", h.ConsumeSecurityEvent).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) SendNotification(w http.ResponseWriter, r *http.Request) {
	var req domain.SendNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	notif, err := h.notifService.SendNotification(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, notif)
}

func (h *Handlers) GetNotifications(w http.ResponseWriter, r *http.Request) {
	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		respondError(w, http.StatusBadRequest, "X-User-ID header is required")
		return
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid user ID")
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}
	notifications, err := h.notifService.GetNotificationsByUser(r.Context(), userID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, notifications)
}

func (h *Handlers) GetNotification(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid notification ID")
		return
	}
	notif, err := h.notifService.GetNotification(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, notif)
}

func (h *Handlers) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid notification ID")
		return
	}
	if err := h.notifService.MarkAsRead(r.Context(), id); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "marked as read"})
}

func (h *Handlers) ConsumePaymentEvent(w http.ResponseWriter, r *http.Request) {
	var event domain.NotificationEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.notifService.ConsumePaymentEvent(r.Context(), &event); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "event consumed"})
}

func (h *Handlers) ConsumeFraudEvent(w http.ResponseWriter, r *http.Request) {
	var event domain.NotificationEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.notifService.ConsumeFraudEvent(r.Context(), &event); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "event consumed"})
}

func (h *Handlers) ConsumeSecurityEvent(w http.ResponseWriter, r *http.Request) {
	var event domain.NotificationEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.notifService.ConsumeSecurityEvent(r.Context(), &event); err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "event consumed"})
}
