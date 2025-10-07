package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
)

// AccountRepository defines the interface for account repository.
type AccountRepository interface {
	// Create creates a new account.
	Create(ctx context.Context, tx pgx.Tx, user models.Account) (pgconn.CommandTag, error)
	// FindById finds an account by ID.
	FindById(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (models.Account, error)
}

type AccountRepositoryImpl struct {
}

func NewAccountRepository() AccountRepository {
	return &AccountRepositoryImpl{}
}

func (a AccountRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, account models.Account) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `INSERT INTO accounts (id, user_id, balance, currency, created_at, updated_at) 
		VALUES ($1, $2, $3, $4, $5, $6) 
		ON CONFLICT DO NOTHING`,
		account.ID, account.UserID, account.Balance, account.Currency, account.CreatedAt, account.UpdatedAt)
}

func (a AccountRepositoryImpl) FindById(ctx context.Context, tx pgx.Tx, accountID uuid.UUID) (models.Account, error) {
	if accountID == uuid.Nil {
		return models.Account{}, fmt.Errorf("invalid account ID: %s", accountID.String())
	}
	var account models.Account
	err := tx.QueryRow(ctx, `SELECT id, user_id, balance, currency, created_at, updated_at FROM accounts WHERE id = $1`, accountID).Scan(
		&account.ID, &account.UserID, &account.Balance, &account.Currency, &account.CreatedAt, &account.UpdatedAt)
	return account, err
}
