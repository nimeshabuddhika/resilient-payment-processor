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
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

// KafkaRetryHandler defines the interface for retrying failed payment jobs.
type KafkaRetryHandler interface {
	Start() func()
}

// KafkaRetryConfig contains dependencies and configuration for the retry handler.
type KafkaRetryConfig struct {
	Context        context.Context
	Logger         *zap.Logger
	RetryChan      chan views.PaymentJob
	Config         *configs.Config
	PaymentService PaymentService
	DB             *database.DB
	OrderRepo      repositories.OrderRepository

	//internal initialization
	validate         *validator.Validate
	dlqRetryProducer *kafka.Producer
	retryProducer    *kafka.Producer
	retryConsumer    *kafka.Consumer
}

// NewKafkaRetryHandler constructs a KafkaRetryHandler with the given configuration.
func NewKafkaRetryHandler(cfg KafkaRetryConfig) KafkaRetryHandler {
	// initialize kafka retry producer
	retryProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers, // Kafka broker(s)
		"acks":               "all",                   // Wait for all replicas
		"enable.idempotence": "true",                  // Ensure messages are not sent twice
		"retries":            cfg.Config.KafkaRetry,   // Built-in retry mechanism
	})
	if err != nil {
		logger.Fatal("failed_to_create_kafka producer", zap.Error(err))
	}
	logger.Info("kafka_producer_created_successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// initializing dead letter queue retry producer
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"enable.idempotence": true,
	})
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_retry_dlq_producer", zap.Error(err))
	}
	logger.Info("retry_dlq_retry_producer_created_successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// initializing kafka retry consumer
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,       // Kafka broker(s)
		"group.id":           cfg.Config.KafkaConsumerGroup, // Consumer group
		"auto.offset.reset":  "earliest",                    // Start from the beginning of the topic
		"enable.auto.commit": false,                         // Disable auto commit
	}
	kafkaRetryConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_kafka_consumer", zap.Error(err))
	}
	logger.Info("kafka_retry_consumer_created_successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

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

	k.Logger.Info("listening_to_retry_channel")

	return func() {
		// drain producer
		if k.retryProducer != nil {
			k.retryProducer.Flush(5000)
			k.retryProducer.Close()
			k.Logger.Info("retry_producer_closed")
		}
		if k.dlqRetryProducer != nil {
			k.dlqRetryProducer.Flush(5000)
			k.dlqRetryProducer.Close()
			k.Logger.Info("dlq_retry_producer closed")
		}
		if err := k.retryConsumer.Close(); err != nil {
			k.Logger.Error("retry_consumer_close_error", zap.Error(err))
			return
		}
		k.Logger.Info("retry_consumer_closed")
	}
}

// startRetryChannelHandler reads from the retry channel and retries failed jobs.
func (k *KafkaRetryConfig) startRetryChannelHandler() {
	for {
		select {
		case <-k.Context.Done():
			// try to flush outstanding messages briefly
			_ = k.retryProducer.Flush(2000)
			return
		case job, ok := <-k.RetryChan:
			if !ok {
				return
			}

			// increment retry count and compute backoff
			job.RetryCount++

			// Check if retry count is greater than max retry count
			if err := k.checkIfMaxRetryLimitExceeded(job); err != nil {
				// checkIfMaxRetryLimitExceeded prints the logs and sends the job to the retry dlq topic.
				return
			}

			k.Logger.Info("publishing_failed_job_to_retry_topic",
				zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
				zap.Int("retry_count", job.RetryCount))

			job.UpdatedAt = time.Now() // update updated at time to calculate backoff

			// Marshal payload
			payload, mErr := json.Marshal(job)
			if mErr != nil {
				k.Logger.Error("failed_to_marshal_job_for_retry",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Error(mErr))
				k.sendToRetryDLQ(job, "marshal_error", mErr.Error())
				return
			}

			// Deterministic partitioning by user ID to balanced load
			partition := int32(job.UserID.ID() % k.Config.KafkaPartition)

			// Publish to retry topic
			topic := k.Config.KafkaRetryTopic
			kafkaMsg := &kafka.Message{
				TopicPartition: kafka.TopicPartition{
					Topic:     &topic,
					Partition: partition,
				},
				Key:       []byte(job.UserID.String()),
				Value:     payload,
				Timestamp: time.Now(),
			}

			if pErr := k.retryProducer.Produce(kafkaMsg, nil); pErr != nil {
				k.Logger.Error("failed_to_publish_to_retry_topic",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Error(pErr))
				k.sendToRetryDLQ(job, "failed to publish to retry topic", pErr.Error())
				return
			}
		}
	}
}

