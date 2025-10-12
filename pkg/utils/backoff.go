package utils

import (
	"math"
	"math/rand"
	"time"
)

// CalculateExponentialBackoffWithJitter computes a jittered exponential backoff delay.
// - count: Retry attempt number (1-based, e.g., 1 for first retry)
// - base: Base delay (e.g., 5 * time.Second)
// - max: Maximum allowable delay (e.g., 5 * time.Minute)
// Returns the calculated duration with jitter.
func CalculateExponentialBackoffWithJitter(count int, base time.Duration, max time.Duration) time.Duration {
	if count <= 0 {
		return 0
	}

	// Exponential backoff: base * 2^(count-1)
	baseDelay := base * time.Duration(math.Pow(2, float64(count-1)))

	// Add jitter: ±25% of baseDelay to avoid synchronization
	jitter := time.Duration(rand.Int63n(int64(baseDelay/4))) - (baseDelay / 8) // -12.5% to +12.5%
	delay := baseDelay + jitter

	// Cap at max delay
	if delay > max {
		delay = max
	}

	return delay
}
