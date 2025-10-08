package services

import (
	"context"
	"sync"
	"time"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

type FraudFlag string

const (
	FraudFlagClean      FraudFlag = "clean"      // or "safe"
	FraudFlagSuspicious FraudFlag = "suspicious" // or "flagged"
	FraudFlagUnknown    FraudFlag = "unknown"
)

type FraudStatus struct {
	FraudFlag          FraudFlag
	IsEligibleForRetry bool
	Reason             string
	Score              int8 // 0-10 score
}

// FraudDetectionService defines the interface for fraud detection operations
type FraudDetectionService interface {
	AnalyzeTransaction(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, paymentJob views.PaymentJob)
}

// FraudDetectionConf holds configuration for fraud detection service
type FraudDetectionConf struct {
	Logger *zap.Logger
	Cnf    *configs.Config
}

// NewFraudDetectionService creates a new instance of FraudDetectionService
func NewFraudDetectionService(fdConf FraudDetectionConf) FraudDetectionService {
	return &FraudDetectionConf{
		Logger: fdConf.Logger,
		Cnf:    fdConf.Cnf,
	}
}

// AnalyzeTransaction checks if the transaction is fraudulent based on predefined rules.
//
// Current Implementation:
//   - Simple threshold-based validation (amount > 1000)
//
// Future Enhancements:
//   - Integrate ML model for advanced fraud detection
//   - Implement event-driven communication via Kafka
//   - Connect with AI-based Python/Django service for real-time analysis
func (f FraudDetectionConf) AnalyzeTransaction(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, paymentJob views.PaymentJob) {
	defer wg.Done()
	// simulate processing time with context cancellation support
	select {
	case <-ctx.Done():
		statusChan <- FraudStatus{IsEligibleForRetry: true, FraudFlag: FraudFlagUnknown, Reason: "context cancelled", Score: 10}
		return
	case <-time.After(time.Second * 2):
		// continue processing
	}

	// simulate fraudulent transaction
	if amount > 1000 {
		statusChan <- FraudStatus{FraudFlag: FraudFlagSuspicious, Reason: "amount exceeds threshold", Score: 10}
		return
	}
	statusChan <- FraudStatus{FraudFlag: FraudFlagClean, Reason: "regular transaction", Score: 2}
}
