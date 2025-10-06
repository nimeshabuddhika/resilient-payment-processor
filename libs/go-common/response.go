package go_common

// APIResponse represents the structure of a standard API response.
type APIResponse struct {
	Data map[string]interface{} `json:"data"`
}
