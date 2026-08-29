package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/simulation-service/internal/domain"
)

type SimulationRepository interface {
	StoreSimulationResult(ctx context.Context, result *domain.SimulationResult) error
	GetSimulationResult(ctx context.Context, id uuid.UUID) (*domain.SimulationResult, error)
}
