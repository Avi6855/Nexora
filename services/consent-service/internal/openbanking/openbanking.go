// Package openbanking wires shared/openbanking into consent-service:
// broken-connection auto-recovery, dataset freshness SLAs and the provider
// capability matrix, held as service state behind one mutex.
package openbanking

import (
	"fmt"
	"sync"
	"time"

	shared "github.com/nexora/nexora/shared/openbanking"
)

// Service holds per-connection recovery trackers, a freshness checker with
// default SLAs and a provider capability registry.
type Service struct {
	mu        sync.Mutex
	trackers  map[string]*shared.RecoveryTracker
	plans     map[string]shared.RecoveryPlan
	freshness *shared.FreshnessChecker
	lastGood  map[string]time.Time
	datasets  map[string]struct{}
	providers map[string]*shared.ProviderCapabilities
}

// NewService builds the service with default SLAs (balance 5m, transactions 30m).
func NewService() *Service {
	f := shared.NewFreshnessChecker()
	f.SetSLA("balance", 5*time.Minute)
	f.SetSLA("transactions", 30*time.Minute)
	return &Service{
		trackers:  map[string]*shared.RecoveryTracker{},
		plans:     map[string]shared.RecoveryPlan{},
		freshness: f,
		lastGood:  map[string]time.Time{},
		datasets:  map[string]struct{}{"balance": {}, "transactions": {}},
		providers: map[string]*shared.ProviderCapabilities{},
	}
}

func validFailureKind(k shared.FailureKind) bool {
	switch k {
	case shared.FailAuthExpired, shared.FailConsentExpired, shared.FailProviderDown,
		shared.FailSchemaChanged, shared.FailRateLimited:
		return true
	}
	return false
}

func validCapability(c shared.Capability) bool {
	switch c {
	case shared.CapBalance, shared.CapTransactions, shared.CapPayments, shared.CapIdentity:
		return true
	}
	return false
}

// ReportFailure classifies a broken connection and (re)starts its recovery tracker.
func (s *Service) ReportFailure(connectionID string, kind shared.FailureKind, retryAfter time.Duration) (shared.RecoveryPlan, error) {
	if connectionID == "" {
		return shared.RecoveryPlan{}, fmt.Errorf("connection_id is required")
	}
	if !validFailureKind(kind) {
		return shared.RecoveryPlan{}, fmt.Errorf("unknown failure kind %q", kind)
	}
	if retryAfter < 0 {
		return shared.RecoveryPlan{}, fmt.Errorf("retry_after must not be negative")
	}
	plan := shared.ClassifyFailure(kind, retryAfter)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trackers[connectionID] = shared.NewRecoveryTracker(plan)
	s.plans[connectionID] = plan
	return plan, nil
}

// ShouldRetry reports whether another attempt is permitted and how long to wait.
func (s *Service) ShouldRetry(connectionID string, now time.Time) (bool, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[connectionID]
	if !ok {
		return false, 0, fmt.Errorf("unknown connection %s", connectionID)
	}
	ok, wait := t.ShouldRetry(now)
	return ok, wait, nil
}

// RecordAttempt logs one recovery attempt for a connection.
func (s *Service) RecordAttempt(connectionID string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[connectionID]
	if !ok {
		return fmt.Errorf("unknown connection %s", connectionID)
	}
	t.RecordAttempt(now)
	return nil
}

// ConnectionExhausted reports whether the recovery budget is spent.
func (s *Service) ConnectionExhausted(connectionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.trackers[connectionID]
	if !ok {
		return false, fmt.Errorf("unknown connection %s", connectionID)
	}
	return t.Exhausted(), nil
}

