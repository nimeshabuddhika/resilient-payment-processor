package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	appsvc "github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/app"
	"go.uber.org/zap"
)

// @title Order API
// @version 1.0
// @description API for accepting orders, validating account balances, and publishing events to Kafka for payment processing in the resilient payment processor system. This service handles order creation with resilience patterns for high-volume, event-driven workflows.
// @contact.url https://github.com/nimeshabuddhika
// @license.name MIT License
// @host localhost:8000
// @BasePath /api/v1

// @schemes http

// @in header
// @name userId
// @description User UUID for demo authentication (TODO: replace with JWT)
func main() {
	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger

	ctx := context.Background()
	// Build app server and dependencies
	srv, cleanup, err := appsvc.NewApp(ctx, logger)
	if err != nil {
		logger.Fatal("failed_to_build_app", zap.Error(err))
	}
	defer cleanup()

	// Start a server in goroutine to allow signal handling
	go func() {
		logger.Sugar().Infow("order_API_started", "addr", srv.Addr)
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server_error", zap.Error(err))
		}
	}()

	// Handle shutdown signals (SIGINT, SIGTERM) for a K8s pod termination grace period
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	osSignal := <-sigChan
	logger.Info("received_shutdown_signal", zap.String("signal", osSignal.String()))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("shutdown error", zap.Error(err))
	}
	if err = logger.Sync(); err != nil {
		panic(err)
	}
	<-shutdownCtx.Done()
	logger.Info("service_shutdown_completed_successfully")
}