// startRetryConsumer starts a consumer for the retry topic.
func (k *KafkaRetryConfig) startRetryConsumer() {
	err := k.retryConsumer.SubscribeTopics([]string{k.Config.KafkaRetryTopic}, nil)
	if err != nil {
		k.Logger.Fatal("failed_to_subscribe", zap.Error(err))
	}

	// Limit concurrent jobs
	sem := make(chan struct{}, k.Config.MaxOrdersRetryConcurrentJobs)

	k.Logger.Info("listening_to_topic", zap.String("topic", k.Config.KafkaRetryTopic), zap.Any("group", k.Config.KafkaConsumerGroup))
	go func() {
		for {
			msg, err := k.retryConsumer.ReadMessage(-1)
			if err != nil {
				k.Logger.Error("read_message_error", zap.Error(err))
				continue
			}
			// Process in goroutine for concurrency
			sem <- struct{}{} // Acquire slot, blocks if at limit
			go func(m *kafka.Message) {
				defer func() { <-sem }() // Release slot
				go k.processRetryMessage(msg)
			}(msg)
		}
	}()
}

// publishToDLQ publishes the given job to the retry DLQ topic.
func (k *KafkaRetryConfig) sendToRetryDLQ(job views.PaymentJob, reason, errMsg string) {
	payload := map[string]any{
		"job":            job,
		"failure_reason": reason,
		"error":          errMsg,
		"failed_at":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	b, _ := json.Marshal(payload)

	err := k.dlqRetryProducer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &k.Config.KafkaRetryDLQTopic,
			Partition: kafka.PartitionAny,
		},
		Key:   job.IdempotencyKey[:],
		Value: b,
	}, nil)
	if err != nil {
		k.Logger.Error("failed_to_produce_to_retry_dlq_message", zap.Error(err))
		return
	}
}

// processRetryMessage processes a retry message.
// Decodes the message, validates it, and processes it.
// Commits the message if the payment is successful.
// Sends the message to the DLQ topic if the payment fails.
// Commits the message if the payment fails.
func (k *KafkaRetryConfig) processRetryMessage(msg *kafka.Message) {
	select {
	case <-k.Context.Done():
		return
	default:
	}

	// decode message
	var job views.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("failed_to_decode_message", zap.Error(err))
		k.sendToRetryDLQ(job, "json_unmarshal_error", err.Error())
		// commit to skip this bad message
		_, _ = k.retryConsumer.CommitMessage(msg)
		return
	}

	// Validate after unmarshal
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("failed to validate message", zap.Error(err))
		k.sendToRetryDLQ(job, "validate error", err.Error())
		// commit to skip this bad message
		_, _ = k.retryConsumer.CommitMessage(msg)
		return
	}

	// Check if retry count is greater than max retry count
	if err := k.checkIfMaxRetryLimitExceeded(job); err != nil {
		// checkIfMaxRetryLimitExceeded sends the job to the DLQ topic.
		return
	}

	// Backoff
	backoff := utils.CalculateExponentialBackoffWithJitter(job.RetryCount, k.Config.RetryBaseBackoff, k.Config.MaxRetryBackoff)
	timeDurationToExecute := job.UpdatedAt.Add(backoff).Sub(time.Now())
	k.Logger.Info("job_waiting_to_be_executed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("time_duration_to_execute", timeDurationToExecute), zap.Int("retry_count", job.RetryCount))

	// Job will be executed after the backoff duration
	time.AfterFunc(timeDurationToExecute, func() {
		// process message
		k.Logger.Info("processing_message", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Int("retry_count", job.RetryCount))
		procErr := k.PaymentService.HandlePayment(k.Context, job)
		if procErr != nil {
			k.Logger.Error("failed_to_process_payment_sending_to_retry_dlq", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(procErr))
			k.sendToRetryDLQ(job, "process_payment_error", procErr.Error())
			// commit to avoid reprocessing the poison message
			if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
				k.Logger.Error("failed_to_commit_offset_after_retry_dlq", zap.Error(err))
			}
			return
		}

		// success, commit offset
		if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
			k.Logger.Error("failed_to_commit_offset", zap.Error(err))
			return
		}
		k.Logger.Info("payment_processed_successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	})
}

// checkIfMaxRetryLimitExceeded checks if the retry count is greater than the max retry count.
// If so, it sends the job to the DLQ topic.
// Updates the order status to failed.
func (k *KafkaRetryConfig) checkIfMaxRetryLimitExceeded(job views.PaymentJob) error {
	// begin transaction
	tx, _ := k.DB.Begin(k.Context)
	defer func() {
		_ = k.DB.Commit(k.Context, tx)
	}()
	if job.RetryCount > k.Config.MaxRetryCount {

		k.Logger.Error("max_retry_count_exceeded",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Int("retry_count", job.RetryCount))
		_ = k.OrderRepo.UpdateStatusIdempotencyID(k.Context, tx, job.IdempotencyKey, pkg.OrderStatusFailed, fmt.Sprintf("failed transaction after %d retries", job.RetryCount))
		k.sendToRetryDLQ(job, "max_retry_exceeded", fmt.Sprintf("max retry count exceeded: %d", k.Config.MaxRetryCount))
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
				k.Logger.Error("retry_topic_delivery_failed",
					zap.Error(ev.TopicPartition.Error),
					zap.Any("topic_partition", ev.TopicPartition))
			} else {
				k.Logger.Debug("retry_topic_delivered",
					zap.Any("topic_partition", ev.TopicPartition))
			}
		}
	}
}
