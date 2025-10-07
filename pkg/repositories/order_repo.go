package repositories

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
)

type OrderRepository interface {
	// Create creates a new order.
	Create(ctx context.Context, tx pgx.Tx, order models.Order) (pgconn.CommandTag, error)
	// CreateAiDataset creates a new order in the AI dataset table.
	// This is used for training the AI model.
	CreateAiDataset(ctx context.Context, tx pgx.Tx, order models.Order, isFraud bool) (pgconn.CommandTag, error)
	FindByIdempotencyKey(ctx context.Context, tx pgx.Tx, idempotencyID uuid.UUID) (bool, error)
}
type OrderRepositoryImpl struct {
}

func NewOrderRepository() OrderRepository {
	return &OrderRepositoryImpl{}
}

func (o OrderRepositoryImpl) Create(ctx context.Context, tx pgx.Tx, order models.Order) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `
						INSERT INTO orders (user_id, account_id, idempotency_key, amount, status, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
		order.UserID,
		order.AccountID,
		order.IdempotencyKey,
		order.Amount,
		order.Status,
		order.CreatedAt,
		order.UpdatedAt,
	)
}

func (o OrderRepositoryImpl) CreateAiDataset(ctx context.Context, tx pgx.Tx, order models.Order, isFraud bool) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `
						INSERT INTO orders_ai_dataset (user_id, account_id, idempotency_key, amount, status, is_fraud, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT DO NOTHING`,
		order.UserID,
		order.AccountID,
		order.IdempotencyKey,
		order.Amount,
		order.Status,
		isFraud,
		order.CreatedAt,
		order.UpdatedAt,
	)
}

// FindByIdempotencyKey finds an order by idempotency key.
func (o OrderRepositoryImpl) FindByIdempotencyKey(ctx context.Context, tx pgx.Tx, idempotencyID uuid.UUID) (bool, error) {
	if idempotencyID == uuid.Nil {
		return false, errors.New("idempotency key cannot be nil")
	}
	var exists bool
	err := tx.QueryRow(ctx, `
							SELECT EXISTS(SELECT 1 FROM orders WHERE idempotency_key = $1)`,
		idempotencyID,
	).Scan(&exists)
	return exists, err
}
