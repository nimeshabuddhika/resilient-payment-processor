package main

import (
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/handlers"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/internal/services"
)

func main() {
	common.InitLogger()
	logger := common.Logger

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

	port := os.Getenv("ORDER_SVC_PORT")
	if port == "" {
		port = "8081"
	}
	addr := fmt.Sprintf(":%s", port)
	logger.Sugar().Infow("Order API started", "port", port)
	if err := r.Run(addr); err != nil {
		logger.Sugar().Fatalw("failed to start server", "error", err)
	}
}
