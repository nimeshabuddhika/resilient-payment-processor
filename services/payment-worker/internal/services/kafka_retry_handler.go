package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	kafkautils "github.com/nimeshabuddhika/resilient-payment-processor/pkg/kafka"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

// KafkaRetryHandler defines the interface for handling retry operations for failed payment jobs for payment-worker service.
// It is responsible for reading from the retry channel, retrying failed jobs, and publishing to the retry topic.
// It also handles retrying failed jobs from the retry DLQ topic.
type KafkaRetryHandler interface {
	Start() func()
}

// KafkaRetryConfig holds configuration and dependencies for the retry handler.
type KafkaRetryConfig struct {
	Context          context.Context
	Logger           *zap.Logger
	RetryChannel     <-chan views.PaymentJob
	Config           *configs.Config
	PaymentProcessor PaymentProcessor
	DB               *database.DB
	OrderRepo        repositories.OrderRepository
	// internal initialization
	validate         *validator.Validate
	dlqRetryProducer *kafka.Producer
	retryProducer    *kafka.Producer
	retryConsumer    *kafka.Consumer
	retrySemaphore   chan struct{} // Semaphore to limit concurrent retry order processing
}

// NewKafkaRetryHandler initializes a KafkaRetryHandler with the given configuration.
// It sets up the retry producer, DLQ producer, consumer, and semaphore based on config values.
func NewKafkaRetryHandler(cfg KafkaRetryConfig) KafkaRetryHandler {
	// Initialize kafka topic configuration
	topicConfig := kafkautils.KafkaConfig{
		BootstrapServers: cfg.Config.KafkaBrokers,
		Topics: []kafkautils.TopicConfig{
			{
				Topic:             cfg.Config.KafkaRetryTopic, // Retry main topic
				NumPartitions:     int(cfg.Config.KafkaPartition),
				ReplicationFactor: 1, // Single partition
				Config: map[string]string{
					"retention.ms": fmt.Sprintf("%d", cfg.Config.KafkaRetryRetention.Milliseconds()),
				},
			},
			{
				Topic:             cfg.Config.KafkaRetryDLQTopic, // Retry DLQ topic
				NumPartitions:     int(cfg.Config.KafkaPartition),
				ReplicationFactor: 1, // Single partition
				Config: map[string]string{
					"retention.ms": fmt.Sprintf("%d", cfg.Config.KafkaRetryDLQRetention.Milliseconds()),
				},
			},
		},
	}
	err := kafkautils.InitKafkaTopics(cfg.Logger, cfg.Context, topicConfig)
	if err != nil {
		logger.Fatal("failed to initialize kafka topics", zap.Error(err))
	}

	// initialize kafka retry producer
	retryProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers, // List of Kafka broker addresses
		"acks":               "all",                   // Wait for all replicas
		"enable.idempotence": true,                    // Ensure messages are not sent twice
		"retries":            "1",                     // Built-in retry mechanism
	})
	if err != nil {
		cfg.Logger.Fatal("failedToCreateKafkaProducer", zap.Error(err))
	}
	cfg.Logger.Info("Kafka producer created successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// initializing dead letter queue retry producer
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"acks":               "all",
		"enable.idempotence": true,
	})
	if err != nil {
		cfg.Logger.Fatal("Failed to create Kafka retry producer", zap.Error(err))
	}
	cfg.Logger.Info("Kafka retry producer created successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// initializing kafka retry consumer
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,       // Kafka broker(s)
		"group.id":           cfg.Config.KafkaConsumerGroup, // Consumer group
		"auto.offset.reset":  "earliest",                    // Start from the beginning of the topic
		"enable.auto.commit": false,                         // Disable auto commit
	}
	kafkaRetryConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("Failed to create retry DLQ producer", zap.Error(err))
	}
	cfg.Logger.Info("Retry DLQ producer created successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// Initialize semaphore for retry concurrency
	cfg.retrySemaphore = make(chan struct{}, cfg.Config.MaxOrdersRetryConcurrentJobs)
	cfg.retryProducer = retryProducer
	cfg.dlqRetryProducer = dlqProducer
	cfg.retryConsumer = kafkaRetryConsumer
	cfg.validate = validator.New()
	return &cfg
}

