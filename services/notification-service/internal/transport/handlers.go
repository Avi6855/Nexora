package transport

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

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

	// Mobile realtime surface: SSE stream + home-screen feed (both real data).
	router.HandleFunc("/v1/stream", h.Stream).Methods("GET")
	router.HandleFunc("/v1/feed", h.GetFeed).Methods("GET")
}

// resolveUserID accepts X-User-ID (app headers) or ?user_id= (SSE/curl).
func resolveUserID(r *http.Request) (uuid.UUID, error) {
	raw := r.Header.Get("X-User-ID")
	if raw == "" {
		raw = r.URL.Query().Get("user_id")
	}
	if raw == "" {
		return uuid.Nil, strconv.ErrSyntax
	}
	return uuid.Parse(raw)
}

func (h *Handlers) GetFeed(w http.ResponseWriter, r *http.Request) {
	userID, err := resolveUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "X-User-ID header or user_id query param is required")
		return
	}
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := h.notifService.GetFeed(r.Context(), userID, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, items)
}

// Stream is the Server-Sent Events endpoint the Android app keeps open. Every
// event the notification service persists for the user (card auths, captures
// with the live balance, security alerts) is pushed here in real time.
func (h *Handlers) Stream(w http.ResponseWriter, r *http.Request) {
	userID, err := resolveUserID(r)
	if err != nil {
		respondError(w, http.StatusBadRequest, "user_id query param is required")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	h.logger.Info().Str("user_id", userID.String()).Msg("SSE client connected")

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "event: ready\ndata: {\"user_id\":\"%s\"}\n\n", userID.String())
	flusher.Flush()

	ch, closeFn := h.notifService.Subscribe(userID)
	defer closeFn()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			h.logger.Info().Str("user_id", userID.String()).Msg("SSE client disconnected")
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case payload := <-ch:
			fmt.Fprintf(w, "event: feed_item\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
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
