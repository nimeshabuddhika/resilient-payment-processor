package handlers

import (
	"context"
	"encoding/json"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"go.uber.org/zap"
)

const (
	TopicOrdersPlaced = "orders-placed"
)

type KafkaConsumer interface {
	Consume(ctx context.Context) func()
}

type KafkaConsumerImpl struct {
	logger   *zap.Logger
	cnf      *configs.Config
	consumer *kafka.Consumer
}

func NewKafkaConsumer(logger *zap.Logger, cnf *configs.Config) KafkaConsumer {
	conf := &kafka.ConfigMap{
		"bootstrap.servers":  cnf.KafkaBrokers,  // Kafka broker(s)
		"group.id":           "payment-workers", // Consumer group
		"auto.offset.reset":  "earliest",        // Start from the beginning of the topic
		"enable.auto.commit": false,             // Disable auto commit
	}
	kafkaConsumer, err := kafka.NewConsumer(conf)
	if err != nil {
		logger.Fatal("failed to create kafka consumer", zap.Error(err))
	}
	return &KafkaConsumerImpl{
		logger:   logger,
		cnf:      cnf,
		consumer: kafkaConsumer,
	}
}

func (k KafkaConsumerImpl) Consume(ctx context.Context) func() {
	err := k.consumer.SubscribeTopics([]string{TopicOrdersPlaced}, nil)
	if err != nil {
		k.logger.Fatal("failed to subscribe to topic", zap.Error(err))
	}

	k.logger.Info("listening to topic", zap.String("topic", TopicOrdersPlaced))
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
		return
	}
	// process message
	k.logger.Info("processing message", zap.Any("paymentJob", paymentJob))
	// commit message
	if _, err := k.consumer.CommitMessage(msg); err != nil {
		k.logger.Error("commit failed", zap.Error(err))
	} else {
		k.logger.Debug("offset committed", zap.Any("topic", *msg.TopicPartition.Topic), zap.Int32("partition", msg.TopicPartition.Partition), zap.Int64("offset", int64(msg.TopicPartition.Offset)))
	}
}
