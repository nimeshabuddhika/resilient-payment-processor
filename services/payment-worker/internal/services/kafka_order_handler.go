package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/go-playground/validator/v10"
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

	// internal
	consumer    *kafka.Consumer
	dlqProducer *kafka.Producer
	validate    *validator.Validate

	// concurrency & intake
	orderSem chan struct{}       // in-flight counter (capacity == max concurrent jobs)
	jobs     chan *kafka.Message // bounded work queue
	workerWG sync.WaitGroup      // for graceful shutdown
}

// NewKafkaOrderConsumer initializes a KafkaOrderHandler with the provided configuration.
// It sets up the Kafka consumer, DLQ producer, and semaphore based on config values.
func NewKafkaOrderConsumer(cfg *KafkaOrderConfig) KafkaOrderHandler {
	// Ensure DLQ topic exists with desired retention.
	topicConfig := kafkautils.KafkaConfig{
		BootstrapServers: cfg.Config.KafkaBrokers,
		Topics: []kafkautils.TopicConfig{
			{
				Topic:             cfg.Config.KafkaDLQTopic,
				NumPartitions:     int(cfg.Config.KafkaPartition),
				ReplicationFactor: 1,
				Config: map[string]string{
					"retention.ms": fmt.Sprintf("%d", cfg.Config.KafkaDLQRetention.Milliseconds()),
				},
			},
		},
	}
	if err := kafkautils.InitKafkaTopics(cfg.Logger, cfg.Context, topicConfig); err != nil {
		cfg.Logger.Fatal("failed_to_initialize_kafka_topics", zap.Error(err))
	}

	// Confluent consumer configuration
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":          cfg.Config.KafkaBrokers,
		"group.id":                   cfg.Config.KafkaOrderConsumerGroup,
		"auto.offset.reset":          "earliest",
		"enable.auto.commit":         false,   // manually commit after processing
		"queued.max.messages.kbytes": "65536", // cap client-side prefetch to ~64MB to avoid burst memory spikes
		"fetch.wait.max.ms":          50,      // allow small batching without adding too much latency

		// Robust group behavior for multi-replica deployments
		"partition.assignment.strategy": "cooperative-sticky", // incremental rebalances
		"session.timeout.ms":            45000,                // tolerate brief stalls
		"heartbeat.interval.ms":         3000,
		"max.poll.interval.ms":          600000, // up to 10 min processing between polls
	}

	// Add a client.id for observability
	if host, err := os.Hostname(); err == nil && host != "" {
		cfg.Logger.Info("setting_client_id", zap.String("client_id", host))
		_ = kafkaConfig.SetKey("client.id", host)
	}

	kafkaConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_kafka_consumer", zap.Error(err))
	}

	// DLQ producer (idempotent)
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"acks":               "all",
		"enable.idempotence": true,
	})
	if err != nil {
		cfg.Logger.Fatal("failed_to_create_dlq_producer", zap.Error(err))
	}

	// Concurrency primitives
	cfg.orderSem = make(chan struct{}, cfg.Config.MaxOrdersPlacedConcurrentJobs)
	cfg.jobs = make(chan *kafka.Message, cfg.Config.MaxOrdersPlacedConcurrentJobs) // small buffer; bounded

	cfg.Logger.Info("initialized_order_concurrency",
		zap.Int("max_concurrent_jobs", cfg.Config.MaxOrdersPlacedConcurrentJobs))

	cfg.consumer = kafkaConsumer
	cfg.dlqProducer = dlqProducer
	cfg.validate = validator.New()
	return cfg
}

