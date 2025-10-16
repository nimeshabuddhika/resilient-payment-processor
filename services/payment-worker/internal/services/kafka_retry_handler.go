package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/dtos"
	kafkautils "github.com/nimeshabuddhika/resilient-payment-processor/pkg/kafka"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

// KafkaRetryHandler defines the contract for retrying failed payment jobs in the
// payment-worker service. Implementations are responsible for:
//   - reading from an in-memory retry channel and publishing to a Kafka retry topic,
//   - consuming from the Kafka retry topic and processing jobs with backoff,
//   - publishing unrecoverable jobs to a retry DLQ.
type KafkaRetryHandler interface {
	Start() func()
}

// KafkaRetryConfig holds runtime configuration and dependencies for the retry handler.
// All fields are required and injected by the composition root (main/wiring).
type KafkaRetryConfig struct {
	Context          context.Context
	Logger           *zap.Logger
	RetryChannel     <-chan dtos.PaymentJob
	Config           *configs.Config
	PaymentProcessor PaymentProcessor
	DB               *database.DB
	OrderRepo        repositories.OrderRepository

	// internal initialization (set by NewKafkaRetryHandler)
	validate         *validator.Validate
	dlqRetryProducer *kafka.Producer
	retryProducer    *kafka.Producer
	retryConsumer    *kafka.Consumer
	retrySemaphore   chan struct{} // semaphore to bound concurrent retry processing
}

// NewKafkaRetryHandler builds a KafkaRetryHandler and wires producers, consumer,
// topics, validation, and a concurrency semaphore. No side effects beyond wiring.
func NewKafkaRetryHandler(cfg KafkaRetryConfig) KafkaRetryHandler {
	// initialize Kafka topics (retry + DLQ) with retention policies
	topicConfig := kafkautils.KafkaConfig{
		BootstrapServers: cfg.Config.KafkaBrokers,
		Topics: []kafkautils.TopicConfig{
			{
				Topic:             cfg.Config.KafkaRetryTopic,
				NumPartitions:     int(cfg.Config.KafkaPartition),
				ReplicationFactor: 1,
				Config: map[string]string{
					"retention.ms": fmt.Sprintf("%d", cfg.Config.KafkaRetryRetention.Milliseconds()),
				},
			},
			{
				Topic:             cfg.Config.KafkaRetryDLQTopic,
				NumPartitions:     int(cfg.Config.KafkaPartition),
				ReplicationFactor: 1,
				Config: map[string]string{
					"retention.ms": fmt.Sprintf("%d", cfg.Config.KafkaRetryDLQRetention.Milliseconds()),
				},
			},
		},
	}
	if err := kafkautils.InitKafkaTopics(cfg.Logger, cfg.Context, topicConfig); err != nil {
		logger.Fatal("failed_to_initialize_kafka_topics", zap.Error(err))
	}

	// retry producer
	retryProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"acks":               "all",
		"enable.idempotence": true,
		"retries":            "1",
	})
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_kafka_producer", zap.Error(err))
	}
	cfg.Logger.Info("kafka_producer_created_successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// retry DLQ producer
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"acks":               "all",
		"enable.idempotence": true,
	})
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_retry_dlq_producer", zap.Error(err))
	}
	cfg.Logger.Info("retry_dlq_producer_created_successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// retry consumer
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":          cfg.Config.KafkaBrokers,
		"group.id":                   cfg.Config.KafkaRetryConsumerGroup,
		"auto.offset.reset":          "earliest",
		"enable.auto.commit":         false,
		"queued.max.messages.kbytes": "65536", // cap client-side prefetch to ~64MB to avoid burst memory spikes
		"fetch.wait.max.ms":          50,      // allow small batching without adding too much latency

		// --- Robust group behavior for multi-replica deployments ---
		"partition.assignment.strategy": "cooperative-sticky", // incremental rebalances
		"session.timeout.ms":            45000,                // tolerate brief stalls
		"heartbeat.interval.ms":         3000,
		"max.poll.interval.ms":          600000, // up to 10 min processing between polls
	}
	// Add a useful client.id for observability
	if host, err := os.Hostname(); err == nil && host != "" {
		cfg.Logger.Info("setting_client_id", zap.String("client_id", host))
		_ = kafkaConfig.SetKey("client.id", host)
	}

	kafkaRetryConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_retry_consumer", zap.Error(err))
	}
	cfg.Logger.Info("retry_consumer_created_successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// internal wiring
	cfg.retrySemaphore = make(chan struct{}, cfg.Config.MaxOrdersRetryConcurrentJobs)
	cfg.retryProducer = retryProducer
	cfg.dlqRetryProducer = dlqProducer
	cfg.retryConsumer = kafkaRetryConsumer
	cfg.validate = validator.New()
	return &cfg
}

