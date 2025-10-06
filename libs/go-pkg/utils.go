package pkg

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GenerateUUID generates a new UUID.
func GenerateUUID() string {
	return uuid.New().String()
}

// IsEmpty checks if a string is empty.
func IsEmpty(s string) bool {
	return s == ""
}

func GetTraceID(c *gin.Context) (string, error) {
	traceID := c.GetString(TraceId)
	if IsEmpty(traceID) {
		return "", errors.New("trace id is empty")
	}
	return traceID, nil
}
