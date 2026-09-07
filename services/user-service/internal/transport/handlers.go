package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/user-service/internal/domain"
	"github.com/nexora/nexora/services/user-service/internal/service"
)

type Handlers struct {
	userService *service.UserService
	logger      zerolog.Logger
}

func NewHandlers(userService *service.UserService, logger zerolog.Logger) *Handlers {
	return &Handlers{
		userService: userService,
		logger:      logger,
	}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/users/me", h.GetMe).Methods("GET")
	router.HandleFunc("/v1/users/me", h.UpdateMe).Methods("PUT")
	router.HandleFunc("/v1/users/{id}", h.GetUser).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) GetMe(w http.ResponseWriter, r *http.Request) {
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

	user, err := h.userService.GetUserByID(r.Context(), userID)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, user)
}

func (h *Handlers) UpdateMe(w http.ResponseWriter, r *http.Request) {
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

	var req domain.UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := h.userService.UpdateUser(r.Context(), userID, &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, user)
}

func (h *Handlers) GetUser(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := uuid.Parse(vars["id"])
	if err != nil {
		respondError(w, http.StatusBadRequest, "invalid user ID")
		return
	}

	// Ownership: a user may only read their own profile. The X-User-ID
	// header is set by the auth middleware from the verified JWT subject,
	// never from client input.
	callerIDStr := r.Header.Get("X-User-ID")
	callerID, err := uuid.Parse(callerIDStr)
	if err != nil || callerID != id {
		respondError(w, http.StatusForbidden, "you can only access your own profile")
		return
	}

	user, err := h.userService.GetUserByID(r.Context(), id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, user)
}
