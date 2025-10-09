package main

import (
	"context"

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

// main function to start the payment-worker service
func main() {
	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	// Load config
	cfg, err := configs.Load(logger)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	// Initialize db
	// Initialize postgres db
	dbConfig := database.Config{
		PrimaryDSN:  cfg.PrimaryDbAddr,
		ReplicaDSNs: []string{cfg.ReplicaDbAddr},
		MaxConns:    cfg.MaxDbCons,
		MinConns:    cfg.MinDbCons,
	}
	db, disconnect, err := database.New(context.Background(), logger, dbConfig)
	if err != nil {
		logger.Fatal("failed to initialize database", zap.Error(err))
	}
	defer disconnect()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize redis client
	redisClient, redisCloser, err := cache.New(ctx, cache.Config{
		Addr: cfg.RedisAddr,
	})
	if err != nil {
		logger.Fatal("failed to initialize redis client", zap.Error(err))
	}
	logger.Info("redis initialized")

	// Initialize payment service dependencies
	aesKey, err := utils.DecodeString(cfg.AesKey)
	if err != nil {
		logger.Fatal("failed to decode AES key", zap.Error(err))
	}

	// Initialize retry channel
	retryChan := make(chan views.PaymentJob)
	// initialize repositories
	orderRepo := repositories.NewOrderRepository()

	paymentSvc := services.NewPaymentService(services.PaymentServiceConfig{
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
		AESKey:    aesKey,
		RetryChan: retryChan,
	})

	// Initialize kafka retry handler
	retryHandler := services.NewKafkaRetryHandler(services.KafkaRetryConfig{
		Context:        ctx,
		Logger:         logger,
		Config:         cfg,
		RetryChan:      retryChan,
		PaymentService: paymentSvc,
		DB:             db,
		OrderRepo:      orderRepo,
	})
	closeRetryHandler := retryHandler.Start()

	// Initialize kafka order consumer
	orderHandler := services.NewKafkaOrderConsumer(services.KafkaOrderConfig{
		Logger:         logger,
		Config:         cfg,
		PaymentService: paymentSvc,
	})
	closeOrderConsumer := orderHandler.Consume(ctx)

	<-ctx.Done()
	closeOrderConsumer() //Close kafka order consumer
	redisCloser()
	closeRetryHandler()
	close(retryChan) // Close retry channel to stop retry handler
}
