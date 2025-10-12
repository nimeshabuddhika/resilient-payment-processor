package paymentworker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/tests"
	"github.com/stretchr/testify/assert"
)

// buildAndStartPaymentWorker builds the payment-worker binary and starts it as a child process
// in its own process group so we can terminate it (and any children) reliably from tests.
func buildAndStartPaymentWorker(t *testing.T, env map[string]string) (*exec.Cmd, func()) {
	t.Helper()

	// Build a temporary binary for the worker
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "payment-worker-testbin")
	build := exec.Command("go", "build", "-o", bin, "../../services/payment-worker/cmd/main.go")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		assert.FailNow(t, "failed to build payment-worker", err.Error())
	}

	cmd := exec.Command(bin)

	// Attach test-provided environment variables to the process
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Put the process in its own group so we can signal the whole group
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		assert.FailNow(t, "failed to start payment-worker", err.Error())
	}

	cleanup := func() {
		if cmd.Process == nil {
			return
		}
		// Try graceful shutdown first
		if runtime.GOOS != "windows" {
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGINT)
			} else {
				_ = cmd.Process.Signal(syscall.SIGINT)
			}
		} else {
			_ = cmd.Process.Kill()
		}
		// Wait with timeout, then force kill if needed
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			return
		case <-time.After(10 * time.Second):
			if runtime.GOOS != "windows" {
				pgid, err := syscall.Getpgid(cmd.Process.Pid)
				if err == nil {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				}
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	return cmd, cleanup
}

// createDLQConsumer creates a Kafka consumer subscribed to the given dlqTopic
// and waits for assignment to reduce flakiness.
func createDLQConsumer(t *testing.T, bootstrap, dlqTopic string) *ckafka.Consumer {
	t.Helper()
	groupID := uuid.NewString()
	c, err := ckafka.NewConsumer(&ckafka.ConfigMap{
		"bootstrap.servers": bootstrap,
		"group.id":          groupID,
		"auto.offset.reset": "earliest",
	})
	assert.NoError(t, err, "failed to create kafka consumer")
	err = c.SubscribeTopics([]string{dlqTopic}, nil)
	assert.NoError(t, err, "failed to subscribe to dlq topic")

	assignCtx, assignCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer assignCancel()
	for {
		parts, err := c.Assignment()
		assert.NoError(t, err)
		if len(parts) > 0 {
			break
		}
		select {
		case <-assignCtx.Done():
			assert.FailNow(t, "timeout waiting for DLQ consumer assignment")
		default:
			_ = c.Poll(100)
		}
	}
	return c
}

