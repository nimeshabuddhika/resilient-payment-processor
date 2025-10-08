package services

import (
	"context"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

type KafkaRetryHandler interface {
	InitializeRetryChannel(ctx context.Context)
}

type KafkaRetryConf struct {
	Context      context.Context
	Logger       *zap.Logger
	Cnf          *configs.Config
	RetryChannel chan views.PaymentJob
}

func NewKafkaRetryHandler(retryConf KafkaRetryConf) KafkaRetryHandler {
	return &retryConf
}

func (k KafkaRetryConf) InitializeRetryChannel(ctx context.Context) {
	k.Logger.Info("initializing retry channel")

	// Read from retry channel and retry failed jobs
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case paymentJob := <-k.RetryChannel:
				k.Logger.Info("kafka retry listener", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Int("retry_count", paymentJob.RetryCount))
			}
		}
	}()

	k.Logger.Info("listening to retry channel")
}
