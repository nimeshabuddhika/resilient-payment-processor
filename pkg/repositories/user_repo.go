package repositories

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
)

// UserRepository defines the interface for user repository.
type UserRepository interface {
	// Create creates a new user.
	Create(ctx context.Context, tx pgx.Tx, user models.User) (pgconn.CommandTag, error)
	UpdateBalanceByAccountID(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, balance string) error
}

type UserRepositoryImpl struct {
}

func NewUserRepository() UserRepository {
	return &UserRepositoryImpl{}
}

func (u UserRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, user models.User) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `INSERT INTO users (id, username, created_at, updated_at) 
				VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING`,
		user.ID,
		user.Username,
		user.CreatedAt,
		user.UpdatedAt,
	)
}

func (u UserRepositoryImpl) UpdateBalanceByAccountID(ctx context.Context, tx pgx.Tx, accountID uuid.UUID, balance string) error {
	_, err := tx.Exec(ctx, `UPDATE accounts SET balance = $1, updated_at = NOW() WHERE id = $2`,
		balance,
		accountID,
	)
	return err
}
