// Package offlineops wires the shared offline-first intents + conflict
// resolution library into transfer-service as a live HTTP surface.
package offlineops

import (
	"github.com/nexora/nexora/shared/offline"
)

// Service is the offline live state: one intent store plus one resolver.
type Service struct {
	store    *offline.IntentStore
	resolver *offline.Resolver
}

// NewService returns an empty offline service.
func NewService() *Service {
	return &Service{
		store:    offline.NewIntentStore(),
		resolver: offline.NewResolver(),
	}
}

// RegisterDevice enrols a device secret.
func (s *Service) RegisterDevice(deviceID, secret string) error {
	return s.store.RegisterDevice(deviceID, secret)
}

// QueueIntent stores an offline intent blob for later sync.
func (s *Service) QueueIntent(deviceID string, intent offline.SignedIntent) error {
	return s.store.QueueIntent(deviceID, intent)
}

// Sync commits a device's queue in seq order.
func (s *Service) Sync(deviceID string) ([]offline.SyncResult, error) {
	return s.store.Sync(deviceID)
}

// Pending returns the queued count for a device.
func (s *Service) Pending(deviceID string) int {
	return s.store.Pending(deviceID)
}

// Resolve decides between two concurrent versioned ops.
func (s *Service) Resolve(local, remote offline.VersionedOp) (offline.Resolution, error) {
	return s.resolver.Resolve(local, remote)
}
