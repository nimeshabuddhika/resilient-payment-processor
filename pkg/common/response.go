package common

// APIResponse represents the structure of a standard API response.
type APIResponse struct {
	TraceID string                 `json:"traceId"` // unique identifier for the API request
	Data    map[string]interface{} `json:"data"`
}
