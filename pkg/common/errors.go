package common

type ErrorCode string

const (
	ErrInvalidInput ErrorCode = "ERR_001"
	ERRServerError  ErrorCode = "ERR_002"
)

type ErrorResponse struct {
	Code    ErrorCode `json:"code"`    // internal error code
	Message string    `json:"message"` // user-friendly message
	TraceID string    `json:"traceID"` // unique identifier for the API request
}
