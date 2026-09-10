package mortgage

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	sharedmortgage "github.com/nexora/nexora/shared/mortgage"
)

var (
	ErrApplicationNotFound = errors.New("mortgage application not found")
	ErrOfferNotFound       = errors.New("mortgage offer not found")
	ErrAlreadyExists       = errors.New("already exists")
)

// Service is an in-memory mortgage journey store backed by shared/mortgage.
type Service struct {
	mu   sync.RWMutex
	apps map[string]*sharedmortgage.Application
	off  map[string]*sharedmortgage.Offer
}

// Progress is the underwriter view of one application.
type Progress struct {
	ApplicationID string                                  `json:"application_id"`
	Done          int                                     `json:"done"`
	Total         int                                     `json:"total"`
	Docs          map[string]*sharedmortgage.DocumentTask `json:"documents"`
}

func NewService() *Service {
	return &Service{
		apps: map[string]*sharedmortgage.Application{},
		off:  map[string]*sharedmortgage.Offer{},
	}
}

// EstimateAffordability delegates to shared ComputeAffordability.
func (s *Service) EstimateAffordability(in sharedmortgage.AffordabilityInputs) (*sharedmortgage.AffordabilityResult, error) {
	return sharedmortgage.ComputeAffordability(in)
}

// OpenApplication creates and stores a new application.
func (s *Service) OpenApplication(id string, kinds []string, deadline time.Time) (*sharedmortgage.Application, error) {
	if len(kinds) == 0 {
		return nil, errors.New("at least one document kind is required")
	}
	if id == "" {
		id = uuid.NewString()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apps[id]; ok {
		return nil, fmt.Errorf("application %s: %w", id, ErrAlreadyExists)
	}
	app := sharedmortgage.NewApplication(id, kinds, deadline)
	s.apps[id] = app
	return app, nil
}

func (s *Service) getApp(appID string) (*sharedmortgage.Application, error) {
	app, ok := s.apps[appID]
	if !ok {
		return nil, ErrApplicationNotFound
	}
	return app, nil
}

// UploadDocument marks a document uploaded and kicks off validation.
func (s *Service) UploadDocument(appID, kind string, now time.Time) error {
	if kind == "" {
		return errors.New("document kind is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.getApp(appID)
	if err != nil {
		return err
	}
	return app.Upload(kind, now)
}

// StartValidation begins provider validation for an uploaded document.
func (s *Service) StartValidation(appID, kind string, now time.Time) error {
	if kind == "" {
		return errors.New("document kind is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	app, err := s.getApp(appID)
	if err != nil {
		return err
	}
	return app.StartValidation(kind, now)
}

// RetryDue lists parked documents whose retry timer has elapsed.
func (s *Service) RetryDue(appID string, now time.Time) ([]string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, err := s.getApp(appID)
	if err != nil {
		return nil, err
	}
	due := app.DueForRetry(now)
	if due == nil {
		due = []string{}
	}
	return due, nil
}

// ApplicationProgress reports reviewed/total plus the document states.
func (s *Service) ApplicationProgress(appID string) (*Progress, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	app, err := s.getApp(appID)
	if err != nil {
		return nil, err
	}
	done, total := app.Completeness()
	docs := make(map[string]*sharedmortgage.DocumentTask, len(app.Docs))
	for k, v := range app.Docs {
		cp := *v
		docs[k] = &cp
	}
	return &Progress{ApplicationID: app.ID, Done: done, Total: total, Docs: docs}, nil
}

// CreateOffer stores a new mortgage offer.
func (s *Service) CreateOffer(id string, validTo time.Time, tasks []sharedmortgage.OfferTask) (*sharedmortgage.Offer, error) {
	if validTo.IsZero() {
		return nil, errors.New("valid_to is required")
	}
	if len(tasks) == 0 {
		return nil, errors.New("at least one offer task is required")
	}
	for _, t := range tasks {
		if t.Name == "" {
			return nil, errors.New("offer task name is required")
		}
		if t.Owner == "" {
			return nil, errors.New("offer task owner is required")
		}
	}
	if id == "" {
		id = uuid.NewString()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.off[id]; ok {
		return nil, fmt.Errorf("offer %s: %w", id, ErrAlreadyExists)
	}
	o := &sharedmortgage.Offer{ID: id, ValidTo: validTo, Tasks: append([]sharedmortgage.OfferTask(nil), tasks...)}
	s.off[id] = o
	return o, nil
}

// CompleteOfferTask marks one offer task done.
func (s *Service) CompleteOfferTask(offerID, taskName string) (*sharedmortgage.Offer, error) {
	if taskName == "" {
		return nil, errors.New("task name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.off[offerID]
	if !ok {
		return nil, ErrOfferNotFound
	}
	if err := o.MarkTaskDone(taskName); err != nil {
		return nil, err
	}
	return o, nil
}

// CheckExpiry evaluates the offer's expiry protection state.
func (s *Service) CheckExpiry(offerID string, now time.Time) (*sharedmortgage.ExpiryWarning, error) {
	if now.IsZero() {
		now = time.Now()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.off[offerID]
	if !ok {
		return nil, ErrOfferNotFound
	}
	return sharedmortgage.EvaluateExpiry(o, now), nil
}