// Start begins listening on the retry channel and logs retry attempts.
func (k *KafkaRetryConfig) Start() func() {
	// delivery report logger, non-blocking
	go k.startDeliveryReportLogger()

	// Read from retry channel and retry failed jobs
	go k.startRetryChannelHandler()

	// Start retry kafka consumer
	go k.startRetryConsumer()

	k.Logger.Info("Listening to retry channel")

	return func() {
		// drain producer
		if k.retryProducer != nil {
			k.retryProducer.Flush(5000)
			k.retryProducer.Close()
			k.Logger.Info("Retry producer closed successfully")
		}
		if k.dlqRetryProducer != nil {
			k.dlqRetryProducer.Flush(5000)
			k.dlqRetryProducer.Close()
			k.Logger.Info("Retry DLQ producer closed successfully")
		}
		if err := k.retryConsumer.Close(); err != nil {
			k.Logger.Error("Failed to close retry consumer", zap.Error(err))
		}
		k.Logger.Info("Retry consumer closed successfully")
	}
}

// startRetryChannelHandler reads from the retry channel and publishes jobs to the retry topic immediately.
func (k *KafkaRetryConfig) startRetryChannelHandler() {
	for {
		select {
		case <-k.Context.Done():
			return
		case job, ok := <-k.RetryChannel:
			if !ok {
				return
			}
			// Increment retry count and calculate next retry time
			job.RetryCount++

			// Check if the max retry count is exceeded
			if job.RetryCount > k.Config.MaxRetryCount {
				k.Logger.Error("Maximum retry count exceeded in channel",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Int("retryCount", job.RetryCount))
				k.sendToRetryDLQ(job, "maxRetryExceeded", fmt.Sprintf("Maximum retry count exceeded: %d", k.Config.MaxRetryCount))
				continue
			}

			delay := utils.CalculateExponentialBackoffWithJitter(job.RetryCount, k.Config.RetryBaseBackoff, k.Config.MaxRetryBackoff)
			job.NextRetryTime = time.Now().Add(delay)

			// Marshal and publish immediately
			payload, err := json.Marshal(job)
			if err != nil {
				k.Logger.Error("Failed to marshal retry job",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Error(err))
				continue
			}
			err = k.retryProducer.Produce(&kafka.Message{
				TopicPartition: kafka.TopicPartition{Topic: &k.Config.KafkaRetryTopic, Partition: kafka.PartitionAny},
				Key:            job.IdempotencyKey[:],
				Value:          payload,
			}, nil)
			if err != nil {
				k.Logger.Error("Failed to produce to retry topic",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Error(err))
			}
			k.Logger.Info("Published to retry topic",
				zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
				zap.Int("retryCount", job.RetryCount),
				zap.Time("nextRetryTime", job.NextRetryTime))
		}
	}
}

// startRetryConsumer starts the retry consumer loop.
func (k *KafkaRetryConfig) startRetryConsumer() {
	// Subscribe to the configured Kafka topic
	err := k.retryConsumer.SubscribeTopics([]string{k.Config.KafkaRetryTopic}, nil)
	if err != nil {
		k.Logger.Fatal("Failed to subscribe to retry topic", zap.Error(err))
	}

	k.Logger.Info("Listening to retry topic",
		zap.String("topic", k.Config.KafkaRetryTopic),
		zap.String("group", k.Config.KafkaConsumerGroup))

	for {
		msg, err := k.retryConsumer.ReadMessage(-1)
		if err != nil {
			k.Logger.Error("Failed to read retry message", zap.Error(err))
			continue
		}
		// Process synchronously in goroutine
		go k.processRetryMessage(msg)
	}
}

