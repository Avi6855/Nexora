package mortgage

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	sharedmortgage "github.com/nexora/nexora/shared/mortgage"
	"github.com/rs/zerolog"
)

type Handlers struct {
	svc    *Service
	logger zerolog.Logger
}

func NewHandlers(svc *Service, logger zerolog.Logger) *Handlers {
	return &Handlers{svc: svc, logger: logger}
}

func (h *Handlers) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/v1/mortgages/affordability/estimate", h.EstimateAffordability).Methods("POST")
	router.HandleFunc("/v1/mortgages/applications", h.OpenApplication).Methods("POST")
	router.HandleFunc("/v1/mortgages/applications/{id}/documents", h.UploadDocument).Methods("POST")
	router.HandleFunc("/v1/mortgages/applications/{id}/validate", h.StartValidation).Methods("POST")
	router.HandleFunc("/v1/mortgages/applications/{id}/progress", h.ApplicationProgress).Methods("GET")
	router.HandleFunc("/v1/mortgages/applications/{id}/retries-due", h.RetryDue).Methods("GET")
	router.HandleFunc("/v1/mortgages/offers", h.CreateOffer).Methods("POST")
	router.HandleFunc("/v1/mortgages/offers/{id}/tasks", h.CompleteOfferTask).Methods("POST")
	router.HandleFunc("/v1/mortgages/offers/{id}/expiry", h.CheckExpiry).Methods("GET")
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func respondError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func (h *Handlers) EstimateAffordability(w http.ResponseWriter, r *http.Request) {
	var in sharedmortgage.AffordabilityInputs
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.svc.EstimateAffordability(in)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, res)
}

type openApplicationRequest struct {
	ID       string    `json:"id,omitempty"`
	Kinds    []string  `json:"kinds"`
	Deadline time.Time `json:"deadline,omitempty"`
}

func (h *Handlers) OpenApplication(w http.ResponseWriter, r *http.Request) {
	var req openApplicationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Kinds) == 0 {
		respondError(w, http.StatusBadRequest, "kinds is required")
		return
	}
	app, err := h.svc.OpenApplication(req.ID, req.Kinds, req.Deadline)
	if err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, app)
}

type docRequest struct {
	Kind string `json:"kind"`
}

func docErrStatus(err error) int {
	if errors.Is(err, ErrApplicationNotFound) {
		return http.StatusNotFound
	}
	msg := err.Error()
	if strings.Contains(msg, "no document") {
		return http.StatusNotFound
	}
	if strings.Contains(msg, "illegal") {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

func (h *Handlers) UploadDocument(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "application id is required")
		return
	}
	var req docRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" {
		respondError(w, http.StatusBadRequest, "kind is required")
		return
	}
	if err := h.svc.UploadDocument(id, req.Kind, time.Now()); err != nil {
		respondError(w, docErrStatus(err), err.Error())
		return
	}
	prog, err := h.svc.ApplicationProgress(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, prog)
}

func (h *Handlers) StartValidation(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if id == "" {
		respondError(w, http.StatusBadRequest, "application id is required")
		return
	}
	var req docRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Kind == "" {
		respondError(w, http.StatusBadRequest, "kind is required")
		return
	}
	if err := h.svc.StartValidation(id, req.Kind, time.Now()); err != nil {
		respondError(w, docErrStatus(err), err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": "validation started"})
}

func (h *Handlers) ApplicationProgress(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	prog, err := h.svc.ApplicationProgress(id)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, prog)
}

func (h *Handlers) RetryDue(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	due, err := h.svc.RetryDue(id, time.Now())
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]interface{}{"application_id": id, "due": due})
}

type createOfferRequest struct {
	ID      string                     `json:"id,omitempty"`
	ValidTo time.Time                  `json:"valid_to"`
	Tasks   []sharedmortgage.OfferTask `json:"tasks"`
}

func (h *Handlers) CreateOffer(w http.ResponseWriter, r *http.Request) {
	var req createOfferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	offer, err := h.svc.CreateOffer(req.ID, req.ValidTo, req.Tasks)
	if err != nil {
		if errors.Is(err, ErrAlreadyExists) {
			respondError(w, http.StatusConflict, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, offer)
}

type completeTaskRequest struct {
	Name string `json:"name"`
}

func (h *Handlers) CompleteOfferTask(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	var req completeTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		respondError(w, http.StatusBadRequest, "name is required")
		return
	}
	offer, err := h.svc.CompleteOfferTask(id, req.Name)
	if err != nil {
		if errors.Is(err, ErrOfferNotFound) || strings.Contains(err.Error(), "not found") {
			respondError(w, http.StatusNotFound, err.Error())
			return
		}
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, offer)
}

func (h *Handlers) CheckExpiry(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	now := time.Now()
	if qs := r.URL.Query().Get("now"); qs != "" {
		t, err := time.Parse(time.RFC3339, qs)
		if err != nil {
			respondError(w, http.StatusBadRequest, "invalid now query param, use RFC3339")
			return
		}
		now = t
	}
	warning, err := h.svc.CheckExpiry(id, now)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, warning)
}
