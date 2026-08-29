package repository

import (
	"context"
	"sync"
	"time"

	"github.com/nexora/nexora/services/control-plane-service/internal/domain"
)

type HealthRepository interface {
	UpdateServiceHealth(ctx context.Context, serviceName string, health *domain.ServiceHealth) error
	GetServiceHealth(ctx context.Context, serviceName string) (*domain.ServiceHealth, error)
	GetAllServiceHealth(ctx context.Context) (map[string]*domain.ServiceHealth, error)
}

type inMemoryHealthRepository struct {
	mu       sync.RWMutex
	services map[string]*domain.ServiceHealth
}

func NewInMemoryHealthRepository() HealthRepository {
	return &inMemoryHealthRepository{
		services: make(map[string]*domain.ServiceHealth),
	}
}

func (r *inMemoryHealthRepository) UpdateServiceHealth(ctx context.Context, serviceName string, health *domain.ServiceHealth) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	health.CheckedAt = time.Now().UTC()
	r.services[serviceName] = health
	return nil
}

func (r *inMemoryHealthRepository) GetServiceHealth(ctx context.Context, serviceName string) (*domain.ServiceHealth, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	health, ok := r.services[serviceName]
	if !ok {
		return &domain.ServiceHealth{
			ServiceName: serviceName,
			Status:      domain.HealthStatusUnhealthy,
			CheckedAt:   time.Now().UTC(),
		}, nil
	}

	return health, nil
}

func (r *inMemoryHealthRepository) GetAllServiceHealth(ctx context.Context) (map[string]*domain.ServiceHealth, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*domain.ServiceHealth, len(r.services))
	for k, v := range r.services {
		copy := *v
		result[k] = &copy
	}

	return result, nil
}
