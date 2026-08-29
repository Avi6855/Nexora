package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/fraud-service/internal/domain"
)

type cassandraFraudRepository struct {
	session *gocql.Session
}

func NewCassandraFraudRepository(session *gocql.Session) FraudRepository {
	return &cassandraFraudRepository{session: session}
}

func (r *cassandraFraudRepository) CreateAnalysis(ctx context.Context, analysis *domain.FraudAnalysis) error {
	query := `INSERT INTO fraud_analyses (
		analysis_id, payment_id, user_id, account_id, risk_score, risk_action, risk_level,
		risk_reasons, device_id, ip_address, geo_location, signals, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	reasonsJSON := analysis.RiskReasons
	signalsJSON := ""

	return r.session.Query(query,
		analysis.AnalysisID,
		analysis.PaymentID,
		analysis.UserID,
		analysis.AccountID,
		analysis.RiskScore,
		string(analysis.RiskAction),
		string(analysis.RiskLevel),
		reasonsJSON,
		analysis.DeviceID,
		analysis.IPAddress,
		analysis.GeoLocation,
		signalsJSON,
		analysis.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *cassandraFraudRepository) GetAnalysisByID(ctx context.Context, id uuid.UUID) (*domain.FraudAnalysis, error) {
	var analysis domain.FraudAnalysis
	var riskAction, riskLevel, reasonsJSON, signalsJSON string

	query := `SELECT analysis_id, payment_id, user_id, account_id, risk_score, risk_action, risk_level,
		risk_reasons, device_id, ip_address, geo_location, signals, created_at
		FROM fraud_analyses WHERE analysis_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&analysis.AnalysisID,
		&analysis.PaymentID,
		&analysis.UserID,
		&analysis.AccountID,
		&analysis.RiskScore,
		&riskAction,
		&riskLevel,
		&reasonsJSON,
		&analysis.DeviceID,
		&analysis.IPAddress,
		&analysis.GeoLocation,
		&signalsJSON,
		&analysis.CreatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("fraud analysis not found")
	}
	if err != nil {
		return nil, err
	}

	analysis.RiskAction = domain.RiskAction(riskAction)
	analysis.RiskLevel = domain.RiskLevel(riskLevel)
	analysis.RiskReasons = reasonsJSON
	return &analysis, nil
}

func (r *cassandraFraudRepository) GetAnalysesByPayment(ctx context.Context, paymentID uuid.UUID) ([]*domain.FraudAnalysis, error) {
	var analyses []*domain.FraudAnalysis

	query := `SELECT analysis_id, payment_id, user_id, account_id, risk_score, risk_action, risk_level,
		risk_reasons, device_id, ip_address, geo_location, signals, created_at
		FROM fraud_analyses WHERE payment_id = ?`

	iter := r.session.Query(query, paymentID).WithContext(ctx).Iter()
	var analysis domain.FraudAnalysis
	var riskAction, riskLevel, reasonsJSON, signalsJSON string

	for iter.Scan(
		&analysis.AnalysisID,
		&analysis.PaymentID,
		&analysis.UserID,
		&analysis.AccountID,
		&analysis.RiskScore,
		&riskAction,
		&riskLevel,
		&reasonsJSON,
		&analysis.DeviceID,
		&analysis.IPAddress,
		&analysis.GeoLocation,
		&signalsJSON,
		&analysis.CreatedAt,
	) {
		analysis.RiskAction = domain.RiskAction(riskAction)
		analysis.RiskLevel = domain.RiskLevel(riskLevel)
		analysis.RiskReasons = reasonsJSON
		a := analysis
		analyses = append(analyses, &a)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return analyses, nil
}

func (r *cassandraFraudRepository) GetAnalysesByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.FraudAnalysis, error) {
	var analyses []*domain.FraudAnalysis

	query := `SELECT analysis_id, payment_id, user_id, account_id, risk_score, risk_action, risk_level,
		risk_reasons, device_id, ip_address, geo_location, signals, created_at
		FROM fraud_analyses WHERE user_id = ? LIMIT ?`

	iter := r.session.Query(query, userID, limit).WithContext(ctx).Iter()
	var analysis domain.FraudAnalysis
	var riskAction, riskLevel, reasonsJSON, signalsJSON string

	for iter.Scan(
		&analysis.AnalysisID,
		&analysis.PaymentID,
		&analysis.UserID,
		&analysis.AccountID,
		&analysis.RiskScore,
		&riskAction,
		&riskLevel,
		&reasonsJSON,
		&analysis.DeviceID,
		&analysis.IPAddress,
		&analysis.GeoLocation,
		&signalsJSON,
		&analysis.CreatedAt,
	) {
		analysis.RiskAction = domain.RiskAction(riskAction)
		analysis.RiskLevel = domain.RiskLevel(riskLevel)
		analysis.RiskReasons = reasonsJSON
		a := analysis
		analyses = append(analyses, &a)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}

	return analyses, nil
}
