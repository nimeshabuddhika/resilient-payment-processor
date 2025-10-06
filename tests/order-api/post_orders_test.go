package orderapi_test

import (
	"net/http"
	"testing"
	"time"

	common "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg"
	testutils "github.com/nimeshabuddhika/resilient-payment-processor/tests/utils"
	"github.com/stretchr/testify/assert"
)

func TestCreateOrder_Success(t *testing.T) {
	// Arrange
	baseURL, stop := testutils.StartOrderAPIServer(t)
	defer stop()

	payload := map[string]interface{}{
		"accountId":       "acc-123",
		"amount":          42.5,
		"currency":        "USD",
		"timestamp":       time.Now(),
		"ipAddress":       "127.0.0.1",
		"transactionType": "PURCHASE",
	}

	// Act
	resp, err := testutils.PostRequest(t, baseURL+"/api/v1/orders", payload)
	assert.NoError(t, err)

	// Assert response
	traceId := testutils.GetTraceId(resp)
	assert.NotEmpty(t, traceId)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Assert response body
	out, err := testutils.DecodeSuccess(resp.Body)
	assert.NoError(t, err)
	orderID, ok := out.Data["orderId"].(string)
	assert.True(t, ok)
	assert.NotEmpty(t, orderID)
}

func TestCreateOrder_InvalidAmount_Less_Than_minimum(t *testing.T) {
	baseURL, stop := testutils.StartOrderAPIServer(t)
	defer stop()

	// Missing required fields (accountId, amount, currency)
	payload := map[string]interface{}{
		"amount":   -10, // invalid
		"currency": "USD",
	}

	resp, err := testutils.PostRequest(t, baseURL+"/api/v1/orders", payload)
	assert.NoError(t, err)

	// Assert response
	traceId := testutils.GetTraceId(resp)
	assert.NotEmpty(t, traceId)
	assert.NotEqual(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)

	// Assert response body
	out, err := testutils.DecodeError(resp.Body)

	assert.NoError(t, err)
	assert.Equal(t, string(common.ErrInvalidInput), out.Code)
	assert.NotEmpty(t, out.Message)
	assert.NotEmpty(t, out.Details)
}
