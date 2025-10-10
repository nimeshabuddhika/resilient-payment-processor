package paymentworker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/google/uuid"
	tests "github.com/nimeshabuddhika/resilient-payment-processor/tests"
)

// TestPaymentWorkerKafkaOrderHandler_DLQOnInvalidMessage verifies that payment-worker consumes
// an invalid message from the orders topic and forwards it to the DLQ topic.
func TestPaymentWorkerKafkaOrderHandler_DLQOnInvalidMessage(t *testing.T) {
	// Spin up Kafka, Postgres, and Redis test containers
	kBootstrap, kTerminate, err := tests.StartKafkaForTests()
	if err != nil {
		t.Fatalf("failed to start kafka: %v", err)
	}
	defer kTerminate()

	dsnNoProto, pgTerminate, err := tests.StartPostgresForTests()
	if err != nil {
		kTerminate()
		t.Fatalf("failed to start postgres: %v", err)
	}
	defer pgTerminate()

	redisAddr, redisTerminate, err := tests.StartRedisForTests()
	if err != nil {
		kTerminate()
		pgTerminate()
		t.Fatalf("failed to start redis: %v", err)
	}
	defer redisTerminate()

	// Create unique topics per test run
	uid := uuid.NewString()
	ordersTopic := fmt.Sprintf("orders-int-%s", uid)
	dlqTopic := fmt.Sprintf("orders-dlq-int-%s", uid)
	retryTopic := fmt.Sprintf("orders-retry-int-%s", uid)
	retryDLQTopic := fmt.Sprintf("orders-retry-dlq-int-%s", uid)
	tests.EnsureKafkaTopic(t, kBootstrap, ordersTopic, 1)
	tests.EnsureKafkaTopic(t, kBootstrap, dlqTopic, 1)
	tests.EnsureKafkaTopic(t, kBootstrap, retryTopic, 1)
	tests.EnsureKafkaTopic(t, kBootstrap, retryDLQTopic, 1)

	// Prepare a DLQ consumer before producing to avoid missing early messages
	groupID := uuid.NewString()
	dlqConsumer, err := ckafka.NewConsumer(&ckafka.ConfigMap{
		"bootstrap.servers": kBootstrap,
		"group.id":          groupID,
		"auto.offset.reset": "earliest",
	})
	if err != nil {
		t.Fatalf("failed to create kafka consumer: %v", err)
	}
	defer dlqConsumer.Close()
	if err := dlqConsumer.SubscribeTopics([]string{dlqTopic}, nil); err != nil {
		t.Fatalf("failed to subscribe to dlq topic: %v", err)
	}
	// Nudge assignment
	assignDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(assignDeadline) {
		if parts, _ := dlqConsumer.Assignment(); len(parts) > 0 {
			break
		}
		_ = dlqConsumer.Poll(100)
	}

	// Configure environment for payment-worker
	setEnv := func(k, v string) { _ = os.Setenv(k, v) }
	setEnv("GIN_MODE", "test")
	setEnv("APP_KAFKA_BROKERS", kBootstrap)
	setEnv("APP_KAFKA_CONSUMER_GROUP", "pw-"+uuid.NewString())
	setEnv("APP_KAFKA_TOPIC", ordersTopic)
	setEnv("APP_KAFKA_DLQ_TOPIC", dlqTopic)
	setEnv("APP_KAFKA_RETRY_TOPIC", retryTopic)
	setEnv("APP_KAFKA_RETRY_DLQ_TOPIC", retryDLQTopic)
	setEnv("APP_PRIMARY_DB_ADDR", dsnNoProto)
	setEnv("APP_REPLICA_DB_ADDR", dsnNoProto)
	setEnv("APP_AES_KEY", "Zk6IWX04Qm7ThZ5dJi8Xo4zyb8g9wfcxr5jxa1i3JKU=")
	setEnv("APP_REDIS_ADDR", redisAddr)
	setEnv("APP_RETRY_BASE_BACKOFF", "100ms")
	setEnv("APP_MAX_RETRY_BACKOFF", "1s")
	setEnv("APP_MAX_ORDERS_PLACED_CONCURRENT_JOBS", "2")
	setEnv("APP_MAX_ORDERS_RETRY_CONCURRENT_JOBS", "2")
	// Handle non-standard mapstructure tag by setting both forms
	setEnv("APP_MAXRETRYCOUNT", "3")
	setEnv("APP_MaxRetryCount", "3")

	// Start payment-worker as a subprocess
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "run", "../../services/payment-worker/cmd/main.go")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start payment-worker: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Wait briefly for the worker to initialize and subscribe
	time.Sleep(3 * time.Second)

	// Produce an invalid (non-JSON) message to the orders topic
	producer, err := ckafka.NewProducer(&ckafka.ConfigMap{
		"bootstrap.servers": kBootstrap,
		"acks":              "all",
	})
	if err != nil {
		t.Fatalf("failed to create kafka producer: %v", err)
	}
	defer producer.Close()

	invalidPayload := []byte("not-json")
	ordTopic := ordersTopic // copy for pointer capture
	err = producer.Produce(&ckafka.Message{
		TopicPartition: ckafka.TopicPartition{Topic: &ordTopic, Partition: ckafka.PartitionAny},
		Key:            []byte(uuid.NewString()),
		Value:          invalidPayload,
	}, nil)
	if err != nil {
		t.Fatalf("failed to produce invalid message: %v", err)
	}
	producer.Flush(5000)

	// Expect a message on DLQ containing failureReason
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := dlqConsumer.ReadMessage(1500 * time.Millisecond)
		if err != nil {
			continue
		}
		var payload map[string]any
		if e := json.Unmarshal(msg.Value, &payload); e != nil {
			continue
		}
		if reason, ok := payload["failureReason"].(string); ok && reason != "" {
			return // success
		}
	}
	t.Fatalf("did not receive DLQ message with failureReason within timeout")
}
