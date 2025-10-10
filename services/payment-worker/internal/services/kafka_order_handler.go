package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

// KafkaOrderHandler defines the interface for consuming messages from Kafka topics.
type KafkaOrderHandler interface {
	Start() func()
}

// KafkaOrderConfig holds configuration and dependencies for the Kafka order consumer.
type KafkaOrderConfig struct {
	Context          context.Context
	Logger           *zap.Logger
	Config           *configs.Config
	PaymentProcessor PaymentProcessor

	// internal initialization
	consumer    *kafka.Consumer
	dlqProducer *kafka.Producer
	validate    *validator.Validate
	orderSem    chan struct{} // Semaphore to limit concurrent order processing
}

// NewKafkaOrderConsumer initializes a KafkaOrderHandler with the provided configuration.
// It sets up the Kafka consumer, DLQ producer, and semaphore based on config values.
func NewKafkaOrderConsumer(cfg KafkaOrderConfig) KafkaOrderHandler {
	// Configure Kafka consumer settings
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,       // List of Kafka broker addresses
		"group.id":           cfg.Config.KafkaConsumerGroup, // Consumer group ID for load balancing
		"auto.offset.reset":  "earliest",                    // Start reading from the earliest offset if no prior offset
		"enable.auto.commit": false,                         // Disable auto-commit for manual offset management
	}
	kafkaConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("Failed to create Kafka order consumer", zap.Error(err))
	}

	// Initialize DLQ producer for failed messages
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"acks":               "all",
		"enable.idempotence": true, // Ensure messages are not duplicated
	})
	if err != nil {
		cfg.Logger.Fatal("Failed to create DLQ producer", zap.Error(err))
	}

	// Initialize semaphore with configured max concurrent jobs
	cfg.orderSem = make(chan struct{}, cfg.Config.MaxOrdersPlacedConcurrentJobs)
	cfg.consumer = kafkaConsumer
	cfg.dlqProducer = dlqProducer
	cfg.validate = validator.New()
	return &cfg
}

// Start initiates the Kafka message consumption loop and returns a cleanup function.
// The loop runs in a goroutine, processing messages with concurrency control.
func (k *KafkaOrderConfig) Start() func() {
	// Subscribe to the configured Kafka topic
	err := k.consumer.SubscribeTopics([]string{k.Config.KafkaTopic}, nil)
	if err != nil {
		k.Logger.Fatal("Failed to subscribe to Kafka topic", zap.Error(err))
	}

	k.Logger.Info("Listening to Kafka topic",
		zap.String("topic", k.Config.KafkaTopic),
		zap.String("group", k.Config.KafkaConsumerGroup))

	go func() {
		for {
			msg, err := k.consumer.ReadMessage(-1)
			if err != nil {
				k.Logger.Error("Failed to read Kafka message", zap.Error(err))
				continue
			}
			// Acquire semaphore slot, blocking if limit is reached
			k.orderSem <- struct{}{}
			go func(m *kafka.Message) {
				defer func() { <-k.orderSem }() // Release slot after processing
				k.processMessage(m)
			}(msg)
		}
	}()

	// Return cleanup function to gracefully shut down resources
	return func() {
		if k.dlqProducer != nil {
			k.dlqProducer.Flush(5000)
			k.dlqProducer.Close()
		}
		if err := k.consumer.Close(); err != nil {
			k.Logger.Error("Failed to close Kafka consumer", zap.Error(err))
		}
		k.Logger.Info("Kafka consumer closed successfully")
	}
}

// processMessage handles the processing of a single Kafka message.
// It decodes, validates, and processes the payment job, committing or sending to DLQ as needed.
func (k *KafkaOrderConfig) processMessage(msg *kafka.Message) {
	select {
	case <-k.Context.Done():
		return // Exit if context is canceled
	default:
	}

	// Decode the incoming message into a PaymentJob struct
	var job views.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("Failed to decode Kafka message", zap.Error(err))
		k.sendToDLQ(job, "json unmarshal error", err.Error())
		_, _ = k.consumer.CommitMessage(msg) // Commit to skip invalid message
		return
	}

	// Validate the decoded job structure
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("Failed to validate payment job", zap.Error(err))
		k.sendToDLQ(job, "validation error", err.Error())
		_, _ = k.consumer.CommitMessage(msg) // Commit to skip invalid message
		return
	}

	// Process the payment job
	k.Logger.Info("Processing payment job", zap.Any("paymentJob", job))
	procErr := k.PaymentProcessor.ProcessPayment(k.Context, job)
	if procErr != nil {
		k.Logger.Error("Failed to process payment, sending to DLQ",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(procErr))
		k.sendToDLQ(job, "processPaymentError", procErr.Error())
		if _, err := k.consumer.CommitMessage(msg); err != nil {
			k.Logger.Error("Failed to commit offset after DLQ", zap.Error(err))
		}
		return
	}

	// Successfully processed, commit the offset
	if _, err := k.consumer.CommitMessage(msg); err != nil {
		k.Logger.Error("Failed to commit offset", zap.Error(err))
		return
	}
	k.Logger.Info("Payment processed successfully",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
}

// sendToDLQ sends a failed job to the Dead Letter Queue with context.
func (k *KafkaOrderConfig) sendToDLQ(job views.PaymentJob, reason, errMsg string) {
	payload := map[string]any{
		"job":           job,
		"failureReason": reason,
		"error":         errMsg,
		"failedAt":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	b, err := json.Marshal(payload)
	if err != nil {
		k.Logger.Error("Failed to marshal DLQ payload",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err))
		return
	}

	err = k.dlqProducer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &k.Config.KafkaDLQTopic,
			Partition: kafka.PartitionAny,
		},
		Key:   job.IdempotencyKey[:],
		Value: b,
	}, nil)
	if err != nil {
		k.Logger.Error("Failed to marshal DLQ payload",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err))
		return
	}
	k.Logger.Info("Sent to order DLQ",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
		zap.String("reason", reason))
}