// Start spins up goroutines for delivery reports, channel-based retries, and the Kafka consumer.
// It returns a close function to flush/close producers and the consumer gracefully.
func (k *KafkaRetryConfig) Start() func() {
	go k.startDeliveryReportLogger() // non-blocking delivery report reader
	go k.startRetryChannelHandler()  // publish to retry topic from in-memory channel
	go k.startRetryConsumer()        // consume/process retry topic

	k.Logger.Info("listening_to_retry_channel")

	return func() {
		// drain and close producers/consumer
		if k.retryProducer != nil {
			k.retryProducer.Flush(5000)
			k.retryProducer.Close()
			k.Logger.Info("retry_producer_closed_successfully")
		}
		if k.dlqRetryProducer != nil {
			k.dlqRetryProducer.Flush(5000)
			k.dlqRetryProducer.Close()
			k.Logger.Info("retry_dlq_producer_closed_successfully")
		}
		if err := k.retryConsumer.Close(); err != nil {
			k.Logger.Error("failed_to_close_retry_consumer", zap.Error(err))
		}
		k.Logger.Info("retry_consumer_closed_successfully")
	}
}

// startRetryChannelHandler reads failed jobs from the in-memory retry channel,
// increments retry metadata, computes backoff, and publishes to the retry topic immediately.
func (k *KafkaRetryConfig) startRetryChannelHandler() {
	for {
		select {
		case <-k.Context.Done():
			return
		case job, ok := <-k.RetryChannel:
			if !ok {
				return
			}

			// increment retry and enforce max
			job.RetryCount++
			if job.RetryCount > k.Config.MaxRetryCount {
				k.Logger.Error("maximum_retry_count_exceeded_in_channel",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Int("retry_count", job.RetryCount),
				)
				k.sendToRetryDLQ(job, "maxRetryExceeded", fmt.Sprintf("Maximum retry count exceeded: %d", k.Config.MaxRetryCount))
				continue
			}

			// compute backoff and set next retry time
			delay := utils.CalculateExponentialBackoffWithJitter(job.RetryCount, k.Config.RetryBaseBackoff, k.Config.MaxRetryBackoff)
			job.NextRetryTime = time.Now().Add(delay)

			// serialize and publish
			payload, err := json.Marshal(job)
			if err != nil {
				k.Logger.Error("failed_to_marshal_retry_job",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Error(err),
				)
				continue
			}
			if err := k.retryProducer.Produce(&kafka.Message{
				TopicPartition: kafka.TopicPartition{Topic: &k.Config.KafkaRetryTopic, Partition: kafka.PartitionAny},
				Key:            job.IdempotencyKey[:],
				Value:          payload,
			}, nil); err != nil {
				k.Logger.Error("failed_to_produce_to_retry_topic",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Error(err),
				)
				continue
			}

			k.Logger.Info("published_to_retry_topic",
				zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
				zap.Int("retry_count", job.RetryCount),
				zap.Time("next_retry_time", job.NextRetryTime),
			)
		}
	}
}

// startRetryConsumer subscribes to the retry topic and continuously reads messages
// for processing. Each message is handled in its own goroutine (bounded by semaphore).
func (k *KafkaRetryConfig) startRetryConsumer() {
	if err := k.retryConsumer.SubscribeTopics([]string{k.Config.KafkaRetryTopic}, nil); err != nil {
		k.Logger.Fatal("failed_to_subscribe_retry_topic", zap.Error(err))
	}

	k.Logger.Info("listening_to_retry_topic",
		zap.String("topic", k.Config.KafkaRetryTopic),
		zap.String("group", k.Config.KafkaRetryConsumerGroup),
	)

	for {
		msg, err := k.retryConsumer.ReadMessage(-1)
		if err != nil {
			k.Logger.Error("failed_to_read_retry_message", zap.Error(err))
			continue
		}
		go k.processRetryMessage(msg)
	}
}

