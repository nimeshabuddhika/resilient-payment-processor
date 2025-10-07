package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Define metrics with promauto for auto-registration
var (
	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "resilient_job_go", // Prefix for all metrics
			Name:      "http_request_duration_seconds",
			Help:      "Duration of HTTP requests in seconds",
			Buckets: []float64{
				0.005, // request < 5ms
				0.01,  // request < 10ms
				0.025, // request < 25ms
				0.05,  // request < 50ms
				0.1,   // request < 100ms
				0.25,  // request < 250ms
				0.5,   // request < 500ms
				1,     // request < 1s
				2.5,   // request < 2.5s
				5,     // request < 5s
				10,    // request < 10s
			}, // 5ms-10s buckets
		},
		[]string{"method", "path", "status"}, // Labels for granularity
	)

	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "resilient_job_go",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
)

// Metrics returns Gin middleware for Prometheus instrumentation.
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		path := c.FullPath() // Route pattern (e.g., "/api/v1/orders")

		c.Next() // Process request

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		// Observe latency and increment count with labels
		httpRequestDuration.WithLabelValues(method, path, status).Observe(duration)
		httpRequestsTotal.WithLabelValues(method, path, status).Inc()
	}
}
