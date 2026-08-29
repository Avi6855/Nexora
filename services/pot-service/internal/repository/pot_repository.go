package repository

import (
	"context"
	"fmt"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/nexora/nexora/services/pot-service/internal/domain"
)

type PotRepository interface {
	Create(ctx context.Context, pot *domain.Pot) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Pot, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Pot, error)
	Update(ctx context.Context, pot *domain.Pot) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type cassandraPotRepository struct {
	session *gocql.Session
}

func NewCassandraPotRepository(session *gocql.Session) PotRepository {
	return &cassandraPotRepository{session: session}
}

func (r *cassandraPotRepository) Create(ctx context.Context, pot *domain.Pot) error {
	query := `INSERT INTO pots (pot_id, user_id, name, target_amount, current_amount, currency, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if err := r.session.Query(query,
		pot.PotID, pot.UserID, pot.Name, pot.TargetAmount, pot.CurrentAmount,
		pot.Currency, string(pot.Status), pot.CreatedAt, pot.UpdatedAt,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("inserting pot: %w", err)
	}

	indexQuery := `INSERT INTO pots_by_user (user_id, pot_id, name, status, created_at)
		VALUES (?, ?, ?, ?, ?)`

	if err := r.session.Query(indexQuery,
		pot.UserID, pot.PotID, pot.Name, string(pot.Status), pot.CreatedAt,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("inserting pot index: %w", err)
	}

	return nil
}

func (r *cassandraPotRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Pot, error) {
	var pot domain.Pot
	var status string

	query := `SELECT pot_id, user_id, name, target_amount, current_amount, currency, status, created_at, updated_at
		FROM pots WHERE pot_id = ?`

	err := r.session.Query(query, id).WithContext(ctx).Scan(
		&pot.PotID, &pot.UserID, &pot.Name, &pot.TargetAmount, &pot.CurrentAmount,
		&pot.Currency, &status, &pot.CreatedAt, &pot.UpdatedAt,
	)

	if err == gocql.ErrNotFound {
		return nil, fmt.Errorf("%w", domain.ErrPotNotFound)
	}
	if err != nil {
		return nil, err
	}

	pot.Status = domain.PotStatus(status)
	return &pot, nil
}

func (r *cassandraPotRepository) GetByUserID(ctx context.Context, userID uuid.UUID) ([]*domain.Pot, error) {
	iter := r.session.Query(
		`SELECT pot_id, user_id, name, target_amount, current_amount, currency, status, created_at, updated_at
		FROM pots WHERE user_id = ?`, userID,
	).WithContext(ctx).Iter()
	defer iter.Close()

	var pots []*domain.Pot
	var pot domain.Pot
	var status string

	for iter.Scan(
		&pot.PotID, &pot.UserID, &pot.Name, &pot.TargetAmount, &pot.CurrentAmount,
		&pot.Currency, &status, &pot.CreatedAt, &pot.UpdatedAt,
	) {
		pot.Status = domain.PotStatus(status)
		p := pot
		pots = append(pots, &p)
	}

	if err := iter.Close(); err != nil {
		return nil, err
	}
	return pots, nil
}

func (r *cassandraPotRepository) Update(ctx context.Context, pot *domain.Pot) error {
	query := `UPDATE pots SET name = ?, current_amount = ?, status = ?, updated_at = ? WHERE pot_id = ?`
	if err := r.session.Query(query,
		pot.Name, pot.CurrentAmount, string(pot.Status), pot.UpdatedAt, pot.PotID,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("updating pot: %w", err)
	}

	indexQuery := `UPDATE pots_by_user SET name = ?, status = ? WHERE user_id = ? AND pot_id = ?`
	return r.session.Query(indexQuery,
		pot.Name, string(pot.Status), pot.UserID, pot.PotID,
	).WithContext(ctx).Exec()
}

func (r *cassandraPotRepository) Delete(ctx context.Context, id uuid.UUID) error {
	pot, err := r.GetByID(ctx, id)
	if err != nil {
		return err
	}

	if err := r.session.Query(`DELETE FROM pots WHERE pot_id = ?`, id).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("deleting pot: %w", err)
	}

	if err := r.session.Query(`DELETE FROM pots_by_user WHERE user_id = ? AND pot_id = ?`,
		pot.UserID, pot.PotID).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("deleting pot index: %w", err)
	}

	return nil
}
