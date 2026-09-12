package moneymove

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Lease is one fencing-token lease over a resource.
type Lease struct {
	ID        string    `json:"id"`
	Resource  string    `json:"resource"`
	Token     uint64    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Acquire takes a lease on resource for ttl, returning a fencing token.
// A live holder blocks takeover (ErrLeaseHeld); an expired lease may be
// taken over with a higher fencing token.
func (s *Store) Acquire(resource string, ttl time.Duration) (*Lease, error) {
	return s.AcquireAt(resource, ttl, time.Now().UTC())
}

// AcquireAt is Acquire with an explicit clock for deterministic tests.
func (s *Store) AcquireAt(resource string, ttl time.Duration, now time.Time) (*Lease, error) {
	if resource == "" {
		return nil, fmt.Errorf("resource is required")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if holderID, ok := s.resourceHeld[resource]; ok {
		if held, exists := s.leases[holderID]; exists && now.Before(held.ExpiresAt) {
			return nil, fmt.Errorf("resource %q: %w", resource, ErrLeaseHeld)
		}
	}
	s.fencing[resource]++
	lease := &Lease{
		ID:        uuid.NewString(),
		Resource:  resource,
		Token:     s.fencing[resource],
		ExpiresAt: now.Add(ttl),
	}
	s.leases[lease.ID] = lease
	s.resourceHeld[resource] = lease.ID
	s.logger.Info().Str("lease_id", lease.ID).Str("resource", resource).Uint64("token", lease.Token).Msg("moneymove lease acquired")
	cp := *lease
	return &cp, nil
}

// Renew extends a lease when the fencing token matches and the lease has not
// expired. Stale tokens are rejected so only the current holder can renew.
func (s *Store) Renew(id string, token uint64, ttl time.Duration) (*Lease, error) {
	return s.RenewAt(id, token, ttl, time.Now().UTC())
}

// RenewAt is Renew with an explicit clock.
func (s *Store) RenewAt(id string, token uint64, ttl time.Duration, now time.Time) (*Lease, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("ttl must be positive")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[id]
	if !ok {
		return nil, ErrLeaseNotFound
	}
	if lease.Token != token {
		return nil, ErrStaleToken
	}
	if !now.Before(lease.ExpiresAt) {
		return nil, ErrLeaseExpired
	}
	lease.ExpiresAt = now.Add(ttl)
	s.logger.Info().Str("lease_id", id).Uint64("token", token).Msg("moneymove lease renewed")
	cp := *lease
	return &cp, nil
}

// ExecuteUnderLease validates that token is the current fencing token for the
// lease and that the lease has not expired. Stale or expired tokens are
// rejected and must not execute.
func (s *Store) ExecuteUnderLease(id string, token uint64) (*Lease, error) {
	return s.ExecuteUnderLeaseAt(id, token, time.Now().UTC())
}

// ExecuteUnderLeaseAt is ExecuteUnderLease with an explicit clock.
func (s *Store) ExecuteUnderLeaseAt(id string, token uint64, now time.Time) (*Lease, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[id]
	if !ok {
		return nil, ErrLeaseNotFound
	}
	// A superseded lease (takeover installed a newer ID for the resource) is
	// stale even if its own expiry has not passed.
	if currentID, held := s.resourceHeld[lease.Resource]; held && currentID != lease.ID {
		if current, exists := s.leases[currentID]; exists && current.Token > lease.Token {
			return nil, ErrStaleToken
		}
		return nil, ErrStaleToken
	}
	if lease.Token != token {
		return nil, ErrStaleToken
	}
	if !now.Before(lease.ExpiresAt) {
		return nil, ErrLeaseExpired
	}
	s.logger.Info().Str("lease_id", id).Uint64("token", token).Msg("moneymove executed under lease")
	cp := *lease
	return &cp, nil
}

// GetLease returns a copy of a lease.
func (s *Store) GetLease(id string) (*Lease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases[id]
	if !ok {
		return nil, ErrLeaseNotFound
	}
	cp := *lease
	return &cp, nil
}