// processRetryMessage processes a retry message synchronously.
// It enforces backoff with blocking sleep, processes the payment, and commits/DLQs accordingly.
func (k *KafkaRetryConfig) processRetryMessage(msg *kafka.Message) {
	select { // Block 1: Non-blocking context check
	case <-k.Context.Done():
		return
	default:
	}

	// Acquire semaphore
	select { // Block 2: Context-aware semaphore acquisition. Provides an early exit if the context is canceled before attempting semaphore acquisition.
	case k.retrySemaphore <- struct{}{}:
		// Acquired
	case <-k.Context.Done():
		return
	}
	defer func() { <-k.retrySemaphore }() // Release after full processing, including commit/DLQ

	// ... (decoding, validation, backoff sleep, processing, commit/DLQ)
	// Decode message
	var job views.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("Failed to decode message",
			zap.Error(err))
		k.sendToRetryDLQ(job, "Json unmarshal error", err.Error())
		_, _ = k.retryConsumer.CommitMessage(msg) // Commit to skip bad message
		return
	}

	// Validate after unmarshal
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("Failed to validate message",
			zap.Error(err))
		k.sendToRetryDLQ(job, "Validate Error", err.Error())
		_, _ = k.retryConsumer.CommitMessage(msg) // Commit to skip
		return
	}

	// Check max retry limit
	if err := k.checkIfMaxRetryLimitExceeded(job, msg); err != nil {
		// Function handles DLQ and commit internally
		return
	}

	// Enforce backoff with blocking sleep durable via NextRetryTime in payload
	if time.Now().Before(job.NextRetryTime) {
		sleepDuration := time.Until(job.NextRetryTime)
		k.Logger.Info("Enforcing backoff delay",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Duration("sleepDuration", sleepDuration),
			zap.Int("retryCount", job.RetryCount))
		time.Sleep(sleepDuration) // Synchronous delay
	}

	// Check context after sleep
	select {
	case <-k.Context.Done():
		return // Do not commit if canceled; allow re-delivery
	default:
	}

	// Process payment
	k.Logger.Info("Processing retry message",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
		zap.Int("retry_count", job.RetryCount))
	procErr := k.PaymentProcessor.ProcessPayment(k.Context, job)
	if procErr != nil {
		k.Logger.Error("Failed to process payment, sending to DLQ",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(procErr))
		k.sendToRetryDLQ(job, "Process payment error", procErr.Error())
		if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
			k.Logger.Error("Failed to commit offset after DLQ", zap.Error(err))
		}
		return
	}

	// Successfully processed, commit the offset
	if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
		k.Logger.Error("Failed to commit offset", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		return
	}
	k.Logger.Info("Payment processed successfully",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
}

// sendToRetryDLQ publishes the given job to the retry DLQ topic.
func (k *KafkaRetryConfig) sendToRetryDLQ(job views.PaymentJob, reason, errMsg string) {
	payload := map[string]any{
		"job":           job,
		"failureReason": reason,
		"error":         errMsg,
		"failedAt":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	b, err := json.Marshal(payload) // Added error handling
	if err != nil {
		k.Logger.Error("Failed to marshal DLQ payload",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err))
		return
	}

	err = k.dlqRetryProducer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &k.Config.KafkaRetryDLQTopic,
			Partition: kafka.PartitionAny,
		},
		Key:   job.IdempotencyKey[:],
		Value: b,
	}, nil)
	if err != nil {
		k.Logger.Error("Failed to produce to retry DLQ",
			zap.Error(err))
		return
	}
	k.Logger.Info("Sent to retry DLQ",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
		zap.String("reason", reason))
}

// checkIfMaxRetryLimitExceeded checks if the retry count exceeds the max, sends to DLQ if so, and commits.
func (k *KafkaRetryConfig) checkIfMaxRetryLimitExceeded(job views.PaymentJob, msg *kafka.Message) error {
	tx, err := k.DB.Begin(k.Context)
	if err != nil {
		k.Logger.Error("Failed to begin transaction for max retry check",
			zap.Error(err))
		return err
	}
	defer func() {
		if commitErr := k.DB.Commit(k.Context, tx); commitErr != nil {
			k.Logger.Error("Failed to commit transaction for max retry check",
				zap.Error(commitErr))
		}
	}()

	if job.RetryCount > k.Config.MaxRetryCount {
		k.Logger.Error("Maximum retry count exceeded",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Int("retry_count", job.RetryCount))
		if err := k.OrderRepo.UpdateStatusIdempotencyID(k.Context, tx, job.IdempotencyKey, pkg.OrderStatusFailed, fmt.Sprintf("failed transaction after %d retries", job.RetryCount)); err != nil {
			k.Logger.Error("Failed to update order status for max retry",
				zap.Error(err))
		}
		k.sendToRetryDLQ(job, "max retry exceeded", fmt.Sprintf("Max retry count exceeded: %d", k.Config.MaxRetryCount))
		if _, commitErr := k.retryConsumer.CommitMessage(msg); commitErr != nil {
			k.Logger.Error("Failed to commit after max retry DLQ", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(commitErr))
		}
		return errors.New("max retry count exceeded")
	}
	return nil
}

// startDeliveryReportLogger logs delivery reports for the retry topic.
func (k *KafkaRetryConfig) startDeliveryReportLogger() {
	for e := range k.retryProducer.Events() {
		switch ev := e.(type) {
		case *kafka.Message:
			if ev.TopicPartition.Error != nil {
				k.Logger.Error("Retry topic delivery failed",
					zap.Error(ev.TopicPartition.Error),
					zap.Any("topic_partition", ev.TopicPartition))
			} else {
				k.Logger.Debug("Retry topic delivered successfully",
					zap.Any("topic_partition", ev.TopicPartition))
			}
		}
	}
}
