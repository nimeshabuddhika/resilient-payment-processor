package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"go.uber.org/zap"
)

// TraceID returns Gin middleware to handle trace IDs for observability.
func TraceID(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestIDStr := c.Request.Header.Get(pkg.HeaderRequestId)
		if utils.IsEmpty(requestIDStr) {
			logger.Error("request ID is missing in header", zap.String("method", c.Request.Method), zap.String("path", c.FullPath()))
			httpErr := pkg.ToErrorResponse(logger, "", pkg.NewAppError(pkg.ErrInvalidInputCode, "request ID is missing in header", nil))
			c.AbortWithStatusJSON(httpErr.Status, httpErr)
			return
		}
		_, err := uuid.Parse(requestIDStr)
		if err != nil {
			logger.Error("failed to parse request ID", zap.String("method", c.Request.Method), zap.String("path", c.FullPath()))
			httpErr := pkg.ToErrorResponse(logger, "", pkg.NewAppError(pkg.ErrInvalidInputCode, "failed to parse request ID", nil))
			c.AbortWithStatusJSON(httpErr.Status, httpErr)
			return
		}
		traceID := c.Request.Header.Get(pkg.HeaderTraceId)
		if utils.IsEmpty(traceID) {
			traceID = uuid.New().String() // UUID; TODO upgrade to crypto/rand or OpenTelemetry integration later
		}
		logger.Info("inbound request", zap.String(pkg.RequestId, requestIDStr), zap.String(pkg.TraceId, traceID), zap.String("method", c.Request.Method), zap.String("path", c.FullPath()))
		// Set in context for handlers/services (e.g., logging, Kafka publish)
		c.Set(pkg.TraceId, traceID)
		// Propagate in the response header for clients/downstream tracing
		c.Writer.Header().Set(pkg.HeaderTraceId, traceID)
		c.Next()
	}
}
