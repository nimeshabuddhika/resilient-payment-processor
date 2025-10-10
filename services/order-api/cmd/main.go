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

// NewApp is an exported helper that delegates to the shared app constructor.
// It exists here to make the router/app creation callable from this package as requested.
func NewApp(ctx context.Context, logger *zap.Logger) (*http.Server, func(), error) {
	return appsvc.NewApp(ctx, logger)
}

func main() {
	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger

	ctx := context.Background()
	// Build app server and dependencies
	srv, cleanup, err := appsvc.NewApp(ctx, logger)
	if err != nil {
		logger.Fatal("failed to build app", zap.Error(err))
	}
	defer cleanup()

	// Start a server in goroutine to allow signal handling
	go func() {
		logger.Sugar().Infow("Order API started", "addr", srv.Addr)
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	// Handle shutdown signals (SIGINT, SIGTERM) for a K8s pod termination grace period
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	osSignal := <-sigChan
	logger.Info("Received shutdown signal", zap.String("signal", osSignal.String()))
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Fatal("shutdown error", zap.Error(err))
	}
	if err = logger.Sync(); err != nil {
		panic(err)
	}
	<-shutdownCtx.Done()
	logger.Info("Service shutdown completed successfully")
}
