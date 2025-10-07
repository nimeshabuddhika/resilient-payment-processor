package pkgrepositories

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	pkgmodels "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/models"
)

// AccountRepository defines the interface for account repository.
type AccountRepository interface {
	// Create creates a new account.
	Create(ctx context.Context, tx pgx.Tx, user pkgmodels.Account) (pgconn.CommandTag, error)
}

type AccountRepositoryImpl struct {
}

func NewAccountRepository() AccountRepository {
	return &AccountRepositoryImpl{}
}

func (a AccountRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, account pkgmodels.Account) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `INSERT INTO accounts (id, user_id, balance, currency, created_at, updated_at) 
		VALUES ($1, $2, $3, $4, $5, $6) 
		ON CONFLICT DO NOTHING`,
		account.ID, account.UserID, account.Balance, account.Currency, account.CreatedAt, account.UpdatedAt)
}
