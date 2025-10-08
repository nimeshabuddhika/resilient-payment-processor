package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

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
	logger.Info("redis client initialized successfully")

	// Initialize payment service dependencies
	aesKey, err := utils.DecodeString(cfg.AesKey)
	if err != nil {
		logger.Fatal("failed to decode AES key", zap.Error(err))
	}
	accountRepo := repositories.NewAccountRepository()
	orderRepo := repositories.NewOrderRepository()
	userRepo := repositories.NewUserRepository()
	fraudDetectionService := services.NewFraudDetectionService(services.FraudDetectionConf{
		Logger: logger,
		Cnf:    cfg,
	})

	// Initialize kafka retry handler
	retryChannel := make(chan views.PaymentJob)
	kafkaRetryHandler := services.NewKafkaRetryHandler(services.KafkaRetryConf{
		Logger:       logger,
		Cnf:          cfg,
		RetryChannel: retryChannel,
	})
	kafkaRetryHandler.InitializeRetryChannel(ctx)

	paymentService := services.NewPaymentService(services.PaymentServiceConf{
		Logger:       logger,
		Cnf:          cfg,
		AccountRepo:  accountRepo,
		OrderRepo:    orderRepo,
		UserRepo:     userRepo,
		Db:           db,
		RedisClient:  redisClient,
		FraudService: fraudDetectionService,
		AesKey:       aesKey,
		RetryChannel: retryChannel,
	})

	// Initialize kafka consumer
	kafkaConsumer := services.NewKafkaConsumer(services.KafkaOrderConf{
		Logger:         logger,
		Cnf:            cfg,
		PaymentService: paymentService,
	})
	closeConsumer := kafkaConsumer.Consume(ctx)

	// Handle shutdown signals (SIGINT, SIGTERM) for a K8s pod termination grace period
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	closeConsumer()     //Close kafka order consumer
	redisCloser()       // Close redis client
	close(retryChannel) // Close retry channel to stop retry handler
}
