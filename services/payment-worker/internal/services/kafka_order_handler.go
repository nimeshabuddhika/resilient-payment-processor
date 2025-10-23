package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/dtos"
	kafkautils "github.com/nimeshabuddhika/resilient-payment-processor/pkg/kafka"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

// KafkaOrderHandler consumes messages from Kafka, processes them with bounded concurrency,
// and pushes failures to a DLQ.
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
	orderConsumer *kafka.Consumer
	dlqProducer   *kafka.Producer
	validate      *validator.Validate
	orderSem      chan struct{} // Semaphore to limit concurrent order processing
}

// NewKafkaOrderConsumer initializes a KafkaOrderHandler with the provided configuration.
// It sets up the Kafka consumer, DLQ producer, and semaphore based on config values.
func NewKafkaOrderConsumer(cfg KafkaOrderConfig) KafkaOrderHandler {
	// Initialize kafka topic configuration
	topicConfig := kafkautils.KafkaConfig{
		BootstrapServers: cfg.Config.KafkaBrokers,
		Topics: []kafkautils.TopicConfig{
			{
				Topic:             cfg.Config.KafkaDLQTopic,
				NumPartitions:     int(cfg.Config.KafkaPartition),
				ReplicationFactor: 1, // Single partition
				Config: map[string]string{
					"retention.ms": fmt.Sprintf("%d", cfg.Config.KafkaDLQRetention.Milliseconds()),
				},
			},
		},
	}
	err := kafkautils.InitKafkaTopics(cfg.Logger, cfg.Context, topicConfig)
	if err != nil {
		logger.Fatal("failed to initialize kafka topics", zap.Error(err))
	}

	// Configure Kafka consumer settings
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,            // List of Kafka broker addresses
		"group.id":           cfg.Config.KafkaOrderConsumerGroup, // Consumer group ID for load balancing
		"auto.offset.reset":  "earliest",                         // Start reading from the earliest offset if no prior offset
		"enable.auto.commit": false,                              // Disable auto-commit for manual offset management
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
	cfg.orderConsumer = kafkaConsumer
	cfg.dlqProducer = dlqProducer
	cfg.validate = validator.New()
	return &cfg
}

// Start initiates the Kafka message consumption loop and returns a cleanup function.
// The loop runs in a goroutine, processing messages with concurrency control.
func (k *KafkaOrderConfig) Start() func() {
	// Subscribe to the configured Kafka topic
	err := k.orderConsumer.SubscribeTopics([]string{k.Config.KafkaOrderTopic}, nil)
	if err != nil {
		k.Logger.Fatal("Failed to subscribe to Kafka topic", zap.Error(err))
	}

	k.Logger.Info("listening_to_kafka",
		zap.String("topic", k.Config.KafkaOrderTopic),
		zap.String("group", k.Config.KafkaOrderConsumerGroup))
	go func() {
		for {
			msg, err := k.orderConsumer.ReadMessage(-1)
			if err != nil {
				k.Logger.Error("failed_to_read_kafka_message", zap.Error(err))
				continue
			}
			// Acquire semaphore slot, blocking if limit is reached
			k.Logger.Info("received_message", zap.Int("semaphore_size", len(k.orderSem)))
			k.orderSem <- struct{}{}
			go func(m *kafka.Message) {
				defer func() {
					<-k.orderSem // Release slot after processing
					k.Logger.Info("released_order_semaphore", zap.Int("semaphore_size", len(k.orderSem)))
				}()
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
		if err := k.orderConsumer.Close(); err != nil {
			k.Logger.Error("failed_to_close_Kafka consumer", zap.Error(err))
		}
		k.Logger.Info("kafka_consumer_closed_successfully")
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
	var job dtos.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("decode_message_failed", zap.Error(err))
		k.sendToDLQ(job, "json_unmarshal_error", err.Error())
		k.Commit(uuid.Nil, msg) // Commit to skip invalid message
		return
	}

	// Validate the decoded job structure
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("validate_job_failed", zap.Error(err))
		k.sendToDLQ(job, "validation_error", err.Error())
		k.Commit(job.IdempotencyKey, msg) // Commit to skip invalid message
		return
	}

	// Process the payment job
	k.Logger.Info("processing_payment_job", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	procErr := k.PaymentProcessor.ProcessPayment(k.Context, job)
	if procErr != nil {
		k.Logger.Error("process_payment_failed_sending_to_dlq",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(procErr))
		k.sendToDLQ(job, "processPaymentError", procErr.Error())
		// Commit to offset
		k.Commit(job.IdempotencyKey, msg)
		return
	}
	k.Logger.Info("payment_processed_successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	// Successfully processed, commit the offset
	k.Commit(job.IdempotencyKey, msg)
}

// Commit kafka message
func (k *KafkaOrderConfig) Commit(idempotencyKey uuid.UUID, msg *kafka.Message) {
	// Successfully processed, commit the offset
	if _, err := k.orderConsumer.CommitMessage(msg); err != nil {
		k.Logger.Error("order_failed_to_commit",
			zap.Any(pkg.IdempotencyKey, idempotencyKey),
			zap.Error(err))
		return
	}
	k.Logger.Info("order_commited_successfully",
		zap.Any(pkg.IdempotencyKey, idempotencyKey))
}

// sendToDLQ sends a failed job to the Dead Letter Queue with context.
func (k *KafkaOrderConfig) sendToDLQ(job dtos.PaymentJob, reason, errMsg string) {
	payload := map[string]any{
		"job":           job,
		"failureReason": reason,
		"error":         errMsg,
		"failedAt":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	b, err := json.Marshal(payload)
	if err != nil {
		k.Logger.Error("marshal_dlq_payload_failed",
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
		k.Logger.Error("produce_dlq_failed",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err))
		return
	}
	k.Logger.Info("sent_to_dlq",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
		zap.String("reason", reason))
}
