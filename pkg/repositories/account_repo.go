package repositories

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
)

// AccountRepository defines the interface for account repository.
type AccountRepository interface {
	// Create creates a new account.
	Create(ctx context.Context, tx pgx.Tx, user models.Account) (pgconn.CommandTag, error)
	// FindByID finds an account by ID.
	FindByID(ctx context.Context, accountID uuid.UUID) (models.Account, error)
	GetAccountsByUserID(ctx context.Context, userID uuid.UUID, pageNumber int, size int) ([]models.Account, error)
	UpdateAccountSummaryForOrderTx(ctx context.Context, tx pgx.Tx, idempotencyKey uuid.UUID, account models.Account) (int64, error)
}

type AccountRepositoryImpl struct {
	db *database.DB
}

func NewAccountRepository(db *database.DB) AccountRepository {
	return &AccountRepositoryImpl{db: db}
}

func (a AccountRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, account models.Account) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `INSERT INTO accounts (id, user_id, balance, currency, created_at, updated_at) 
		VALUES ($1, $2, $3, $4, $5, $6) 
		ON CONFLICT DO NOTHING`,
		account.ID, account.UserID, account.Balance, account.Currency, account.CreatedAt, account.UpdatedAt)
}

func (a AccountRepositoryImpl) FindByID(ctx context.Context, accountID uuid.UUID) (models.Account, error) {
	if accountID == uuid.Nil {
		return models.Account{}, fmt.Errorf("invalid account ID: %s", accountID.String())
	}
	var account models.Account
	err := a.db.QueryRow(ctx, `SELECT id, user_id, balance, currency, order_count, avg_order_amount, created_at, updated_at FROM accounts WHERE id = $1`, accountID).Scan(
		&account.ID, &account.UserID, &account.Balance, &account.Currency, &account.OrderCount, &account.AvgOrderAmount, &account.CreatedAt, &account.UpdatedAt)
	return account, err
}

func (a AccountRepositoryImpl) GetAccountsByUserID(ctx context.Context, userID uuid.UUID, pageNumber int, size int) ([]models.Account, error) {
	//calculate offset.
	offset := (pageNumber - 1) * size

	rows, err := a.db.Query(ctx, `SELECT id, user_id,currency, created_at, updated_at FROM svc_schema.accounts WHERE accounts.user_id = $1 LIMIT $2 OFFSET $3`,
		userID, size, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	accounts := make([]models.Account, 0)
	for rows.Next() {
		var acc models.Account
		if err := rows.Scan(&acc.ID, &acc.UserID, &acc.Currency, &acc.CreatedAt, &acc.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, acc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accounts, nil
}

func (a AccountRepositoryImpl) UpdateAccountSummaryForOrderTx(ctx context.Context, tx pgx.Tx, idempotencyKey uuid.UUID, account models.Account) (int64, error) {
	commandTag, err := tx.Exec(ctx, `
				UPDATE accounts a 
				SET 
					balance = $1, 
					order_count = $2,
					avg_order_amount = $3,
					updated_at = NOW()
			  	FROM orders o
			  	WHERE 
				  	a.id = $4 
					AND a.id = o.account_id 
			  	  	AND o.idempotency_key = $5 
			  	  	AND o.status != $6`,
		account.Balance,
		account.OrderCount,
		account.AvgOrderAmount,
		account.ID,
		idempotencyKey,
		pkg.OrderStatusSuccess,
	)
	if err != nil {
		return 0, err
	}
	return commandTag.RowsAffected(), nil
}
