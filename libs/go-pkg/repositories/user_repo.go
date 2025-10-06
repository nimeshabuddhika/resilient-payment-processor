package pkgrepositories

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	pkgmodels "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/models"
)

type UserRepository interface {
	Create(ctx context.Context, tx pgx.Tx, user pkgmodels.User) (pgconn.CommandTag, error)
}

type UserRepositoryImpl struct {
}

func NewUserRepository() UserRepository {
	return &UserRepositoryImpl{}
}

func (u UserRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, user pkgmodels.User) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `INSERT INTO users (id, username, created_at, updated_at) 
				VALUES ($1, $2, $3, $4)
				ON CONFLICT DO NOTHING`,
		user.ID,
		user.Username,
		user.CreatedAt,
		user.UpdatedAt,
	)
}
