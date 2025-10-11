package services

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/configs"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/dtos"
	"go.uber.org/zap"
)

type OrderService interface {
	CreateOrder(ctx context.Context, traceId string, userId uuid.UUID, request dtos.OrderRequest) (string, error)
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

func (s *OrderServiceImpl) CreateOrder(ctx context.Context, traceId string, userId uuid.UUID, req dtos.OrderRequest) (string, error) {
	// For now, simulate order creation by generating a simple ID and logging the request.
	order := models.Order{
		ID:             uuid.New(),
		UserID:         userId,
		AccountID:      req.AccountID,
		IdempotencyKey: req.IdempotencyID,
		Currency:       req.Currency,
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
			s.logger.Warn("idempotency key already exists",
				zap.String(pkg.TraceId, traceId),
				zap.String("idempotencyKey", req.IdempotencyID.String()))
			return pkg.NewAppError(pkg.ErrIdempotencyConflictCode, "idempotency key already exists", nil)
		}
		// Get account
		account, err := s.accountRepo.FindByID(ctx, tx, req.AccountID)
		if err != nil {
			return pkg.HandleSQLError(traceId, s.logger, err)
		}

		// decrypt balance
		accountBalanceStr, err := utils.DecryptAES(account.Balance, s.aesKey) // TODO: use a key manager or call user-api to get the balance
		if err != nil {
			s.logger.Error("failed to decrypt balance", zap.String(pkg.TraceId, traceId), zap.Error(err))
			return pkg.NewAppError(pkg.ErrServerCode, "decryption failed", err)
		}
		// convert `accountBalanceStr` to float64
		accountBalance, err := utils.ToFloat64(accountBalanceStr)
		if err != nil {
			s.logger.Error("failed to convert balance to float64", zap.String(pkg.TraceId, traceId), zap.Error(err))
			return pkg.NewAppError(pkg.ErrServerCode, "parse failed", err)
		}

		// Check balance
		if accountBalance < req.Amount {
			s.logger.Warn("insufficient balance", zap.String(pkg.TraceId, traceId))
			return pkg.NewAppError(pkg.ErrInsufficientFundsCode, "insufficient balance", pkg.ErrInsufficientBalance)
		}

		//encrypt transaction amount
		encAmount, err := utils.EncryptAES(utils.Float64ToByte(req.Amount), s.aesKey)
		if err != nil {
			s.logger.Error("failed to encrypt transaction amount", zap.String(pkg.TraceId, traceId), zap.Error(err))
			return pkg.NewAppError(pkg.ErrServerCode, "encryption failed", err)
		}
		order.Amount = encAmount

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
	if err = s.kafkaPublisher.PublishOrder(paymentJob); err != nil {
		s.logger.Error("failed to publish order", zap.String(pkg.TraceId, traceId), zap.Error(err))
		// TODO: Enqueue for retry or DLQ; don't fail API
	}
	s.logger.Info("order created successfully", zap.String(pkg.TraceId, traceId), zap.String("order_id", order.ID.String()))
	return order.ID.String(), nil
}
