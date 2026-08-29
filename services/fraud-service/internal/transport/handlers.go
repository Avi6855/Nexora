package transport

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/rs/zerolog"
	"github.com/nexora/nexora/services/fraud-service/internal/domain"
	"github.com/nexora/nexora/services/fraud-service/internal/service"
)

type Handlers struct {
	fraudService *service.FraudService
	logger       zerolog.Logger
}

func NewHandlers(fraudService *service.FraudService, logger zerolog.Logger) *Handlers {
	return &Handlers{fraudService: fraudService, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/fraud/analyze", h.AnalyzePaymentRisk).Methods("POST")
	router.HandleFunc("/v1/fraud/analyze/full", h.AnalyzePaymentFull).Methods("POST")
	router.HandleFunc("/v1/fraud/health", h.Health).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) AnalyzePaymentRisk(w http.ResponseWriter, r *http.Request) {
	var req domain.AnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := h.fraudService.AnalyzePaymentRisk(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handlers) AnalyzePaymentFull(w http.ResponseWriter, r *http.Request) {
	var req domain.AnalyzePaymentFullRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	resp, err := h.fraudService.AnalyzePayment(r.Context(), &req)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, resp)
}

func (h *Handlers) Health(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
