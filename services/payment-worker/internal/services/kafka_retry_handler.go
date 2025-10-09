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
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	logger.Info("kafka producer created successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// initializing dead letter queue retry producer
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"enable.idempotence": true,
	})
	if err != nil {
		cfg.Logger.Fatal("failed to create DLQ producer", zap.Error(err))
	}
	logger.Info("dlq retry producer created successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

	// initializing kafka retry consumer
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,       // Kafka broker(s)
		"group.id":           cfg.Config.KafkaConsumerGroup, // Consumer group
		"auto.offset.reset":  "earliest",                    // Start from the beginning of the topic
		"enable.auto.commit": false,                         // Disable auto commit
	}
	kafkaRetryConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("failed to create kafka consumer", zap.Error(err))
	}
	logger.Info("kafka retry consumer created successfully", zap.String("brokers", cfg.Config.KafkaBrokers))

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

	k.Logger.Info("listening to retry channel")

	return func() {
		// drain producer
		if k.retryProducer != nil {
			k.retryProducer.Flush(5000)
			k.retryProducer.Close()
			k.Logger.Info("retry producer closed")
		}
		if k.dlqRetryProducer != nil {
			k.dlqRetryProducer.Flush(5000)
			k.dlqRetryProducer.Close()
			k.Logger.Info("dlq retry producer closed")
		}
		if err := k.retryConsumer.Close(); err != nil {
			k.Logger.Error("retry consumer close error", zap.Error(err))
			return
		}
		k.Logger.Info("retry consumer closed")
	}
}

func (k *KafkaRetryConfig) startDeliveryReportLogger() {
	for e := range k.retryProducer.Events() {
		switch ev := e.(type) {
		case *kafka.Message:
			if ev.TopicPartition.Error != nil {
				k.Logger.Error("retry topic delivery failed",
					zap.Error(ev.TopicPartition.Error),
					zap.Any("topic_partition", ev.TopicPartition))
			} else {
				k.Logger.Debug("retry topic delivered",
					zap.Any("topic_partition", ev.TopicPartition))
			}
		}
	}
}

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

			k.Logger.Info("retrying job",
				zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
				zap.Int("retry_count", job.RetryCount))

			// increment retry count and compute backoff
			job.RetryCount++

			// Check if retry count is greater than max retry count
			if err := k.checkIfMaxRetryLimitExceeded(job); err != nil {
				// checkIfMaxRetryLimitExceeded sends the job to the DLQ topic.
				return
			}

			backoff := time.Duration(float64(job.RetryCount*k.Config.RetryBaseBackoffSec)) * time.Second

			time.AfterFunc(backoff, func() {
				k.Logger.Info("republishing after backoff",
					zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
					zap.Duration("backoff", backoff))

				// Marshal payload
				payload, mErr := json.Marshal(job)
				if mErr != nil {
					k.Logger.Error("failed to marshal job for retry publish",
						zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
						zap.Error(mErr))
					k.sendToDLQ(job, "marshal_error", mErr.Error())
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
					k.Logger.Error("failed to publish to retry topic",
						zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
						zap.Error(pErr))
					k.sendToDLQ(job, "publish_error", pErr.Error())
					return
				}
			})
		}
	}
}

// publishToDLQ publishes the given job to the retry DLQ topic.
func (k *KafkaRetryConfig) sendToDLQ(job views.PaymentJob, reason, errMsg string) {
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
		k.Logger.Error("failed to produce to retry dlq message", zap.Error(err))
		return
	}
}

func (k *KafkaRetryConfig) startRetryConsumer() {
	err := k.retryConsumer.SubscribeTopics([]string{k.Config.KafkaRetryTopic}, nil)
	if err != nil {
		k.Logger.Fatal("failed to subscribe", zap.Error(err))
	}

	k.Logger.Info("listening to topic", zap.String("topic", k.Config.KafkaRetryTopic), zap.Any("group", k.Config.KafkaConsumerGroup))
	go func() {
		for {
			msg, err := k.retryConsumer.ReadMessage(-1)
			if err != nil {
				k.Logger.Error("read error", zap.Error(err))
				continue
			}
			// Process in goroutine for concurrency
			go k.processRetryMessage(msg)
		}
	}()
}

func (k *KafkaRetryConfig) processRetryMessage(msg *kafka.Message) {
	select {
	case <-k.Context.Done():
		return
	default:
	}

	// decode message
	var job views.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("failed to decode message", zap.Error(err))
		k.sendToDLQ(job, "json_unmarshal_error", err.Error())
		// commit to skip this bad message
		_, _ = k.retryConsumer.CommitMessage(msg)
		return
	}

	// Validate after unmarshal
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("failed to validate message", zap.Error(err))
		k.sendToDLQ(job, "validate error", err.Error())
		// commit to skip this bad message
		_, _ = k.retryConsumer.CommitMessage(msg)
		return
	}

	// Check if retry count is greater than max retry count
	if err := k.checkIfMaxRetryLimitExceeded(job); err != nil {
		// checkIfMaxRetryLimitExceeded sends the job to the DLQ topic.
		return
	}

	// process message
	k.Logger.Info("processing message", zap.Any("paymentJob", job))
	procErr := k.PaymentService.HandlePayment(k.Context, job)
	if procErr != nil {
		k.Logger.Error("failed to process payment; sending to DLQ", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(procErr))
		k.sendToDLQ(job, "process_payment_error", procErr.Error())
		// commit to avoid reprocessing the poison message
		if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
			k.Logger.Error("failed to commit offset after DLQ", zap.Error(err))
		}
		return
	}

	// success, commit offset
	if _, err := k.retryConsumer.CommitMessage(msg); err != nil {
		k.Logger.Error("failed to commit offset", zap.Error(err))
		return
	}
	k.Logger.Info("payment processed successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
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

		k.Logger.Error("max retry count exceeded",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Int("retry_count", job.RetryCount))
		_ = k.OrderRepo.UpdateStatusIdempotencyID(k.Context, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "max retry count exceeded")
		k.sendToDLQ(job, "max_retry_exceeded", fmt.Sprintf("max retry count exceeded: %d", k.Config.MaxRetryCount))
		return errors.New("max retry count exceeded")
	}
	return nil
}
