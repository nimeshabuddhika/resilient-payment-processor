package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/gopkg/util/logger"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

type KafkaOrderHandler interface {
	Consume(ctx context.Context) func()
}

type KafkaOrderConf struct {
	Logger         *zap.Logger
	Cnf            *configs.Config
	Consumer       *kafka.Consumer
	PaymentService PaymentService
	DLQProducer    *kafka.Producer // DLQ producer
}

// NewKafkaConsumer creates and initializes a KafkaOrderHandler with the provided logger and configuration parameters.
func NewKafkaConsumer(kfConf KafkaOrderConf) KafkaOrderHandler {
	conf := &kafka.ConfigMap{
		"bootstrap.servers":  kfConf.Cnf.KafkaBrokers,       // Kafka broker(s)
		"group.id":           kfConf.Cnf.KafkaConsumerGroup, // Consumer group
		"auto.offset.reset":  "earliest",                    // Start from the beginning of the topic
		"enable.auto.commit": false,                         // Disable auto commit
	}
	kafkaConsumer, err := kafka.NewConsumer(conf)
	if err != nil {
		logger.Fatal("failed to create kafka consumer", zap.Error(err))
	}

	// Create a DLQ producer (idempotent for safety, optional)
	dlqProducer, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  kfConf.Cnf.KafkaBrokers,
		"enable.idempotence": true,
	})
	if err != nil {
		logger.Fatal("failed to create kafka producer for DLQ", zap.Error(err))
	}

	kfConf.Consumer = kafkaConsumer
	kfConf.DLQProducer = dlqProducer
	return &kfConf
}

func (k KafkaOrderConf) Consume(ctx context.Context) func() {
	err := k.Consumer.SubscribeTopics([]string{k.Cnf.KafkaTopic}, nil)
	if err != nil {
		k.Logger.Fatal("failed to subscribe to topic", zap.Error(err))
	}

	k.Logger.Info("listening to topic", zap.String("topic", k.Cnf.KafkaTopic), zap.Any("consumer_group", k.Cnf.KafkaConsumerGroup))
	go func() {
		for {
			msg, err := k.Consumer.ReadMessage(-1)
			if err != nil {
				k.Logger.Error("read", zap.Error(err))
				continue
			}
			// Process in goroutine for concurrency, but limit with semaphore for rate.
			go k.processMessage(ctx, msg)
		}
	}()

	closer := func() {
		// drain producer
		if k.DLQProducer != nil {
			k.DLQProducer.Flush(5000)
			k.DLQProducer.Close()
		}
		err := k.Consumer.Close()
		if err != nil {
			k.Logger.Error("failed to close kafka consumer", zap.Error(err))
			return
		}
		k.Logger.Info("kafka consumer closed successfully")
	}
	return closer
}

func (k KafkaOrderConf) processMessage(ctx context.Context, msg *kafka.Message) {
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
		_, _ = k.Consumer.CommitMessage(msg)
		return
	}

	// Validate after unmarshal
	validate := validator.New()
	if err := validate.Struct(&paymentJob); err != nil {
		k.Logger.Error("failed to validate message", zap.Error(err))
		k.sendToDLQ(ctx, msg, "validate error", err.Error())
		// commit to skip this bad message
		_, _ = k.Consumer.CommitMessage(msg)
		return
	}

	// process message
	k.Logger.Info("processing message", zap.Any("paymentJob", paymentJob))
	procErr := k.PaymentService.PaymentHandler(ctx, paymentJob)
	if procErr != nil {
		k.Logger.Error("failed to process payment; sending to DLQ", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(procErr))
		k.sendToDLQ(ctx, msg, "process_payment_error", procErr.Error())
		// commit to avoid reprocessing the poison message
		if _, err := k.Consumer.CommitMessage(msg); err != nil {
			k.Logger.Error("failed to commit offset after DLQ", zap.Error(err))
		}
		return
	}

	// success, commit offset
	if _, err := k.Consumer.CommitMessage(msg); err != nil {
		k.Logger.Error("failed to commit offset", zap.Error(err))
		return
	}
	k.Logger.Info("payment processed successfully", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey))
}

func (k KafkaOrderConf) sendToDLQ(_ context.Context, original *kafka.Message, reason, errMsg string) {
	if k.DLQProducer == nil {
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

	err := k.DLQProducer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{Topic: &k.Cnf.KafkaDLQTopic, Partition: kafka.PartitionAny},
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
