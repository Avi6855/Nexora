package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/nexora/nexora/services/fraud-service/internal/domain"
)

type FraudRepository interface {
	CreateAnalysis(ctx context.Context, analysis *domain.FraudAnalysis) error
	GetAnalysisByID(ctx context.Context, id uuid.UUID) (*domain.FraudAnalysis, error)
	GetAnalysesByPayment(ctx context.Context, paymentID uuid.UUID) ([]*domain.FraudAnalysis, error)
	GetAnalysesByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.FraudAnalysis, error)
}
