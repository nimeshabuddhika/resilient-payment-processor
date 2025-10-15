package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	middleware "github.com/nimeshabuddhika/resilient-payment-processor/pkg/middlewares"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/configs"
	_ "github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/docs"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/handlers"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/services"
	"github.com/swaggo/files"
	"github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
)

// NewApp wires dependencies, builds the Gin engine, and returns an *http.Server and a cleanup func.
// It reads configuration from environment variables via configs.Load.
func NewApp(ctx context.Context, logger *zap.Logger) (*http.Server, func(), error) {
	// Load config
	cfg, err := configs.Load(logger)
	if err != nil {
		return nil, nil, err
	}

	// Initialize postgres db
	dbConfig := database.Config{
		PrimaryDSN:  cfg.PrimaryDbAddr,
		ReplicaDSNs: []string{cfg.ReplicaDbAddr},
		MaxConns:    cfg.MaxDbCons,
		MinConns:    cfg.MinDbCons,
	}
	db, disconnect, err := database.New(ctx, logger, dbConfig)
	if err != nil {
		return nil, nil, err
	}

	// Run migrations on primary
	if err := database.RunMigrations(logger, cfg.PrimaryDbAddr); err != nil {
		disconnect()
		return nil, nil, err
	}
	// Setup dependencies
	baseHandler := handlers.NewBaseHandler(logger)
	publisher := services.NewKafkaPublisher(logger, ctx, cfg)

	orderRepo := repositories.NewOrderRepository(db)
	accountRepo := repositories.NewAccountRepository(db)
	orderService := services.NewOrderService(logger, cfg, publisher, db, orderRepo, accountRepo)
	orderHandler := handlers.NewOrderHandler(logger, orderService)

	// Router
	r := gin.Default()

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	api := r.Group("/api/v1")
	api.Use(middleware.TraceID(logger))
	api.Use(middleware.Metrics())

	orderHandler.RegisterRoutes(api)
	baseHandler.RegisterRoutes(r)

	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{Addr: addr, Handler: r}

	cleanup := func() {
		// close db pools
		disconnect()
		// close kafka producer
		publisher.Close()
	}

	return srv, cleanup, nil
}
