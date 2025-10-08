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
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/internal/services"
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

	// Initialize payment service. account and order repo
	accountRepo := repositories.NewAccountRepository()
	orderRepo := repositories.NewOrderRepository()
	paymentService := services.NewPaymentService(logger, cfg, accountRepo, orderRepo, db, redisClient)

	// Initialize kafka consumer
	kafkaConsumer := services.NewKafkaConsumer(logger, cfg, paymentService)
	closeConsumer := kafkaConsumer.Consume(ctx)

	// Handle shutdown signals (SIGINT, SIGTERM) for a K8s pod termination grace period
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	closeConsumer()
	redisCloser()
}
