package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/configs"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/views"
	"go.uber.org/zap"
)

type OrderService interface {
	CreateOrder(ctx context.Context, traceId string, userId uuid.UUID, req views.OrderRequest) (string, error)
}

type OrderServiceImpl struct {
	logger         *zap.Logger
	kafkaPublisher KafkaPublisher
	db             *database.DB
	orderRepo      repositories.OrderRepository
	accountRepo    repositories.AccountRepository
	aesKey         []byte
}

func NewOrderService(logger *zap.Logger, cfg *configs.Config, publisher KafkaPublisher, db *database.DB, repo repositories.OrderRepository, accountRepo repositories.AccountRepository) OrderService {
	key, err := utils.DecodeString(cfg.AesKey)
	if err != nil {
		logger.Fatal("failed to decode AES key", zap.Error(err))
	}
	return &OrderServiceImpl{
		logger:         logger,
		kafkaPublisher: publisher,
		db:             db,
		orderRepo:      repo,
		accountRepo:    accountRepo,
		aesKey:         key,
	}
}

func (s *OrderServiceImpl) CreateOrder(ctx context.Context, traceId string, userId uuid.UUID, req views.OrderRequest) (string, error) {
	// For now, simulate order creation by generating a simple ID and logging the request.
	order := models.Order{
		ID:             uuid.New(),
		UserID:         userId,
		AccountID:      req.AccountID,
		IdempotencyKey: req.IdempotencyID,
		Amount:         req.Amount,
		Status:         pkg.OrderStatusPending,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	err := s.db.WithTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Check idempotency (query existing)
		exists, err := s.orderRepo.FindByIdempotencyKey(ctx, tx, req.IdempotencyID)
		if err != nil {
			return pkg.HandleSQLError(traceId, s.logger, err)
		}
		if exists {
			s.logger.Warn("Idempotency key already exists", zap.String("idempotencyKey", req.IdempotencyID.String()))
			return nil // Idempotent: success without action
		}
		// Validate balance
		account, err := s.accountRepo.FindById(ctx, tx, req.AccountID)
		if err != nil {
			return pkg.HandleSQLError(traceId, s.logger, err)
		}

		// decrypt balance
		accountBalanceStr, err := utils.DecryptAES(account.Balance, s.aesKey) // TODO: use a key manager or call user-api to get the balance
		if err != nil {
			s.logger.Error("Failed to decrypt balance", zap.String(pkg.TraceId, traceId), zap.Error(err))
			return err
		}
		// convert `accountBalanceStr` to float64
		accountBalance, err := utils.ToFloat64(accountBalanceStr)
		if err != nil {
			s.logger.Error("Failed to convert balance to float64", zap.String(pkg.TraceId, traceId), zap.Error(err))
			return err
		}

		// Check balance
		if accountBalance < req.Amount {
			return fmt.Errorf("insufficient balance: %d < %d", account.Balance, req.Amount)
		}

		// Create order
		_, err = s.orderRepo.Create(ctx, tx, order)
		if err != nil {
			return pkg.HandleSQLError(traceId, s.logger, err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	paymentJob := order.ToPaymentJob()
	// Publish after commit
	if err = s.kafkaPublisher.PublishOrder(userId, paymentJob); err != nil {
		s.logger.Error("Failed to publish order", zap.String(pkg.TraceId, traceId), zap.Error(err))
		// TODO: Enqueue for retry or DLQ; don't fail API
	}
	s.logger.Info("Order created successfully", zap.String(pkg.TraceId, traceId), zap.String("orderId", order.ID.String()))
	return order.ID.String(), nil
}