// SetSLA declares the freshness contract for a dataset.
func (s *Service) SetSLA(dataset string, maxAge time.Duration) error {
	if dataset == "" {
		return fmt.Errorf("dataset is required")
	}
	if maxAge <= 0 {
		return fmt.Errorf("max_age must be positive")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.freshness.SetSLA(dataset, maxAge)
	s.datasets[dataset] = struct{}{}
	return nil
}

// RecordRefresh records the last successful refresh for a dataset.
func (s *Service) RecordRefresh(dataset string, at time.Time) error {
	if dataset == "" {
		return fmt.Errorf("dataset is required")
	}
	if at.IsZero() {
		return fmt.Errorf("refresh time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastGood[dataset] = at
	return nil
}

// DatasetFreshness evaluates one dataset against its SLA using stored refresh state.
func (s *Service) DatasetFreshness(dataset string, now time.Time) shared.FreshnessReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.freshness.Evaluate(dataset, s.lastGood[dataset], now)
}

// DatasetFreshnessAt evaluates with an explicit last-good time (and records it).
func (s *Service) DatasetFreshnessAt(dataset string, lastGood, now time.Time) shared.FreshnessReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !lastGood.IsZero() {
		s.lastGood[dataset] = lastGood
	}
	return s.freshness.Evaluate(dataset, s.lastGood[dataset], now)
}

// ListFreshness evaluates every dataset with a declared SLA.
func (s *Service) ListFreshness(now time.Time) []shared.FreshnessReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]shared.FreshnessReport, 0, len(s.datasets))
	for ds := range s.datasets {
		out = append(out, s.freshness.Evaluate(ds, s.lastGood[ds], now))
	}
	return out
}

// OverallFreshness returns the worst verdict across all SLA datasets.
func (s *Service) OverallFreshness(now time.Time) (shared.FreshnessVerdict, []shared.FreshnessReport) {
	reports := s.ListFreshness(now)
	return shared.OverallVerdict(reports), reports
}

// RegisterProvider adds a provider with its declared capabilities.
func (s *Service) RegisterProvider(name string, declared []shared.Capability) error {
	if name == "" {
		return fmt.Errorf("provider name is required")
	}
	if len(declared) == 0 {
		return fmt.Errorf("at least one declared capability is required")
	}
	for _, c := range declared {
		if !validCapability(c) {
			return fmt.Errorf("unknown capability %q", c)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.providers[name]; exists {
		return fmt.Errorf("provider %s already registered", name)
	}
	s.providers[name] = shared.NewProviderCapabilities(name, declared)
	return nil
}

func (s *Service) lookupProvider(name string) (*shared.ProviderCapabilities, error) {
	p, ok := s.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %s", name)
	}
	return p, nil
}

// DeclareOutage marks a capability down until end.
func (s *Service) DeclareOutage(provider string, cap shared.Capability, until time.Time) error {
	if !validCapability(cap) {
		return fmt.Errorf("unknown capability %q", cap)
	}
	if until.IsZero() {
		return fmt.Errorf("outage end time is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.lookupProvider(provider)
	if err != nil {
		return err
	}
	p.DeclareOutage(cap, until)
	return nil
}

// RecordAuthFailure flags a capability as needing reauth.
func (s *Service) RecordAuthFailure(provider string, cap shared.Capability) error {
	if !validCapability(cap) {
		return fmt.Errorf("unknown capability %q", cap)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.lookupProvider(provider)
	if err != nil {
		return err
	}
	p.RecordAuthFailure(cap)
	return nil
}

// RecordSuccess clears outage/auth flags for a capability.
func (s *Service) RecordSuccess(provider string, cap shared.Capability) error {
	if !validCapability(cap) {
		return fmt.Errorf("unknown capability %q", cap)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.lookupProvider(provider)
	if err != nil {
		return err
	}
	p.RecordSuccess(cap)
	return nil
}

// MarkUnsupported records spec-vs-reality drift for a capability.
func (s *Service) MarkUnsupported(provider string, cap shared.Capability) error {
	if !validCapability(cap) {
		return fmt.Errorf("unknown capability %q", cap)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.lookupProvider(provider)
	if err != nil {
		return err
	}
	p.ObserveUnsupported(cap)
	return nil
}

// CheckCapability returns the runtime verdict for a capability.
func (s *Service) CheckCapability(provider string, cap shared.Capability, now time.Time) (shared.Availability, error) {
	if !validCapability(cap) {
		return "", fmt.Errorf("unknown capability %q", cap)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.lookupProvider(provider)
	if err != nil {
		return "", err
	}
	return p.Check(cap, now), nil
}

// CapabilityMatrix renders the provider x capability table.
func (s *Service) CapabilityMatrix(now time.Time) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*shared.ProviderCapabilities, 0, len(s.providers))
	for _, p := range s.providers {
		list = append(list, p)
	}
	return shared.MatrixSummary(list, now)
}
