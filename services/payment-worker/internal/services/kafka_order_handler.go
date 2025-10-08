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

// KafkaOrderHandler is an interface for consuming messages from Kafka.
type KafkaOrderHandler interface {
	Consume(ctx context.Context) func()
}

// KafkaOrderConfig holds configuration and dependencies for the Kafka order consumer.
type KafkaOrderConfig struct {
	Logger         *zap.Logger
	Config         *configs.Config
	consumer       *kafka.Consumer
	PaymentService PaymentService
	dlqProducer    *kafka.Producer
	validate       *validator.Validate
}

// NewKafkaOrderConsumer creates and initializes a KafkaOrderHandler with the provided logger and configuration parameters.
func NewKafkaOrderConsumer(cfg KafkaOrderConfig) KafkaOrderHandler {
	kafkaConfig := &kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,       // Kafka broker(s)
		"group.id":           cfg.Config.KafkaConsumerGroup, // Consumer group
		"auto.offset.reset":  "earliest",                    // Start from the beginning of the topic
		"enable.auto.commit": false,                         // Disable auto commit
	}
	kafkaConsumer, err := kafka.NewConsumer(kafkaConfig)
	if err != nil {
		cfg.Logger.Fatal("failed to create kafka consumer", zap.Error(err))
	}

	// Create a DLQ producer (idempotent for safety, optional)
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cfg.Config.KafkaBrokers,
		"enable.idempotence": true,
	})
	if err != nil {
		cfg.Logger.Fatal("failed to create DLQ producer", zap.Error(err))
	}

	cfg.consumer = kafkaConsumer
	cfg.dlqProducer = dlqProducer
	cfg.validate = validator.New()
	return &cfg
}

// Consume starts the message consumption loop and returns a closure to gracefully stop resources.
func (k *KafkaOrderConfig) Consume(ctx context.Context) func() {
	err := k.consumer.SubscribeTopics([]string{k.Config.KafkaTopic}, nil)
	if err != nil {
		k.Logger.Fatal("failed to subscribe", zap.Error(err))
	}

	k.Logger.Info("listening to topic", zap.String("topic", k.Config.KafkaTopic), zap.Any("group", k.Config.KafkaConsumerGroup))
	go func() {
		for {
			msg, err := k.consumer.ReadMessage(-1)
			if err != nil {
				k.Logger.Error("read error", zap.Error(err))
				continue
			}
			// Process in goroutine for concurrency
			go k.processMessage(ctx, msg)
		}
	}()

	return func() {
		// drain producer
		if k.dlqProducer != nil {
			k.dlqProducer.Flush(5000)
			k.dlqProducer.Close()
		}
		if err := k.consumer.Close(); err != nil {
			k.Logger.Error("close error", zap.Error(err))
			return
		}
		k.Logger.Info("consumer closed")
	}
}

func (k *KafkaOrderConfig) processMessage(ctx context.Context, msg *kafka.Message) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	// decode message
	var paymentJob views.PaymentJob
	if err := json.Unmarshal(msg.Value, &paymentJob); err != nil {
		k.Logger.Error("failed to decode message", zap.Error(err))
		k.sendToDLQ(ctx, msg, "json_unmarshal_error", err.Error())
		// commit to skip this bad message
		_, _ = k.consumer.CommitMessage(msg)
		return
	}

	// Validate after unmarshal
	if err := k.validate.Struct(&paymentJob); err != nil {
		k.Logger.Error("failed to validate message", zap.Error(err))
		k.sendToDLQ(ctx, msg, "validate error", err.Error())
		// commit to skip this bad message
		_, _ = k.consumer.CommitMessage(msg)
		return
	}

	// process message
	k.Logger.Info("processing message", zap.Any("paymentJob", paymentJob))
	procErr := k.PaymentService.HandlePayment(ctx, paymentJob)
	if procErr != nil {
		k.Logger.Error("failed to process payment; sending to DLQ", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(procErr))
		k.sendToDLQ(ctx, msg, "process_payment_error", procErr.Error())
		// commit to avoid reprocessing the poison message
		if _, err := k.consumer.CommitMessage(msg); err != nil {
			k.Logger.Error("failed to commit offset after DLQ", zap.Error(err))
		}
		return
	}

	// success, commit offset
	if _, err := k.consumer.CommitMessage(msg); err != nil {
		k.Logger.Error("failed to commit offset", zap.Error(err))
		return
	}
	k.Logger.Info("payment processed successfully", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey))
}

// sendToDLQ publishes the original message with error context to the configured DLQ topic.
func (k *KafkaOrderConfig) sendToDLQ(_ context.Context, original *kafka.Message, reason, errMsg string) {
	if k.dlqProducer == nil {
		k.Logger.Error("DLQ producer not initialized; dropping message", zap.String("reason", reason))
		return
	}
	// Wrap with metadata
	payload := map[string]any{
		"original_topic":     getTopic(original),
		"original_partition": original.TopicPartition.Partition,
		"original_offset":    original.TopicPartition.Offset,
		"key":                stringOrNil(original.Key),
		"value":              string(original.Value),
		"headers":            headersToMap(original.Headers),
		"failure_reason":     reason,
		"error":              errMsg,
		"failed_at":          time.Now().UTC().Format(time.RFC3339Nano),
	}
	b, _ := json.Marshal(payload)

	err := k.dlqProducer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &k.Config.KafkaDLQTopic, Partition: kafka.PartitionAny},
		Key:            original.Key,
		Value:          b,
		Headers:        append(original.Headers, kafka.Header{Key: "x-dlq-reason", Value: []byte(reason)}),
	}, nil)
	if err != nil {
		k.Logger.Error("failed to produce to DLQ", zap.Error(err))
		return
	}
}

func getTopic(msg *kafka.Message) string {
	if msg.TopicPartition.Topic == nil {
		return ""
	}
	return *msg.TopicPartition.Topic
}

func stringOrNil(b []byte) any {
	if b == nil {
		return nil
	}
	return string(b)
}

func headersToMap(hdrs []kafka.Header) map[string]string {
	m := make(map[string]string, len(hdrs))
	for _, h := range hdrs {
		m[h.Key] = string(h.Value)
	}
	return m
}
