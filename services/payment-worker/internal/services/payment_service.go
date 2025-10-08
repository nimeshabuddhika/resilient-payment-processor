package services

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type PaymentService interface {
	ProcessPayment(ctx context.Context, paymentJob views.PaymentJob) error
}

type PaymentServiceImpl struct {
	logger      *zap.Logger
	cnf         *configs.Config
	accountRepo repositories.AccountRepository
	orderRepo   repositories.OrderRepository
	db          *database.DB
	redisClient *redis.Client
}

func NewPaymentService(logger *zap.Logger, cnf *configs.Config, repo repositories.AccountRepository, orderRepo repositories.OrderRepository, db *database.DB, redisClient *redis.Client) PaymentService {
	return &PaymentServiceImpl{
		logger:      logger,
		cnf:         cnf,
		accountRepo: repo,
		orderRepo:   orderRepo,
		db:          db,
		redisClient: redisClient,
	}
}

func (p PaymentServiceImpl) ProcessPayment(ctx context.Context, paymentJob views.PaymentJob) error {
	err := p.db.WithTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// validate idempotency key
		idempotencyUUID, err := uuid.Parse(paymentJob.IdempotencyKey)
		if err != nil {
			_, dbErr := p.orderRepo.UpdateStatusIdempotencyID(ctx, tx, idempotencyUUID, pkg.OrderStatusFailed, "failed to parse idempotency key, no retry possible")
			p.logger.Error("failed to parse idempotency key", zap.Error(err), zap.Error(dbErr))
			return errors.New("failed to parse idempotency key")
		}

		// Check if the transaction is fraudulent
		isFraud := detectFraudTransaction(paymentJob.Amount)
		if isFraud {
			_, dbErr := p.orderRepo.UpdateStatusIdempotencyID(ctx, tx, idempotencyUUID, pkg.OrderStatusFailed, "fraudulent transaction detected, no retry possible")
			p.logger.Error("fraudulent transaction detected", zap.String("idempotency_key", paymentJob.IdempotencyKey), zap.Error(dbErr))
			return errors.New("fraudulent transaction detected")
		}

		// process transaction
		err = p.processTransaction(idempotencyUUID)
		if err != nil {
			dbErr := p.orderRepo.UpdateStatusIdempotencyError(ctx, tx, p.logger, idempotencyUUID)
			p.logger.Error("failed to process transaction", zap.String("idempotency_key", paymentJob.IdempotencyKey), zap.Error(err), zap.Error(dbErr))
			return err
		}

		_, dbErr := p.orderRepo.UpdateStatusIdempotencyID(ctx, tx, idempotencyUUID, pkg.OrderStatusSuccess, "payment processed successfully")
		p.logger.Info("payment processed successfully", zap.String("idempotency_key", paymentJob.IdempotencyKey), zap.Error(dbErr))
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func (p PaymentServiceImpl) processTransaction(idempotencyUUID uuid.UUID) error {
	// simulate processing time
	time.Sleep(5 * time.Second)

	// simulate random failure
	if int32(idempotencyUUID.ID()%4) == 0 {
		p.logger.Error("random failure detected", zap.String("idempotency_key", idempotencyUUID.String()))
		return errors.New("random failure detected")
	}
	return nil
}

// isFraud simulate fraud detection logic for testing
func detectFraudTransaction(amount float64) bool {
	if amount > 1000 {
		return true
	}
	return false
}

//func (p PaymentServiceImpl) updateOrderDbStatus(ctx context.Context, tx pgx.Tx, idempotencyUUID uuid.UUID, orderStatus pkg.OrderStatus, message string) {
//	err := p.orderRepo.UpdateStatusIdempotencyID(ctx, tx, idempotencyUUID, orderStatus, message)
//	if err != nil {
//		p.logger.Error("failed to update order status", zap.String("idempotency_key", idempotencyUUID.String()), zap.Error(err), zap.String("order_status", string(orderStatus)))
//	}
//	p.logger.Info("order status updated successfully", zap.String("idempotency_key", idempotencyUUID.String()), zap.String("order_status", string(orderStatus)))
//}
