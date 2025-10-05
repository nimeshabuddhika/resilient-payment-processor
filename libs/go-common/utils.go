package go_common

import (
	"github.com/google/uuid"
)

// GenerateTraceID TODO - Use a library for cryptographically secure, unique IDs. Integrate with OpenTelemetry
func GenerateTraceID() string {
	return uuid.New().String()
}
