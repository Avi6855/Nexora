package transport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/nexora/nexora/services/identity-service/internal/domain"
	"github.com/nexora/nexora/services/identity-service/internal/service"
	domainerrors "github.com/nexora/nexora/shared/errors"
	"github.com/rs/zerolog"
)

type Handlers struct {
	authService *service.AuthService
	logger      zerolog.Logger
}

func NewHandlers(authService *service.AuthService, logger zerolog.Logger) *Handlers {
	return &Handlers{
		authService: authService,
		logger:      logger,
	}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/auth/register", h.Register).Methods("POST")
	router.HandleFunc("/v1/auth/login", h.Login).Methods("POST")
	router.HandleFunc("/v1/auth/otp/verify", h.VerifyOTP).Methods("POST")
	router.HandleFunc("/v1/auth/refresh", h.RefreshToken).Methods("POST")
	router.HandleFunc("/v1/auth/logout", h.Logout).Methods("POST")
	router.HandleFunc("/v1/auth/device", h.RegisterDevice).Methods("POST")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

// clientError writes a generic message at the mapped status so responses never
// disclose whether an account exists or why credentials were rejected.
func clientError(w http.ResponseWriter, err error) {
	var de *domainerrors.DomainError
	status := http.StatusInternalServerError
	if errors.As(err, &de) {
		status = de.HTTPStatus
	}
	if status >= 500 {
		respondError(w, status, "something went wrong, please try again")
		return
	}
	switch {
	case errors.Is(err, domainerrors.ErrRateLimited):
		respondError(w, http.StatusTooManyRequests, "too many attempts, please try again later")
	case errors.Is(err, domainerrors.ErrUnauthorized):
		respondError(w, http.StatusUnauthorized, "invalid credentials")
	default:
		respondError(w, status, "request could not be completed")
	}
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	var req domain.RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	user, err := h.authService.Register(r.Context(), &req)
	if err != nil {
		// Duplicate addresses and validation failures return 400/409 with a
		// generic body — never "email already registered".
		clientError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, user)
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	var req domain.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Email == "" || req.Password == "" {
		respondError(w, http.StatusBadRequest, "email and password are required")
		return
	}

	ctx := service.WithRemoteIP(r.Context(), service.ClientIP(r.RemoteAddr))
	tokens, err := h.authService.Login(ctx, &req)
	if err != nil {
		clientError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, tokens)
}

func (h *Handlers) VerifyOTP(w http.ResponseWriter, r *http.Request) {
	var req domain.OTPVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Code == "" {
		respondError(w, http.StatusBadRequest, "OTP code is required")
		return
	}

	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	ctx := service.WithRemoteIP(r.Context(), service.ClientIP(r.RemoteAddr))
	tokens, err := h.authService.VerifyOTP(ctx, userID, req.Code, domain.OTPPurpose(req.Purpose), req.DeviceID)
	if err != nil {
		clientError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, tokens)
}

func (h *Handlers) RefreshToken(w http.ResponseWriter, r *http.Request) {
	var req domain.RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RefreshToken == "" {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	tokens, err := h.authService.RefreshToken(r.Context(), req.RefreshToken)
	if err != nil {
		clientError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, tokens)
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	var req domain.LogoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if req.RefreshToken == "" && req.DeviceID == "" {
		respondError(w, http.StatusBadRequest, "refresh_token or device_id is required")
		return
	}

	if err := h.authService.Logout(r.Context(), userID, req.RefreshToken, req.DeviceID); err != nil {
		clientError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
}

func (h *Handlers) RegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req domain.DeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	userIDStr := r.Header.Get("X-User-ID")
	if userIDStr == "" {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		respondError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	device, err := h.authService.RegisterDevice(r.Context(), userID, &req)
	if err != nil {
		clientError(w, err)
		return
	}

	respondJSON(w, http.StatusCreated, device)
}
