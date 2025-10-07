package pkgmiddleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/utils"
)

// TraceID returns Gin middleware to handle trace IDs for observability.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Request.Header.Get(common.HeaderTraceId)
		if utils.IsEmpty(traceID) {
			traceID = uuid.New().String() // UUID; TODO upgrade to crypto/rand or OpenTelemetry integration later
		}
		// Set in context for handlers/services (e.g., logging, Kafka publish)
		c.Set(common.TraceId, traceID)
		// Propagate in the response header for clients/downstream tracing
		c.Writer.Header().Set(common.HeaderTraceId, traceID)
		c.Next()
	}
}
