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
	pwdtos "github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/dtos"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// defaults. can be moved to configs later
const (
	// HTTP client timeouts
	defaultClientTimeout         = 2 * time.Second // total request deadline (hard cap)
	defaultResponseHeaderTimeout = 1 * time.Second // time to first byte of headers

	// HTTP transport pool sizing (per pod)
	defaultMaxConnsPerHost = 128
)

var ErrMLThrottled = errors.New("ml request throttled")

type FraudFlag string

// fraud analysis constants
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

// FraudDetectorConfig holds configuration for fraud detection service
type FraudDetectorConfig struct {
	Logger        *zap.Logger
	Config        *configs.Config
	Redis         *redis.Client // Injected Redis client
	AccountRepo   repositories.AccountRepository
	EncryptionKey []byte

	fraudMLAddr string
	httpClient  *http.Client
	rateLimiter *rate.Limiter
}

// FraudDetector defines the interface for fraud detection operations
type FraudDetector interface {
	Analyze(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, paymentJob dtos.PaymentJob)
}

// NewFraudDetectionService creates a new instance of FraudDetector
func NewFraudDetectionService(fdConf FraudDetectorConfig) FraudDetector {
	fdConf.fraudMLAddr = fmt.Sprintf("%s/api/v1/fraud-ml/predict", fdConf.Config.FraudMLServiceAddr)
	fdConf.Logger.Info("fraud_ml_server_address", zap.String("url", fdConf.fraudMLAddr))

	// initialize HTTP client (pooled, timeouts)
	fdConf.httpClient = utils.NewHTTPClient(
		utils.WithClientTimeout(defaultClientTimeout),
		utils.WithResponseHeaderTimeout(defaultResponseHeaderTimeout),
		utils.WithMaxConnsPerHost(defaultMaxConnsPerHost),
		utils.WithProxy(http.ProxyFromEnvironment),
		utils.WithForceAttemptHTTP2(true),
	)

	fdConf.rateLimiter = rate.NewLimiter(rate.Limit(fdConf.Config.MlRateLimitPerSec), fdConf.Config.MaxReplicaRateLimit)
	return &fdConf
}

