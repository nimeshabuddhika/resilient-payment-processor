package pkg

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// Errors
var ErrRateLimitExceeded = errors.New("rate limit exceeded")

// DistributedLimiter combines local rate.Limiter with Redis for global enforcement.
type DistributedLimiter struct {
	localLimiter *rate.Limiter
	redisClient  *redis.Client
	key          string        // e.g: "global:job_rate"
	ttl          time.Duration // e.g: 1m for counter-expiry
	logger       *zap.Logger
}

// NewDistributedLimiter creates a limiter; if globalRate=0, it's unlimited.
func NewDistributedLimiter(redisClient *redis.Client, key string, globalRate, burst int, ttl time.Duration, logger *zap.Logger) *DistributedLimiter {
	var local *rate.Limiter
	if globalRate > 0 {
		local = rate.NewLimiter(rate.Limit(globalRate), burst)
	}
	return &DistributedLimiter{
		localLimiter: local,
		redisClient:  redisClient,
		key:          key,
		ttl:          ttl,
		logger:       logger,
	}
}

// Allow checks if a token is available; uses Redis for distributed increment.
func (d *DistributedLimiter) Allow(ctx context.Context) bool {
	if d.localLimiter == nil {
		return true // Unlimited
	}

	// Local check first (fast path)
	if !d.localLimiter.Allow() {
		return false
	}

	// Distributed check via Redis atomic increment
	pipe := d.redisClient.Pipeline()
	incr := pipe.Incr(ctx, d.key)
	pipe.Expire(ctx, d.key, d.ttl)
	_, err := pipe.Exec(ctx)
	if err != nil {
		d.logger.Error("Redis rate limit error; falling back to local", zap.Error(err))
		return true
	}

	count := incr.Val()
	if count > int64(d.localLimiter.Burst()) {
		d.logger.Warn("Global rate limit exceeded", zap.Int64("count", count))
		// Optional: decrement to correct, but skip for simplicity
		return false
	}
	return true
}
