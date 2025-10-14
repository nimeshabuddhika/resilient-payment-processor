package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
)

type OrderRepository interface {
	// Create creates a new order.
	Create(ctx context.Context, tx pgx.Tx, order models.Order) (pgconn.CommandTag, error)
	FindByIdempotencyKey(ctx context.Context, tx pgx.Tx, idempotencyID uuid.UUID) (bool, error)
	// UpdateStatusIdempotencyID updates the status of an order by idempotency key.
	UpdateStatusIdempotencyID(ctx context.Context, tx pgx.Tx, idempotencyID uuid.UUID, status pkg.OrderStatus, message string) error

	// CreateAiDataset creates a new order in the AI dataset table.
	// This is used for training the AI model.
	CreateAiDataset(ctx context.Context, tx pgx.Tx, order models.OrderAIModel) (pgconn.CommandTag, error)
	GetAllAiDataset(ctx context.Context, db *database.DB, pageNumber int, size int) ([]models.OrderAIModel, error)
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

func (o OrderRepositoryImpl) UpdateStatusIdempotencyID(ctx context.Context, tx pgx.Tx, idempotencyID uuid.UUID, status pkg.OrderStatus, message string) error {
	_, err := tx.Exec(ctx, `UPDATE orders SET status = $1, message = $2, updated_at = $3 WHERE idempotency_key = $4`,
		status, message, time.Now(), idempotencyID)
	return err
}

func (o OrderRepositoryImpl) CreateAiDataset(ctx context.Context, tx pgx.Tx, order models.OrderAIModel) (pgconn.CommandTag, error) {
	return tx.Exec(ctx, `
						INSERT INTO orders_ai_dataset (user_id, account_id, idempotency_key, amount, is_fraud, transaction_velocity,ip_address, amount_deviation , created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT DO NOTHING`,
		order.UserID,
		order.AccountID,
		order.IdempotencyKey,
		order.Amount,
		order.IsFraud,
		order.TransactionVelocity,
		order.IpAddress,
		order.AmountDeviation,
		order.CreatedAt,
		order.UpdatedAt,
	)
}

func (o OrderRepositoryImpl) GetAllAiDataset(ctx context.Context, db *database.DB, pageNumber int, size int) ([]models.OrderAIModel, error) {
	//calculate offset.
	offset := (pageNumber - 1) * size
	rows, err := db.Query(ctx, `SELECT id, user_id, account_id, idempotency_key, amount, is_fraud, transaction_velocity,ip_address,amount_deviation  , created_at, updated_at 
		FROM svc_schema.orders_ai_dataset
		LIMIT $1 OFFSET $2`, size, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orderAIModels []models.OrderAIModel
	for rows.Next() {
		var order models.OrderAIModel
		if err = rows.Scan(
			&order.ID,
			&order.UserID,
			&order.AccountID,
			&order.IdempotencyKey,
			&order.Amount,
			&order.IsFraud,
			&order.TransactionVelocity,
			&order.IpAddress,
			&order.AmountDeviation,
			&order.CreatedAt,
			&order.UpdatedAt,
		); err != nil {
			return nil, err
		}
		orderAIModels = append(orderAIModels, order)
	}
	return orderAIModels, nil
}