// Start subscribes and runs the intake loop with pause/resume backpressure.
// Intake uses timed polling, periodically check capacity and adjust.
func (k *KafkaOrderConfig) Start() func() {
	if err := k.consumer.SubscribeTopics([]string{k.Config.KafkaOrderTopic}, nil); err != nil {
		k.Logger.Fatal("failed_to_subscribe_topic",
			zap.String("topic", k.Config.KafkaOrderTopic),
			zap.Error(err))
	}

	k.Logger.Info("listening_to_kafka",
		zap.String("topic", k.Config.KafkaOrderTopic),
		zap.String("group", k.Config.KafkaOrderConsumerGroup))

	// Spin up the bounded worker pool: N == MaxOrdersPlacedConcurrentJobs
	k.startWorkers(k.Config.MaxOrdersPlacedConcurrentJobs)

	go func() {
		paused := false
		for {
			// If saturated, pause partitions to stop client-side prefetch growth.
			if len(k.orderSem) == cap(k.orderSem) {
				parts, _ := k.consumer.Assignment()
				if !paused && len(parts) > 0 {
					if err := k.consumer.Pause(parts); err == nil {
						paused = true
						k.Logger.Warn("consumer_paused_due_to_backpressure",
							zap.Int("inflight", len(k.orderSem)),
							zap.Int("capacity", cap(k.orderSem)))
					} else {
						k.Logger.Error("failed_to_pause_partitions", zap.Error(err))
					}
				}

				// CRITICAL: keep heartbeats & group callbacks served while paused.
				// Without this, multi-replica groups flap (“unknown member id”, member failed).
				_ = k.consumer.Poll(0)
				time.Sleep(20 * time.Millisecond)
				continue
			}

			// If previously paused, resume after draining below half capacity.
			if paused && len(k.orderSem) < cap(k.orderSem)/2 {
				parts, _ := k.consumer.Assignment()
				if len(parts) > 0 {
					if err := k.consumer.Resume(parts); err == nil {
						paused = false
						k.Logger.Info("consumer_resumed_after_drain",
							zap.Int("inflight", len(k.orderSem)),
							zap.Int("capacity", cap(k.orderSem)))
					} else {
						k.Logger.Error("failed_to_resume_partitions", zap.Error(err))
					}
				}
			}

			// Timed poll to keep checking capacity
			msg, err := k.consumer.ReadMessage(100) // 100ms
			if err != nil {
				var ke kafka.Error
				if errors.As(err, &ke) && ke.Code() == kafka.ErrTimedOut {
					// serve callbacks / heartbeats anyway
					_ = k.consumer.Poll(0)
					continue // poll tick
				}
				k.Logger.Error("read_message_failed", zap.Error(err))
				// still service callbacks before next loop
				_ = k.consumer.Poll(0)
				continue
			}

			// Dispatch to workers; block here when queue is full (bounded by channel size)
			select {
			case k.jobs <- msg:
			case <-k.Context.Done():
				return
			}
		}
	}()

	// Cleanup function
	return func() {
		// stop intake
		close(k.jobs)
		// wait all workers to finish current tasks
		k.workerWG.Wait()

		if k.dlqProducer != nil {
			k.dlqProducer.Flush(5000)
			k.dlqProducer.Close()
		}
		if err := k.consumer.Close(); err != nil {
			k.Logger.Error("kafka_consumer_close_failed", zap.Error(err))
		}
		k.Logger.Info("kafka_consumer_closed")
	}
}

// startWorkers launches n workers reading from k.jobs.
// Each job increments the in-flight counter on start and decrements on completion.
func (k *KafkaOrderConfig) startWorkers(n int) {
	k.workerWG.Add(n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer k.workerWG.Done()
			for msg := range k.jobs {
				// mark in-flight
				k.orderSem <- struct{}{}
				func() {
					defer func() { <-k.orderSem }()
					k.processMessage(msg)
				}()
			}
		}(i)
	}
}

// processMessage decodes, validates, processes, and commits (or DLQs) one record.
func (k *KafkaOrderConfig) processMessage(msg *kafka.Message) {
	select {
	case <-k.Context.Done():
		return
	default:
	}

	// Decode payload
	var job dtos.PaymentJob
	if err := json.Unmarshal(msg.Value, &job); err != nil {
		k.Logger.Error("decode_message_failed", zap.Error(err))
		k.sendToDLQ(job, "json_unmarshal_error", err.Error())
		_, _ = k.consumer.CommitMessage(msg) // skip invalid message
		return
	}

	// Validate struct
	if err := k.validate.Struct(&job); err != nil {
		k.Logger.Error("validate_job_failed", zap.Error(err))
		k.sendToDLQ(job, "validation_error", err.Error())
		_, _ = k.consumer.CommitMessage(msg)
		return
	}

	// Business processing
	k.Logger.Info("processing_payment_job", zap.Any("paymentJob", job))
	if err := k.PaymentProcessor.ProcessPayment(k.Context, job); err != nil {
		k.Logger.Error("process_payment_failed_sending_to_dlq",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err))
		k.sendToDLQ(job, "processPaymentError", err.Error())
		if _, cErr := k.consumer.CommitMessage(msg); cErr != nil {
			k.Logger.Error("commit_after_dlq_failed", zap.Error(cErr))
		}
		return
	}

	// Commit on success
	if _, err := k.consumer.CommitMessage(msg); err != nil {
		k.Logger.Error("commit_offset_failed",
			zap.Any(pkg.IdempotencyKey, job.IdempotencyKey),
			zap.Error(err))
		return
	}
	k.Logger.Info("payment_processed_successfully",
		zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
}

// sendToDLQ sends a failed job to the Dead Letter Queue with a reason and error message.
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
