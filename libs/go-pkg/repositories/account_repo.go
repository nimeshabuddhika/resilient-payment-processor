package pkgrepositories

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	pkgmodels "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/models"
)

type AccountRepository interface {
	Create(ctx context.Context, tx pgx.Tx, user pkgmodels.Account) (pgconn.CommandTag, error)
}

type AccountRepositoryImpl struct {
}

func NewAccountRepository() AccountRepository {
	return &AccountRepositoryImpl{}
}

func (a AccountRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, account pkgmodels.Account) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `INSERT INTO accounts (user_id, balance, created_at, updated_at) 
					VALUES ($1, $2, $3, $4)
					ON CONFLICT DO NOTHING`,
		account.UserID,
		account.Balance,
		account.CreatedAt,
		account.UpdatedAt,
	)
}
