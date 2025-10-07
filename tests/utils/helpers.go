package testutils

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
)

func PostRequest(t *testing.T, url string, payload interface{}) (*http.Response, error) {
	b, _ := json.Marshal(payload)
	t.Logf("Request POST %s", url)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if resp != nil {
		t.Logf("Response POST %s: Status %d", url, resp.StatusCode)
	}
	// Close the body
	t.Cleanup(func() {
		_ = resp.Body.Close()
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
