package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/config"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/handlers"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/services"
	"go.uber.org/zap"
)

func main() {
	// Initialize logger
	common.InitLogger()
	logger := common.Logger

	// Load config
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	// Setup dependencies
	// Base Handler
	baseHandler := handlers.NewBaseHandler(logger)

	// Order Service
	publisher := services.NewKafkaPublisher(logger)
	orderService := services.NewOrderService(logger, publisher)
	orderHandler := handlers.NewOrderHandler(logger, orderService)

	// Setup Gin
	r := gin.Default()
	orderHandler.RegisterRoutes(r)
	baseHandler.RegisterRoutes(r)

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}
	logger.Sugar().Infow("Order API started", "port", cfg.Port)
	if err := r.Run(addr); err != nil {
		logger.Sugar().Fatalw("failed to start server", "error", err)
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("shutdown error", zap.Error(err))
	}
	err = logger.Sync()
	if err != nil {
		panic(err)
	} // Flush logs
}
