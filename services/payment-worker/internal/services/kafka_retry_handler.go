package services

import (
	"context"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

// KafkaRetryHandler defines the interface for retrying failed payment jobs.
type KafkaRetryHandler interface {
	Start(ctx context.Context)
}

// KafkaRetryConfig contains dependencies and configuration for the retry handler.
type KafkaRetryConfig struct {
	Context   context.Context
	Logger    *zap.Logger
	Config    *configs.Config
	RetryChan chan views.PaymentJob
}

// NewKafkaRetryHandler constructs a KafkaRetryHandler with the given configuration.
func NewKafkaRetryHandler(cfg KafkaRetryConfig) KafkaRetryHandler {
	return &cfg
}

// Start begins listening on the retry channel and logs retry attempts.
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
