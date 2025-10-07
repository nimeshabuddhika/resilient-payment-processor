package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/internal/handlers"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger

	// Load config
	cfg, err := configs.Load(logger)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize kafka consumer
	kafkaConsumer := handlers.NewKafkaConsumer(logger, cfg)
	closeConsumer := kafkaConsumer.Consume(ctx)

	// Handle shutdown signals (SIGINT, SIGTERM) for a K8s pod termination grace period
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	closeConsumer()
}
