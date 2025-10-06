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
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common/middleware"
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
	// Base Handler for health/metrics
	baseHandler := handlers.NewBaseHandler(logger)

	// Kafka Publisher: Pass config for brokers to enable actual publishing
	publisher := services.NewKafkaPublisher(logger, cfg.KafkaBrokers)

	// Order Service: Business logic for job creation and Kafka publish
	orderService := services.NewOrderService(logger, publisher)
	orderHandler := handlers.NewOrderHandler(logger, orderService)

	// Setup Gin router: TODO extracting to a func NewRouter() *gin.Engine for reuse/testability
	r := gin.Default()

	// Group routes with /api/v1 prefix for versioning
	api := r.Group("/api/v1")
	api.Use(middleware.TraceID()) // Add trace ID middleware
	api.Use(middleware.Metrics()) // Add latency middleware

	orderHandler.RegisterRoutes(api)
	baseHandler.RegisterRoutes(r)

	// Prepare server address from config
	addr := fmt.Sprintf(":%s", cfg.Port)

	// Create an HTTP server for graceful shutdown
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Start a server in goroutine to allow signal handling
	go func() {
		logger.Sugar().Infow("Order API started", "port", cfg.Port)
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	// Handle shutdown signals (SIGINT, SIGTERM) for a K8s pod termination grace period
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")

	// Timeout context for draining connections (align with K8s terminationGracePeriodSeconds)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("shutdown error", zap.Error(err))
	}

	// Flush logs before exit for observability, TODO integrate with Prometheus later
	if err = logger.Sync(); err != nil {
		panic(err) // ensures logs are persisted
	}
}