// processRetryMessage enforces backoff, processes a payment, and commits offsets.
// Messages that cannot be recovered are sent to the retry DLQ.
func (k *KafkaRetryConfig) processRetryMessage(msg *kafka.Message) {
	// early exit if shutting down
	select {
	case <-k.Context.Done():
		return
	default:
	}

	// acquire semaphore with context
	select {
	case k.retrySemaphore <- struct{}{}:
	case <-k.Context.Done():
		return
	}
	defer func() { <-k.retrySemaphore }()

	// decode
	var job dtos.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("failed_to_decode_message", zap.Error(err))
		k.sendToRetryDLQ(job, "Json unmarshal error", err.Error())
		_, _ = k.retryConsumer.CommitMessage(msg) // commit to skip bad message
		return
	}

	// validate
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("failed_to_validate_message", zap.Error(err))
		k.sendToRetryDLQ(job, "Validate Error", err.Error())
		_, _ = k.retryConsumer.CommitMessage(msg) // commit to skip bad message
		return
	}

	// max retry check (handles DLQ + commit internally)
	if err := k.checkIfMaxRetryLimitExceeded(job, msg); err != nil {
		return
	}

	// enforce backoff using NextRetryTime in payload
	if time.Now().Before(job.NextRetryTime) {
		sleepDuration := time.Until(job.NextRetryTime)
		k.Logger.Info("enforcing_backoff_delay",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Duration("sleep_duration", sleepDuration),
			zap.Int("retry_count", job.RetryCount),
		)
		time.Sleep(sleepDuration)
	}

	// re-check context after sleep
	select {
	case <-k.Context.Done():
		return // do not commit; allow re-delivery
	default:
	}

	// process payment
	k.Logger.Info("processing_retry_message",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
		zap.Int("retry_count", job.RetryCount),
	)
	if procErr := k.PaymentProcessor.ProcessPayment(k.Context, job); procErr != nil {
		k.Logger.Error("failed_to_process_payment_sending_to_dlq",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(procErr),
		)
		k.sendToRetryDLQ(job, "Process payment error", procErr.Error())
		if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
			k.Logger.Error("failed_to_commit_offset_after_dlq", zap.Error(err))
		}
		return
	}

	// success: commit offset
	if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
		k.Logger.Error("failed_to_commit_offset",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err),
		)
		return
	}
	k.Logger.Info("retry_payment_processed_successfully",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
	)
}

// sendToRetryDLQ publishes a job and failure context to the retry DLQ topic.
// This function does not commit consumer offsets—callers must decide when to commit.
func (k *KafkaRetryConfig) sendToRetryDLQ(job dtos.PaymentJob, reason, errMsg string) {
	payload := map[string]any{
		"job":           job,
		"failureReason": reason,
		"error":         errMsg,
		"failedAt":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	b, err := json.Marshal(payload)
	if err != nil {
		k.Logger.Error("failed_to_marshal_dlq_payload",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err),
		)
		return
	}

	if err := k.dlqRetryProducer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &k.Config.KafkaRetryDLQTopic,
			Partition: kafka.PartitionAny,
		},
		Key:   job.IdempotencyKey[:],
		Value: b,
	}, nil); err != nil {
		k.Logger.Error("failed_to_produce_to_retry_dlq", zap.Error(err))
		return
	}

	k.Logger.Info("sent_to_retry_dlq",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
		zap.String("reason", reason),
	)
}

// checkIfMaxRetryLimitExceeded updates state, DLQs, and commits if the job exceeded
// the configured maximum retry count. Returns an error when the limit is exceeded.
func (k *KafkaRetryConfig) checkIfMaxRetryLimitExceeded(job dtos.PaymentJob, msg *kafka.Message) error {
	tx, err := k.DB.Begin(k.Context)
	if err != nil {
		k.Logger.Error("failed_to_begin_transaction_for_max_retry_check", zap.Error(err))
		return err
	}
	defer func() {
		if commitErr := k.DB.Commit(k.Context, tx); commitErr != nil {
			k.Logger.Error("failed_to_commit_transaction_for_max_retry_check", zap.Error(commitErr))
		}
	}()

	if job.RetryCount > k.Config.MaxRetryCount {
		k.Logger.Error("maximum_retry_count_exceeded",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Int("retry_count", job.RetryCount),
		)

		affectedRows, err := k.OrderRepo.UpdateStatusByIdempotencyID(
			k.Context, tx, job.IdempotencyKey, pkg.OrderStatusFailed,
			fmt.Sprintf("failed transaction after %d retries", job.RetryCount),
		)
		if err != nil {
			k.Logger.Error("failed_to_update_order_status_for_max_retry", zap.Error(err))
		}
		k.Logger.Info("max_retry_exceeded", zap.Int64("affected_rows", affectedRows))
		k.sendToRetryDLQ(job, "max retry exceeded", fmt.Sprintf("Max retry count exceeded: %d", k.Config.MaxRetryCount))

		if _, commitErr := k.retryConsumer.CommitMessage(msg); commitErr != nil {
			k.Logger.Error("failed_to_commit_after_max_retry_dlq",
				zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
				zap.Error(commitErr),
			)
		}
		return errors.New("max retry count exceeded")
	}

	return nil
}

// startDeliveryReportLogger logs async delivery reports emitted by the retry producer.
// It uses debug level for success and error level for failures.
func (k *KafkaRetryConfig) startDeliveryReportLogger() {
	for e := range k.retryProducer.Events() {
		switch ev := e.(type) {
		case *kafka.Message:
			if ev.TopicPartition.Error != nil {
				k.Logger.Error("retry_topic_delivery_failed",
					zap.Error(ev.TopicPartition.Error),
					zap.Any("topic_partition", ev.TopicPartition),
				)
			} else {
				k.Logger.Debug("retry_topic_delivered_successfully",
					zap.Any("topic_partition", ev.TopicPartition),
				)
			}
		}
	}
}
