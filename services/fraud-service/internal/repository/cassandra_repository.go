package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

// toUUID converts google/uuid values (a named [16]byte array) to gocql.UUID,
// the only type gocql can marshal/unmarshal into CQL uuid columns.
func toUUID(id uuid.UUID) gocql.UUID {
	return gocql.UUID(id)
}

const fullAnalysisCols = `analysis_id, payment_id, user_id, account_id, risk_score, risk_action, risk_level, risk_reasons, device_id, ip_address, geo_location, signals, amount, currency, merchant, merchant_category, merchant_city, merchant_country, latitude, longitude, created_at`

func (r *cassandraFraudRepository) CreateAnalysis(ctx context.Context, analysis *domain.FraudAnalysis) error {
	query := `INSERT INTO fraud_analyses (
		analysis_id, payment_id, user_id, account_id, risk_score, risk_action, risk_level,
		risk_reasons, device_id, ip_address, geo_location, signals,
		amount, currency, merchant, merchant_category, merchant_city, merchant_country,
		latitude, longitude, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	return r.session.Query(query,
		toUUID(analysis.AnalysisID),
		toUUID(analysis.PaymentID),
		toUUID(analysis.UserID),
		toUUID(analysis.AccountID),
		analysis.RiskScore,
		string(analysis.RiskAction),
		string(analysis.RiskLevel),
		analysis.RiskReasons,
		analysis.DeviceID,
		analysis.IPAddress,
		analysis.GeoLocation,
		analysis.Signals,
		analysis.Amount,
		analysis.Currency,
		analysis.Merchant,
		analysis.MerchantCategory,
		analysis.MerchantCity,
		analysis.MerchantCountry,
		analysis.Latitude,
		analysis.Longitude,
		analysis.CreatedAt,
	).WithContext(ctx).Exec()
}

func scanAnalyses(iter *gocql.Iter) ([]*domain.FraudAnalysis, error) {
	var analyses []*domain.FraudAnalysis
	var analysisID, paymentID, userID, accountID gocql.UUID
	var riskAction, riskLevel, reasonsJSON, signalsJSON string
	var riskScore, latitude, longitude float64
	var amount int64
	var currency, merchant, merchantCategory, merchantCity, merchantCountry string
	var deviceID, ipAddress, geoLocation string
	var createdAt time.Time

	for iter.Scan(
		&analysisID,
		&paymentID,
		&userID,
		&accountID,
		&riskScore,
		&riskAction,
		&riskLevel,
		&reasonsJSON,
		&deviceID,
		&ipAddress,
		&geoLocation,
		&signalsJSON,
		&amount,
		&currency,
		&merchant,
		&merchantCategory,
		&merchantCity,
		&merchantCountry,
		&latitude,
		&longitude,
		&createdAt,
	) {
		a := &domain.FraudAnalysis{
			AnalysisID:       uuid.UUID(analysisID),
			PaymentID:        uuid.UUID(paymentID),
			UserID:           uuid.UUID(userID),
			AccountID:        uuid.UUID(accountID),
			RiskScore:        riskScore,
			RiskAction:       domain.RiskAction(riskAction),
			RiskLevel:        domain.RiskLevel(riskLevel),
			RiskReasons:      reasonsJSON,
			DeviceID:         deviceID,
			IPAddress:        ipAddress,
			GeoLocation:      geoLocation,
			Signals:          signalsJSON,
			Amount:           amount,
			Currency:         currency,
			Merchant:         merchant,
			MerchantCategory: merchantCategory,
			MerchantCity:     merchantCity,
			MerchantCountry:  merchantCountry,
			Latitude:         latitude,
			Longitude:        longitude,
			CreatedAt:        createdAt,
		}
		analyses = append(analyses, a)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return analyses, nil
}

func (r *cassandraFraudRepository) GetAnalysisByID(ctx context.Context, id uuid.UUID) (*domain.FraudAnalysis, error) {
	query := `SELECT ` + fullAnalysisCols + `
		FROM fraud_analyses WHERE analysis_id = ?`

	iter := r.session.Query(query, toUUID(id)).WithContext(ctx).Iter()
	defer iter.Close()

	analyses, err := scanAnalyses(iter)
	if err != nil {
		return nil, err
	}
	if len(analyses) == 0 {
		return nil, fmt.Errorf("fraud analysis not found")
	}
	return analyses[0], nil
}

func (r *cassandraFraudRepository) GetAnalysesByPayment(ctx context.Context, paymentID uuid.UUID) ([]*domain.FraudAnalysis, error) {
	query := `SELECT ` + fullAnalysisCols + `
		FROM fraud_analyses WHERE payment_id = ?`

	iter := r.session.Query(query, toUUID(paymentID)).WithContext(ctx).Iter()
	defer iter.Close()

	return scanAnalyses(iter)
}

func (r *cassandraFraudRepository) GetAnalysesByUser(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.FraudAnalysis, error) {
	if limit <= 0 {
		limit = 100
	}
	query := `SELECT ` + fullAnalysisCols + `
		FROM fraud_analyses WHERE user_id = ? LIMIT ?`

	iter := r.session.Query(query, toUUID(userID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	return scanAnalyses(iter)
}

// GetRecentDecisions returns the user's most recent authorisation analyses
// (created_at within the window) with the columns the real-time engine needs:
// amount, merchant, geo and outcome of every prior attempt.
func (r *cassandraFraudRepository) GetRecentDecisions(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]*domain.FraudAnalysis, error) {
	if limit <= 0 {
		limit = 200
	}
	query := `SELECT ` + fullAnalysisCols + `
		FROM fraud_analyses WHERE user_id = ? LIMIT ?`

	iter := r.session.Query(query, toUUID(userID), limit).WithContext(ctx).Iter()
	defer iter.Close()

	analyses, err := scanAnalyses(iter)
	if err != nil {
		return nil, err
	}

	recent := make([]*domain.FraudAnalysis, 0, len(analyses))
	for _, a := range analyses {
		if a.CreatedAt.After(since) {
			recent = append(recent, a)
		}
	}
	return recent, nil
}

// RebuildRiskReasonsFromSignals is a helper kept for parity with prior
// behaviour: signals were historically stored as JSON in the signals column.
func RebuildRiskReasonsFromSignals(signals string) []string {
	if signals == "" {
		return nil
	}
	var reasons []string
	if err := json.Unmarshal([]byte(signals), &reasons); err != nil {
		return nil
	}
	return reasons
}
