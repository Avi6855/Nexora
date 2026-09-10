// Package idev wires the shared/identity platform (progressive account
// recovery, device trust graph, passkey-first security) into
// identity-service. The shared types own the security invariants (score
// ceilings, distrust cascade, single-use challenges); this package owns
// session identity (UUIDs), lookup (404s) and mutual exclusion around the
// non-thread-safe shared engines.
package idev

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/nexora/nexora/shared/identity"
)

// ErrNotFound is returned for unknown recovery sessions, devices and challenges.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned for duplicates and illegal state transitions.
var ErrConflict = errors.New("conflict")

// Service holds the recovery engine, trust graph, passkey store and the
// recovery-session table. Devices live inside the TrustGraph itself.
type Service struct {
	mu       sync.Mutex
	engine   *identity.RecoveryEngine
	graph    *identity.TrustGraph
	store    *identity.PasskeyStore
	sessions map[string]*identity.RecoverySession
}

// NewService constructs a Service under the default recovery policy.
func NewService() *Service {
	return &Service{
		engine:   identity.NewRecoveryEngine(identity.DefaultRecoveryPolicy()),
		graph:    identity.NewTrustGraph(),
		store:    identity.NewPasskeyStore(),
		sessions: map[string]*identity.RecoverySession{},
	}
}

// StartRecovery opens a recovery session for an account.
func (s *Service) StartRecovery(accountID string) (string, *identity.RecoverySession, error) {
	if accountID == "" {
		return "", nil, fmt.Errorf("account_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.engine.StartRecovery(accountID, time.Now().UTC())
	id := uuid.NewString()
	s.sessions[id] = sess
	return id, sess, nil
}

// GetSession returns a recovery session by ID.
func (s *Service) GetSession(sessionID string) (*identity.RecoverySession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: recovery session %s", ErrNotFound, sessionID)
	}
	return sess, nil
}

// PresentSignal adds one evidence signal to a session.
func (s *Service) PresentSignal(sessionID string, kind identity.SignalKind) (*identity.RecoverySession, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("session id is required")
	}
	if kind == "" {
		return nil, fmt.Errorf("signal kind is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("%w: recovery session %s", ErrNotFound, sessionID)
	}
	if err := s.engine.Present(sess, kind); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return sess, nil
}

// EvaluateRecovery decides a session under the policy.
func (s *Service) EvaluateRecovery(sessionID string) (bool, string, error) {
	if sessionID == "" {
		return false, "", fmt.Errorf("session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionID]
	if !ok {
		return false, "", fmt.Errorf("%w: recovery session %s", ErrNotFound, sessionID)
	}
	return s.engine.Evaluate(sess)
}

// RegisterDevice adds a NEW device to the trust graph.
func (s *Service) RegisterDevice(deviceID string) (*identity.Device, error) {
	if deviceID == "" {
		return nil, fmt.Errorf("device_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.graph.State(deviceID); err == nil {
		return nil, fmt.Errorf("%w: device %s already registered", ErrConflict, deviceID)
	}
	return s.graph.Register(deviceID, time.Now().UTC())
}

// LinkDevices records a trust relationship between two devices.
func (s *Service) LinkDevices(from, to, kind string) error {
	if from == "" || to == "" {
		return fmt.Errorf("both device ids are required")
	}
	if kind == "" {
		return fmt.Errorf("link kind is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.graph.Link(from, to, kind)
}

// TrustDevice marks a device trusted (propagates to MFA-paired NEW devices).
func (s *Service) TrustDevice(id string) error {
	if id == "" {
		return fmt.Errorf("device id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.graph.Trust(id, time.Now().UTC()); err != nil {
		if errors.Is(err, identity.ErrUnknownDevice) {
			return fmt.Errorf("%w: device %s", ErrNotFound, id)
		}
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return nil
}

// FlagDevice marks a device suspicious (distrust propagates).
func (s *Service) FlagDevice(id string) error {
	if id == "" {
		return fmt.Errorf("device id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.graph.MarkSuspicious(id, time.Now().UTC()); err != nil {
		if errors.Is(err, identity.ErrUnknownDevice) {
			return fmt.Errorf("%w: device %s", ErrNotFound, id)
		}
		return err
	}
	return nil
}

// RevokeDevice permanently revokes a device (cascade along MFA pairings).
func (s *Service) RevokeDevice(id string) error {
	if id == "" {
		return fmt.Errorf("device id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.graph.Revoke(id, time.Now().UTC()); err != nil {
		if errors.Is(err, identity.ErrUnknownDevice) {
			return fmt.Errorf("%w: device %s", ErrNotFound, id)
		}
		return err
	}
	return nil
}

// ExpireDevices marks devices unseen within maxAge as EXPIRED.
func (s *Service) ExpireDevices(maxAge time.Duration) []string {
	if maxAge <= 0 {
		maxAge = 90 * 24 * time.Hour
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.graph.ExpireOld(time.Now().UTC(), maxAge)
}

// DeviceState returns the current trust state of a device.
func (s *Service) DeviceState(id string) (identity.DeviceState, error) {
	if id == "" {
		return "", fmt.Errorf("device id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.graph.State(id)
	if err != nil {
		return "", fmt.Errorf("%w: device %s", ErrNotFound, id)
	}
	return st, nil
}

// IssueChallenge creates a single-use short-TTL passkey challenge.
func (s *Service) IssueChallenge(accountID string) (string, error) {
	if accountID == "" {
		return "", fmt.Errorf("account_id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.IssueChallenge(accountID, time.Now().UTC())
}

// ConsumeChallenge redeems a challenge exactly once.
func (s *Service) ConsumeChallenge(challengeID, accountID string) error {
	if challengeID == "" || accountID == "" {
		return fmt.Errorf("challenge id and account_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ConsumeChallenge(challengeID, accountID, time.Now().UTC())
}

// EnrolRecovery adds a recovery credential (at most two per account).
func (s *Service) EnrolRecovery(accountID, credID string) error {
	if accountID == "" || credID == "" {
		return fmt.Errorf("account_id and credential_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.EnrolRecoveryCredential(accountID, credID, time.Now().UTC()); err != nil {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return nil
}

// RotateRecovery replaces one credential with a successor atomically.
func (s *Service) RotateRecovery(accountID, oldID, newID string) error {
	if accountID == "" || oldID == "" || newID == "" {
		return fmt.Errorf("account_id, old_credential_id and new_credential_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.RotateRecoveryCredential(accountID, oldID, newID, time.Now().UTC()); err != nil {
		if errors.Is(err, identity.ErrLastCredential) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return nil
}

// UseRecovery redeems a recovery credential.
func (s *Service) UseRecovery(accountID, credID string) error {
	if accountID == "" || credID == "" {
		return fmt.Errorf("account_id and credential_id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.store.UseRecoveryCredential(accountID, credID, time.Now().UTC()); err != nil {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return nil
}

// ValidRecoveryCount reports live recovery paths for an account.
func (s *Service) ValidRecoveryCount(accountID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.ValidRecoveryCount(accountID, time.Now().UTC())
}
