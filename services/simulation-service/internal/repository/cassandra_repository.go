package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/simulation-service/internal/domain"
)

type cassandraSimulationRepository struct {
	session *gocql.Session
}

func NewCassandraSimulationRepository(session *gocql.Session) SimulationRepository {
	return &cassandraSimulationRepository{session: session}
}

func (r *cassandraSimulationRepository) StoreSimulationResult(ctx context.Context, result *domain.SimulationResult) error {
	query := `INSERT INTO simulation_results (
		result_id, would_succeed, predicted_balance, predicted_available,
		risk_score, reasons, simulated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		uuid.New(),
		result.WouldSucceed,
		result.PredictedBalance,
		result.PredictedAvailable,
		result.RiskScore,
		"",
		result.SimulatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraSimulationRepository) GetSimulationResult(ctx context.Context, id uuid.UUID) (*domain.SimulationResult, error) {
	var result domain.SimulationResult
	var reasons string

	query := `SELECT result_id, would_succeed, predicted_balance, predicted_available,
		risk_score, reasons, simulated_at
		FROM simulation_results WHERE result_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&id,
		&result.WouldSucceed,
		&result.PredictedBalance,
		&result.PredictedAvailable,
		&result.RiskScore,
		&reasons,
		&result.SimulatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("simulation result not found")
	}
	if err != nil {
		return nil, err
	}

	return &result, nil
}
