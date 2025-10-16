package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/dtos"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	pw_dtos "github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/dtos"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type FraudFlag string

const (
	FraudFlagClean      FraudFlag = "clean"
	FraudFlagSuspicious FraudFlag = "suspicious"
	FraudFlagUnknown    FraudFlag = "unknown"
	VelocityWindow                = 1 * time.Hour // 1 hour for velocity
	DeviationCacheTTL             = 1 * time.Hour // 1 hour cache for avg amount
	RedisAvgKey                   = "fraud:avg_avg_amount:%s"
	RedisVelocityKey              = "fraud:velocity:%s"
)

type FraudStatus struct {
	FraudFlag          FraudFlag
	IsEligibleForRetry bool
	Reason             string
	Score              float64 // 0-1 score
}

// FraudDetector defines the interface for fraud detection operations
type FraudDetector interface {
	Analyze(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, paymentJob dtos.PaymentJob)
}

// FraudDetectorConfig holds configuration for fraud detection service
type FraudDetectorConfig struct {
	Logger        *zap.Logger
	Cnf           *configs.Config
	Redis         *redis.Client // Injected Redis client
	AccountRepo   repositories.AccountRepository
	EncryptionKey []byte

	fraudMLAddr string
}

// NewFraudDetectionService creates a new instance of FraudDetector
func NewFraudDetectionService(fdConf FraudDetectorConfig) FraudDetector {
	fdConf.fraudMLAddr = fmt.Sprintf("%s/api/v1/fraud-ml/predict", fdConf.Cnf.FraudMLServiceAddr)
	fdConf.Logger.Info("fraud_ml_server_address", zap.String("url", fdConf.fraudMLAddr))
	return &fdConf
}

// Analyze checks if the transaction is fraudulent based on predefined rules and computed features.
func (f *FraudDetectorConfig) Analyze(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, job dtos.PaymentJob) {
	defer wg.Done()
	// Simulate processing time with context cancellation support
	select {
	case <-ctx.Done():
		statusChan <- FraudStatus{IsEligibleForRetry: true, FraudFlag: FraudFlagUnknown, Reason: "context cancelled", Score: 10}
		return
	default:
		// Continue processing
	}
	f.Logger.Info("start_processing", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.String("account_id", job.AccountID.String()))

	// Calculate velocity using redis
	velocity, err := f.computeVelocity(ctx, job.AccountID)
	if err != nil {
		f.Logger.Error("velocity_calc_failed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		velocity = 1 // Fallback default
	}
	f.Logger.Info("velocity", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Int("velocity", velocity))

	// Compute deviation using db query + Redis cache
	deviation, err := f.computeDeviation(ctx, job.AccountID, amount)
	if err != nil {
		f.Logger.Error("deviation_calc_failed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
	}
	f.Logger.Info("deviation", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Float64("deviation", deviation))

	// Integration with fraud-ml-service
	result, err := f.AnalyzeTransaction(ctx, job.IdempotencyKey, pw_dtos.PredictRequest{
		Amount:              amount,
		TransactionVelocity: velocity,
		AmountDeviation:     deviation,
	})
	// Handle error
	if err != nil {
		f.Logger.Error("ml_request_failed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		statusChan <- FraudStatus{IsEligibleForRetry: true, FraudFlag: FraudFlagUnknown, Reason: err.Error(), Score: 1}
		return
	}
	// Handle suspicious
	if result.IsFraud {
		f.Logger.Info("suspicious_transaction", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("result", result))
		statusChan <- FraudStatus{IsEligibleForRetry: false, FraudFlag: FraudFlagSuspicious, Reason: "suspicious transaction", Score: result.FraudProbability}
		return
	}
	// regular transaction
	f.Logger.Info("regular_transaction", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("result", result))
	statusChan <- FraudStatus{FraudFlag: FraudFlagClean, Reason: "regular transaction", Score: result.FraudProbability}
}

// computeVelocity increments and gets the counter with TTL (orders in window).
func (f *FraudDetectorConfig) computeVelocity(ctx context.Context, accountId uuid.UUID) (int, error) {
	// Increment - atomic
	velocityKey := fmt.Sprintf(RedisVelocityKey, accountId.String())
	pipe := f.Redis.Pipeline()
	incr := pipe.Incr(ctx, velocityKey)
	pipe.Expire(ctx, velocityKey, VelocityWindow) // Reset window
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}
	return int(incr.Val()), nil
}

// computeDeviation gets avg from cache or DB, computes relative deviation.
func (f *FraudDetectorConfig) computeDeviation(ctx context.Context, accountID uuid.UUID, amount float64) (float64, error) {
	cacheKey := fmt.Sprintf(RedisAvgKey, accountID.String())
	avgOrderAmountStr, err := f.Redis.Get(ctx, cacheKey).Result()
	if errors.Is(err, redis.Nil) {
		// Fetch from db
		account, err := f.AccountRepo.FindByID(ctx, accountID)
		if err != nil {
			return 0, err
		}
		// Decrypt average order amount
		avgOrderAmount, err := utils.DecryptToFloat64(account.AvgOrderAmount, f.EncryptionKey)
		if err != nil {
			return 0, err
		}
		// Cache
		f.Redis.Set(ctx, cacheKey, fmt.Sprintf("%f", avgOrderAmount), DeviationCacheTTL)
		if avgOrderAmount > 0 {
			return utils.ComputeDeviation(amount, avgOrderAmount), nil
		}
		return 0, nil
	} else if err != nil {
		f.Logger.Error("failed_to_get_avg_amount", zap.Any(pkg.IdempotencyKey, accountID), zap.String("cache_key", cacheKey), zap.Error(err))
		return 0, err
	}
	// Parse string average amount to float64
	avgOrderAmount, err := utils.ToFloat64(avgOrderAmountStr)
	if err != nil {
		f.Logger.Error("failed_to_parse_average_order_amount", zap.Any(pkg.IdempotencyKey, accountID), zap.String("cache_key", cacheKey), zap.Error(err))
		return 0, err
	} else if avgOrderAmount > 0 {
		return utils.ComputeDeviation(amount, avgOrderAmount), nil
	}
	return 0, err
}

// AnalyzeTransaction sends a transaction analysis request to the fraud detection service and returns the response or an error.
func (f *FraudDetectorConfig) AnalyzeTransaction(ctx context.Context, idempotencyKey uuid.UUID, request pw_dtos.PredictRequest) (pw_dtos.PredictResponse, error) {
	f.Logger.Info("ml_request", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.String("url", f.fraudMLAddr))
	b, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.fraudMLAddr, bytes.NewReader(b))
	if err != nil {
		return pw_dtos.PredictResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		f.Logger.Info("ml_request_error", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Int("status_code", resp.StatusCode), zap.Error(err))
		return pw_dtos.PredictResponse{}, err
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	// Check response status
	if http.StatusOK != resp.StatusCode {
		f.Logger.Info("ml_request_failed", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Int("status_code", resp.StatusCode), zap.Error(err))
		return pw_dtos.PredictResponse{}, err
	}
	//Decode response
	var result pw_dtos.PredictResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
	}
	f.Logger.Info("ml_response", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Int("status_code", resp.StatusCode), zap.Error(err))
	return result, err
}
