package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/cache"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/internal/services"
	"go.uber.org/zap"
)

// main initializes and runs the payment worker service.
func main() {
	// Initialize global logger with default configuration
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync() // Ensure all buffered logs are flushed on exit

	// Load configuration from environment and optional config file
	cfg, err := configs.Load(logger)
	if err != nil {
		logger.Fatal("failed_to_load_config", zap.Error(err))
	}

	// Initialize PostgreSQL database connection
	dbConfig := database.Config{
		PrimaryDSN:  cfg.PrimaryDbAddr,
		ReplicaDSNs: []string{cfg.ReplicaDbAddr},
		MaxConns:    cfg.MaxDbCons,
		MinConns:    cfg.MinDbCons,
	}
	db, disconnect, err := database.New(context.Background(), logger, dbConfig)
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}
	defer disconnect() // Ensure database connections are closed on shutdown

	// Create a context that can be canceled for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Redis client for caching and locking
	redisClient, redisCloser, err := cache.New(ctx, cache.Config{
		Addr: cfg.RedisAddr,
	})
	if err != nil {
		logger.Fatal("Failed to initialize database", zap.Error(err))
	}
	logger.Info("Redis client initialized successfully")

	// Decode encryption key to byte array
	aesKey, err := utils.DecodeString(cfg.AesKey)
	if err != nil {
		logger.Fatal("Failed to decode encryption key", zap.Error(err))
	}

	// Initialize retry channel for failed payment jobs
	retryChannel := make(chan views.PaymentJob)
	// Initialize repositories for data access
	orderRepo := repositories.NewOrderRepository()
	// Configure and instantiate the payment processor
	paymentProcessor := services.NewPaymentProcessor(services.PaymentProcessorConfig{
		Logger:      logger,
		Config:      cfg,
		AccountRepo: repositories.NewAccountRepository(),
		OrderRepo:   orderRepo,
		UserRepo:    repositories.NewUserRepository(),
		DB:          db,
		RedisClient: redisClient,
		FraudDetector: services.NewFraudDetectionService(services.FraudDetectorConfig{
			Logger: logger,
			Cnf:    cfg,
		}),
		EncryptionKey: aesKey,
		RetryChannel:  retryChannel,
	})

	// Set up Kafka retry handler
	retryHandler := services.NewKafkaRetryHandler(services.KafkaRetryConfig{
		Context:          ctx,
		Logger:           logger,
		Config:           cfg,
		RetryChannel:     retryChannel,
		PaymentProcessor: paymentProcessor,
		DB:               db,
		OrderRepo:        orderRepo,
	})
	closeRetryHandler := retryHandler.Start()

	// Set up Kafka order consumer
	orderHandler := services.NewKafkaOrderConsumer(services.KafkaOrderConfig{
		Context:          ctx,
		Logger:           logger,
		Config:           cfg,
		PaymentProcessor: paymentProcessor,
	})
	closeOrderConsumer := orderHandler.Start()

	// Handle graceful shutdown on SIGINT or SIGTERM
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	osSignal := <-sigChan
	logger.Info("Received shutdown signal", zap.String("signal", osSignal.String()))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	cancel() // Trigger context cancellation
	closeOrderConsumer()
	redisCloser()
	closeRetryHandler()
	close(retryChannel) // Close retry channel to stop retry handler
	<-shutdownCtx.Done()
	logger.Info("Service shutdown completed successfully")
}