// TestPaymentWorkerKafkaOrderHandler_DLQOnInvalidMessage verifies that payment-worker consumes
// an invalid message from the orders topic and forwards it to the DLQ topic with the expected failure details.
func TestPaymentWorkerKafkaOrderHandler_DLQOnInvalidMessage(t *testing.T) {
	// Spin up Kafka, Postgres, and Redis test containers using helpers from tests package.
	kBootstrap, kTerminate, err := tests.StartKafkaForTests()
	assert.NoError(t, err, "failed to start kafka")
	t.Cleanup(kTerminate)

	dsnNoProto, pgTerminate, err := tests.StartPostgresForTests()
	assert.NoError(t, err, "failed to start postgres")
	t.Cleanup(pgTerminate)

	redisAddr, redisTerminate, err := tests.StartRedisForTests()
	assert.NoError(t, err, "failed to start redis")
	t.Cleanup(redisTerminate)

	// Create unique topics per test run to avoid interference in parallel test runs.
	uid := uuid.NewString()
	ordersTopic := fmt.Sprintf("orders-int-%s", uid)
	dlqTopic := fmt.Sprintf("orders-dlq-int-%s", uid)
	retryTopic := fmt.Sprintf("orders-retry-int-%s", uid)
	retryDLQTopic := fmt.Sprintf("orders-retry-dlq-int-%s", uid)
	tests.EnsureKafkaTopic(t, kBootstrap, ordersTopic, 1)
	tests.EnsureKafkaTopic(t, kBootstrap, dlqTopic, 1)
	tests.EnsureKafkaTopic(t, kBootstrap, retryTopic, 1)
	tests.EnsureKafkaTopic(t, kBootstrap, retryDLQTopic, 1)

	// Prepare a DLQ consumer before producing to capture messages reliably.
	dlqConsumer := createDLQConsumer(t, kBootstrap, dlqTopic)
	t.Cleanup(func() { _ = dlqConsumer.Close() })

	// Configure environment for payment-worker.
	env := map[string]string{
		"GIN_MODE":                              "test",
		"APP_KAFKA_BROKERS":                     kBootstrap,
		"APP_KAFKA_CONSUMER_GROUP":              "pw-" + uuid.NewString(),
		"APP_KAFKA_TOPIC":                       ordersTopic,
		"APP_KAFKA_DLQ_TOPIC":                   dlqTopic,
		"APP_KAFKA_RETRY_TOPIC":                 retryTopic,
		"APP_KAFKA_RETRY_DLQ_TOPIC":             retryDLQTopic,
		"APP_PRIMARY_DB_ADDR":                   dsnNoProto,
		"APP_REPLICA_DB_ADDR":                   dsnNoProto,
		"APP_AES_KEY":                           "Zk6IWX04Qm7ThZ5dJi8Xo4zyb8g9wfcxr5jxa1i3JKU=",
		"APP_REDIS_ADDR":                        redisAddr,
		"APP_RETRY_BASE_BACKOFF":                "100ms",
		"APP_MAX_RETRY_BACKOFF":                 "1s",
		"APP_MAX_ORDERS_PLACED_CONCURRENT_JOBS": "2",
		"APP_MAX_ORDERS_RETRY_CONCURRENT_JOBS":  "2",
		"APP_MAX_RETRY_COUNT":                   "3",
	}

	// Start payment-worker as a real binary so we can terminate it cleanly.
	_, stopWorker := buildAndStartPaymentWorker(t, env)
	t.Cleanup(stopWorker)

	// Give the worker a moment to initialize.
	time.Sleep(3 * time.Second)

	// Produce an invalid (non-JSON) message to the orders topic.
	producer, err := ckafka.NewProducer(&ckafka.ConfigMap{
		"bootstrap.servers": kBootstrap,
		"acks":              "all",
	})
	assert.NoError(t, err, "failed to create kafka producer")
	t.Cleanup(producer.Close)

	invalidPayload := []byte("not-json")
	ordTopic := ordersTopic // Copy for pointer safety.
	err = producer.Produce(&ckafka.Message{
		TopicPartition: ckafka.TopicPartition{Topic: &ordTopic, Partition: ckafka.PartitionAny},
		Key:            []byte(uuid.NewString()),
		Value:          invalidPayload,
	}, nil)
	assert.NoError(t, err, "failed to produce invalid message")
	_ = producer.Flush(5000)

	// Poll for DLQ message with expected failureReason.
	pollCtx, pollCancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer pollCancel()
	for {
		select {
		case <-pollCtx.Done():
			assert.FailNow(t, "timeout waiting for DLQ message")
		default:
			msg, err := dlqConsumer.ReadMessage(1500 * time.Millisecond)
			if err != nil {
				continue // Ignore timeouts or errors; retry.
			}
			var payload map[string]any
			if err := json.Unmarshal(msg.Value, &payload); err != nil {
				continue
			}
			reason, ok := payload["failureReason"].(string)
			assert.True(t, ok && reason == "json unmarshal error", "expected failureReason 'json unmarshal error', got %q", reason)
			_, ok = payload["job"].(map[string]any)
			assert.True(t, ok, "expected 'job' as empty map in DLQ payload")
			errMsg, ok := payload["error"].(string)
			assert.True(t, ok && errMsg != "", "expected non-empty 'error' in DLQ payload")
			return
		}
	}
}
