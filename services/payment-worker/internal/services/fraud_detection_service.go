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
	FraudFlagClean      FraudFlag = "clean"
	FraudFlagSuspicious FraudFlag = "suspicious"
	FraudFlagUnknown    FraudFlag = "unknown"

	FraudThresholdAmount = 1000.0
	AnalysisTimeout      = 2 * time.Second
)

type FraudStatus struct {
	FraudFlag          FraudFlag
	IsEligibleForRetry bool
	Reason             string
	Score              int8 // 0-10 score
}

// FraudDetector defines the interface for fraud detection operations
type FraudDetector interface {
	Analyze(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, paymentJob views.PaymentJob)
}

// FraudDetectorConfig holds configuration for fraud detection service
type FraudDetectorConfig struct {
	Logger *zap.Logger
	Cnf    *configs.Config
}

// NewFraudDetectionService creates a new instance of FraudDetector
func NewFraudDetectionService(fdConf FraudDetectorConfig) FraudDetector {
	return &FraudDetectorConfig{
		Logger: fdConf.Logger,
		Cnf:    fdConf.Cnf,
	}
}

// Analyze checks if the transaction is fraudulent based on predefined rules.
//
// Current Implementation:
//   - Simple threshold-based validation (amount > 1000)
//
// Future Enhancements:
//   - Integrate ML model for advanced fraud detection
//   - Implement event-driven communication via Kafka
//   - Connect with AI-based Python/Django service for real-time analysis
func (f *FraudDetectorConfig) Analyze(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, job views.PaymentJob) {
	defer wg.Done()
	// simulate processing time with context cancellation support
	select {
	case <-ctx.Done():
		statusChan <- FraudStatus{IsEligibleForRetry: true, FraudFlag: FraudFlagUnknown, Reason: "context cancelled", Score: 10}
		return
	case <-time.After(AnalysisTimeout):
		// continue processing
	}

	// simulate fraudulent transaction
	if amount > FraudThresholdAmount {
		statusChan <- FraudStatus{FraudFlag: FraudFlagSuspicious, Reason: "amount exceeds threshold", Score: 10}
		return
	}
	statusChan <- FraudStatus{FraudFlag: FraudFlagClean, Reason: "regular transaction", Score: 2}
}
