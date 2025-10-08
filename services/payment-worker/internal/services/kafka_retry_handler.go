package services

import (
	"context"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

type KafkaRetryHandler interface {
	Start(ctx context.Context)
}

type KafkaRetryConfig struct {
	Context   context.Context
	Logger    *zap.Logger
	Config    *configs.Config
	RetryChan chan views.PaymentJob
}

func NewKafkaRetryHandler(cfg KafkaRetryConfig) KafkaRetryHandler {
	return &cfg
}

func (k KafkaRetryConfig) Start(ctx context.Context) {
	k.Logger.Info("starting retry handler")

	// Read from retry channel and retry failed jobs
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-k.RetryChan:
				k.Logger.Info("retrying job", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Int("retry_count", job.RetryCount))
			}
		}
	}()

	k.Logger.Info("listening to retry channel")
}