// Analyze checks if the transaction is fraudulent based on predefined rules and computed features.
func (f *FraudDetectorConfig) Analyze(ctx context.Context, wg *sync.WaitGroup, statusChan chan<- FraudStatus, amount float64, job dtos.PaymentJob) {
	defer wg.Done()
	// Handles context cancellation at the beginning
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
	f.Logger.Debug("velocity", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Int("velocity", velocity))

	// Compute deviation using db query + Redis cache
	deviation, err := f.computeDeviation(ctx, job.AccountID, amount)
	if err != nil {
		f.Logger.Error("deviation_calc_failed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
	}
	f.Logger.Debug("deviation", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Float64("deviation", deviation))

	// Integration with fraud-ml-service
	result, err := f.getFraudReport(ctx, job.IdempotencyKey, pwdtos.PredictRequest{
		Amount:              amount,
		TransactionVelocity: velocity,
		AmountDeviation:     deviation,
	})
	if err != nil {
		// Treat throttling/timeouts as retry-eligible. the retry handler will backoff.
		f.Logger.Error("ml_request_failed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		statusChan <- FraudStatus{IsEligibleForRetry: true, FraudFlag: FraudFlagUnknown, Reason: err.Error(), Score: 0}
		return
	}

	// suspicious vs clean
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
	pipe.Expire(ctx, velocityKey, VelocityWindow) // reset window
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, err
	}
	return int(incr.Val()), nil
}

// computeDeviation gets avg from cache or DB, computes relative deviation.
func (f *FraudDetectorConfig) computeDeviation(ctx context.Context, accountID uuid.UUID, amount float64) (float64, error) {
	cacheKey := GetDeviationRedisKey(accountID)
	avgOrderAmountStr, err := f.Redis.Get(ctx, cacheKey).Result()
	if errors.Is(err, redis.Nil) {
		// fetch from db
		account, err := f.AccountRepo.FindByID(ctx, accountID)
		if err != nil {
			return 0, err
		}
		// decrypt average order amount
		avgOrderAmount, err := utils.DecryptToFloat64(account.AvgOrderAmount, f.EncryptionKey)
		if err != nil {
			return 0, err
		}
		// cache
		f.Redis.Set(ctx, cacheKey, fmt.Sprintf("%f", avgOrderAmount), DeviationCacheTTL)
		if avgOrderAmount > 0 {
			return utils.ComputeDeviation(amount, avgOrderAmount), nil
		}
		return 0, nil
	} else if err != nil {
		f.Logger.Error("failed_to_get_avg_amount", zap.Any(pkg.IdempotencyKey, accountID), zap.String("cache_key", cacheKey), zap.Error(err))
		return 0, err
	}
	// parse string average amount to float64
	avgOrderAmount, err := utils.ToFloat64(avgOrderAmountStr)
	if err != nil {
		f.Logger.Error("failed_to_parse_average_order_amount", zap.Any(pkg.IdempotencyKey, accountID), zap.String("cache_key", cacheKey), zap.Error(err))
		return 0, err
	} else if avgOrderAmount > 0 {
		return utils.ComputeDeviation(amount, avgOrderAmount), nil
	}
	return 0, err
}

// getFraudReport sends a transaction analysis request to the fraud detection service and returns the response or an error.
// - per-pod rate limit with bounded wait (token bucket)
// - hardened HTTP client (pooling + timeouts)
// - request-scoped timeout & full error propagation
func (f *FraudDetectorConfig) getFraudReport(ctx context.Context, idempotencyKey uuid.UUID, request pwdtos.PredictRequest) (pwdtos.PredictResponse, error) {
	// 1. per pod throttle with bounded wait using reserve
	now := time.Now()
	reservation := f.rateLimiter.ReserveN(now, 1)
	if !reservation.OK() {
		f.Logger.Warn("ml_request_throttled_rejected", zap.Any(pkg.IdempotencyKey, idempotencyKey))
		return pwdtos.PredictResponse{}, ErrMLThrottled
	}
	delay := reservation.DelayFrom(now)
	if delay > f.Config.MlRequestMaxThrottleWait {
		// too long; cancel the reservation and fail fast (retry path will backoff)
		reservation.Cancel()
		f.Logger.Warn("ml_request_throttled_timeout",
			zap.Any(pkg.IdempotencyKey, idempotencyKey),
			zap.Duration("throttle_delay_sec", delay.Round(time.Second)),
		)
		return pwdtos.PredictResponse{}, ErrMLThrottled
	}

	// short, bounded wait
	timer := time.NewTimer(delay)
	defer timer.Stop()
	if delay > 0 {
		select {
		case <-ctx.Done():
			reservation.Cancel() // return token to limiter
			f.Logger.Warn("ml_throttle_wait_context_cancelled", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(ctx.Err()))
			return pwdtos.PredictResponse{}, ctx.Err()
		case <-timer.C:
		}
	}

	// 2. build request with request scoped timeout (<= client timeout)
	reqBytes, err := json.Marshal(request)
	if err != nil {
		f.Logger.Error("ml_request_marshal_failed", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(err))
		return pwdtos.PredictResponse{}, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, defaultClientTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, f.fraudMLAddr, bytes.NewReader(reqBytes))
	if err != nil {
		return pwdtos.PredictResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	// 3. execute the call with the hardened client (keep-alives + timeouts)
	resp, err := f.httpClient.Do(req)
	if err != nil {
		f.Logger.Error("ml_request_error", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(err))
		return pwdtos.PredictResponse{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// 4. status code handling
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("ml_request_failed_status: %d", resp.StatusCode)
		f.Logger.Error("ml_request_failed", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Int("status_code", resp.StatusCode), zap.Error(err))
		return pwdtos.PredictResponse{}, err
	}

	// 5. decode response
	var result pwdtos.PredictResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		f.Logger.Error("failed_to_decode_response", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(err))
		return pwdtos.PredictResponse{}, err
	}

	f.Logger.Debug("ml_response", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Int("status_code", resp.StatusCode))
	return result, nil
}

// GetDeviationRedisKey returns the deviation redis key
func GetDeviationRedisKey(accountID uuid.UUID) string {
	return fmt.Sprintf(RedisAvgKey, accountID.String())
}
