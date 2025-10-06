package services

import (
	"context"
	"fmt"
	"time"

	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/views"
	"go.uber.org/zap"
)

type OrderService interface {
	CreateOrder(ctx context.Context, traceId string, userId string, req views.OrderRequest) (string, error)
}

type OrderServiceImpl struct {
	logger         *zap.Logger
	kafkaPublisher KafkaPublisher // not used yet; placeholder for future integration
}

func NewOrderService(logger *zap.Logger, publisher KafkaPublisher) OrderService {
	return &OrderServiceImpl{
		logger:         logger,
		kafkaPublisher: publisher,
	}
}

func (s *OrderServiceImpl) CreateOrder(ctx context.Context, traceId string, userId string, req views.OrderRequest) (string, error) {
	// For now, simulate order creation by generating a simple ID and logging the request.
	orderID := fmt.Sprintf("ord_%d", time.Now().UnixNano())
	s.logger.Info("order created",
		zap.String(common.TRACE_ID, traceId),
		zap.String("orderId", orderID),
		zap.String("userId", userId),
		zap.String("accountId", req.AccountID),
		zap.Float64("amount", req.Amount),
		zap.Time("timestamp", req.Timestamp),
		zap.String("ipAddress", req.IPAddress),
		zap.String("transactionType", req.TransactionType),
	)
	return orderID, nil
}
