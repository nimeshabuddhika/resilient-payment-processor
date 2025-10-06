package middleware

import (
	"github.com/gin-gonic/gin"
	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-common"
)

// TraceID returns Gin middleware to handle trace IDs for observability.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Request.Header.Get(common.HEADER_TRACEID)
		if common.IsEmpty(traceID) {
			traceID = common.GenerateUUID() // UUID; TODO upgrade to crypto/rand or OpenTelemetry integration later
		}
		// Set in context for handlers/services (e.g., logging, Kafka publish)
		c.Set(common.TRACE_ID, traceID)
		// Propagate in the response header for clients/downstream tracing
		c.Writer.Header().Set(common.HEADER_TRACEID, traceID)
		c.Next()
	}
}
