package orderapi_test

import (
	"bytes"
	"testing"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/google/uuid"
	testutils "github.com/nimeshabuddhika/resilient-payment-processor/tests/utils"
	"github.com/stretchr/testify/assert"
)

// TestKafkaPublish_Success verifies that creating an order causes a message to be published to Kafka.
func TestKafkaPublish_Success(t *testing.T) {
	baseURL, stop := testutils.StartOrderAPIServer(t)
	defer stop()

	userID, accountID := testutils.GetSeededIDs()
	bootstrap := testutils.GetKafkaBootstrap()
	topic := testutils.GetKafkaTopic()

	// Start a consumer first and ensure it is assigned before producing to avoid missing messages
	groupID := uuid.New().String()
	consumer, err := ckafka.NewConsumer(&ckafka.ConfigMap{
		"bootstrap.servers": bootstrap,
		"group.id":          groupID,
		"auto.offset.reset": "earliest",
	})
	if err != nil {
		t.Fatalf("failed to create kafka consumer: %v", err)
	}
	defer consumer.Close()

	if err := consumer.SubscribeTopics([]string{topic}, nil); err != nil {
		t.Fatalf("failed to subscribe to topic: %v", err)
	}

	// Wait until the consumer has an assignment to avoid race where message is produced before assignment completes
	assignDeadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(assignDeadline) {
			break
		}
		if parts, _ := consumer.Assignment(); len(parts) > 0 {
			break
		}
		// Poll to drive the consumer background event loop and trigger rebalances
		_ = consumer.Poll(100)
	}

	// Arrange request
	idemp := uuid.New()
	payload := map[string]interface{}{
		"idempotencyId":   idemp.String(),
		"accountId":       accountID.String(),
		"amount":          11.11,
		"currency":        "USD",
		"timestamp":       time.Now(),
		"ipAddress":       "127.0.0.1",
		"transactionType": "PURCHASE",
	}
	headers := map[string]string{"userId": userID.String()}

	// Act: create order which should publish to Kafka
	resp, err := testutils.PostRequestWithHeaders(t, baseURL+"/api/v1/orders", payload, headers)
	assert.NoError(t, err)
	assert.Equal(t, 201, resp.StatusCode)

	// Assert: read from Kafka and find the matching key
	deadline := time.Now().Add(30 * time.Second)
	found := false
	key := idemp[:]
	for time.Now().Before(deadline) {
		msg, err := consumer.ReadMessage(1500 * time.Millisecond)
		if err != nil {
			continue
		}
		if bytes.Equal(msg.Key, key) {
			found = true
			break
		}
	}
	assert.True(t, found, "expected to read a message with idempotency key from Kafka, but did not within timeout")
}
