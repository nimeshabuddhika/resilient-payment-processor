package tests

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
)

type ApiResponse struct {
	Data map[string]interface{} `json:"data"`
}
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details"`
}

func PostRequestWithHeaders(t *testing.T, url string, payload interface{}, headers map[string]string) (*http.Response, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Ensure required tracing headers exist for middleware
	if req.Header.Get(pkg.HeaderRequestId) == "" {
		req.Header.Set(pkg.HeaderRequestId, uuid.New().String())
	}
	if req.Header.Get(pkg.HeaderTraceId) == "" {
		req.Header.Set(pkg.HeaderTraceId, uuid.New().String())
	}
	client := &http.Client{}
	t.Logf("Request POST %s with headers", url)
	resp, err := client.Do(req)
	if resp != nil {
		t.Logf("Response POST %s: Status %d", url, resp.StatusCode)
	}
	t.Cleanup(func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	})
	return resp, err
}

func GetTraceId(resp *http.Response) string {
	return resp.Header.Get(pkg.HeaderTraceId)
}

func DecodeSuccess(r io.Reader) (ApiResponse, error) {
	var out ApiResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func DecodeError(r io.Reader) (ErrorResponse, error) {
	var out ErrorResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}
