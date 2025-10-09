package orderapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	testutils "github.com/nimeshabuddhika/resilient-payment-processor/tests/utils"
	"github.com/stretchr/testify/assert"
)

func TestCreateOrder_Success(t *testing.T) {
	// Arrange
	baseURL, stop := testutils.StartOrderAPIServer(t)
	defer stop()
	userID, accountID := testutils.GetSeededIDs()

	payload := map[string]interface{}{
		"idempotencyId":   uuid.New().String(),
		"accountId":       accountID.String(),
		"amount":          42.5,
		"currency":        "USD",
		"timestamp":       time.Now(),
		"ipAddress":       "127.0.0.1",
		"transactionType": "PURCHASE",
	}

	headers := map[string]string{"userId": userID.String()}

	// Act
	resp, err := testutils.PostRequestWithHeaders(t, baseURL+"/api/v1/orders", payload, headers)
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
	userID, accountID := testutils.GetSeededIDs()

	payload := map[string]interface{}{
		"idempotencyId": uuid.New().String(),
		"accountId":     accountID.String(),
		"amount":        -10, // invalid
		"currency":      "USD",
	}

	headers := map[string]string{"userId": userID.String()}

	resp, err := testutils.PostRequestWithHeaders(t, baseURL+"/api/v1/orders", payload, headers)
	assert.NoError(t, err)

	// Assert response
	traceId := testutils.GetTraceId(resp)
	assert.NotEmpty(t, traceId)
	assert.NotEqual(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "expected status %d, got %d", http.StatusBadRequest, resp.StatusCode)

	// Assert response body
	out, err := testutils.DecodeError(resp.Body)

	assert.NoError(t, err)
	assert.Equal(t, pkg.ErrInvalidInputCode.Code, out.Code)
	assert.NotEmpty(t, out.Message)
	assert.NotEmpty(t, out.Details)
}
