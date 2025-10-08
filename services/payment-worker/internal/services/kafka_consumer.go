package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

type KafkaConsumer interface {
	Consume(ctx context.Context) func()
}

type KafkaConsumerImpl struct {
	logger         *zap.Logger
	cnf            *configs.Config
	consumer       *kafka.Consumer
	paymentService PaymentService
	dlqProducer    *kafka.Producer // DLQ producer
}

func NewKafkaConsumer(logger *zap.Logger, cnf *configs.Config, paymentService PaymentService) KafkaConsumer {
	conf := &kafka.ConfigMap{
		"bootstrap.servers":  cnf.KafkaBrokers,       // Kafka broker(s)
		"group.id":           cnf.KafkaConsumerGroup, // Consumer group
		"auto.offset.reset":  "earliest",             // Start from the beginning of the topic
		"enable.auto.commit": false,                  // Disable auto commit
	}
	kafkaConsumer, err := kafka.NewConsumer(conf)
	if err != nil {
		logger.Fatal("failed to create kafka consumer", zap.Error(err))
	}

	// Create a DLQ producer (idempotent for safety, optional)
	prod, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cnf.KafkaBrokers,
		"enable.idempotence": true,
	})
	if err != nil {
		logger.Fatal("failed to create kafka producer for DLQ", zap.Error(err))
	}

	return &KafkaConsumerImpl{
		logger:         logger,
		cnf:            cnf,
		consumer:       kafkaConsumer,
		paymentService: paymentService,
		dlqProducer:    prod,
	}
}

func (k KafkaConsumerImpl) Consume(ctx context.Context) func() {
	err := k.consumer.SubscribeTopics([]string{k.cnf.KafkaTopic}, nil)
	if err != nil {
		k.logger.Fatal("failed to subscribe to topic", zap.Error(err))
	}

	k.logger.Info("listening to topic", zap.String("topic", k.cnf.KafkaTopic), zap.Any("consumer_group", k.cnf.KafkaConsumerGroup))
	go func() {
		for {
			msg, err := k.consumer.ReadMessage(-1)
			if err != nil {
				k.logger.Error("read", zap.Error(err))
				continue
			}
			// Process in goroutine for concurrency, but limit with semaphore for rate.
			go k.processMessage(ctx, msg)
		}
	}()

	closer := func() {
		// drain producer
		if k.dlqProducer != nil {
			k.dlqProducer.Flush(5000)
			k.dlqProducer.Close()
		}
		err := k.consumer.Close()
		if err != nil {
			k.logger.Error("failed to close kafka consumer", zap.Error(err))
			return
		}
		k.logger.Info("kafka consumer closed successfully")
	}
	return closer
}

func (k KafkaConsumerImpl) processMessage(ctx context.Context, msg *kafka.Message) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	// decode message
	var paymentJob views.PaymentJob
	if err := json.Unmarshal(msg.Value, &paymentJob); err != nil {
		k.logger.Error("failed to decode message", zap.Error(err))
		k.sendToDLQ(ctx, msg, "json_unmarshal_error", err.Error())
		// commit to skip this bad message
		_, _ = k.consumer.CommitMessage(msg)
		return
	}
	// process message
	k.logger.Info("processing message", zap.Any("paymentJob", paymentJob))
	procErr := k.paymentService.ProcessPayment(ctx, paymentJob)
	if procErr != nil {
		k.logger.Error("failed to process payment; sending to DLQ", zap.String("idempotency_key", paymentJob.IdempotencyKey), zap.Error(procErr))
		k.sendToDLQ(ctx, msg, "process_payment_error", procErr.Error())
		// commit to avoid reprocessing the poison message
		if _, err := k.consumer.CommitMessage(msg); err != nil {
			k.logger.Error("failed to commit offset after DLQ", zap.Error(err))
		}
		return
	}

	// success, commit offset
	if _, err := k.consumer.CommitMessage(msg); err != nil {
		k.logger.Error("failed to commit offset", zap.Error(err))
		return
	}
	k.logger.Info("payment processed successfully", zap.String("idempotency_key", paymentJob.IdempotencyKey))
}

func (k KafkaConsumerImpl) sendToDLQ(_ context.Context, original *kafka.Message, reason, errMsg string) {
	if k.dlqProducer == nil {
		k.logger.Error("DLQ producer not initialized; dropping message", zap.String("reason", reason))
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
		TopicPartition: kafka.TopicPartition{Topic: &k.cnf.KafkaDLQTopic, Partition: kafka.PartitionAny},
		Key:            original.Key,
		Value:          b,
		Headers:        append(original.Headers, kafka.Header{Key: "x-dlq-reason", Value: []byte(reason)}),
	}, nil)
	if err != nil {
		k.logger.Error("failed to produce to DLQ", zap.Error(err))
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
