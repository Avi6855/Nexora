package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/replay-service/internal/domain"
)

type ReplayRepository interface {
	StoreReplayResult(ctx context.Context, result *domain.ReplayResult) error
	GetReplayResult(ctx context.Context, id uuid.UUID) (*domain.ReplayResult, error)
}
