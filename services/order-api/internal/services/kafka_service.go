package services

import (
	"encoding/json"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/configs"
	"go.uber.org/zap"
)

type KafkaPublisher interface {
	PublishOrder(userUUID uuid.UUID, paymentJob views.PaymentJob) error
	Close()
}

type KafkaPublisherImpl struct {
	logger   *zap.Logger
	producer *kafka.Producer
	cnf      *configs.Config
}

// NewKafkaPublisher creates and initializes a KafkaPublisher with the provided logger and configuration parameters.
func NewKafkaPublisher(logger *zap.Logger, cnf *configs.Config) KafkaPublisher {
	p, err := kafka.NewProducer(&kafka.ConfigMap{
		"bootstrap.servers":  cnf.KafkaBrokers, // Kafka broker(s)
		"acks":               "all",            // Wait for all replicas
		"enable.idempotence": "true",           // Ensure messages are not sent twice
		"retries":            cnf.KafkaRetry,   // Built-in retry mechanism
	})
	if err != nil {
		logger.Fatal("failed to create kafka producer", zap.Error(err))
	}
	logger.Info("kafka producer created successfully", zap.String("brokers", cnf.KafkaBrokers))
	go handleDeliveryReports(logger, p) // Async error handling
	return &KafkaPublisherImpl{
		logger:   logger,
		cnf:      cnf,
		producer: p,
	}
}

func (k KafkaPublisherImpl) PublishOrder(userUUID uuid.UUID, paymentJob views.PaymentJob) error {
	// Serialize the order payload to JSON for Kafka transport
	msgBytes, err := json.Marshal(paymentJob)
	if err != nil {
		return err
	}

	// Deterministic partitioning by user ID to balanced load
	partition := int32(userUUID.ID() % k.cnf.KafkaPartition)

	// Produce the message asynchronously; delivery results are handled by handleDeliveryReports
	return k.producer.Produce(&kafka.Message{
		TopicPartition: kafka.TopicPartition{
			Topic:     &k.cnf.KafkaTopic,
			Partition: partition, // target partition for ordering/affinity
		},
		Key:   []byte(paymentJob.IdempotencyKey), // key for idempotency and partitioning semantics
		Value: msgBytes,                          // serialized order payload
	}, nil)
}

func (k KafkaPublisherImpl) Close() {
	k.producer.Close()
}

func handleDeliveryReports(logger *zap.Logger, p *kafka.Producer) {
	for e := range p.Events() {
		switch ev := e.(type) {
		case *kafka.Message:
			if ev.TopicPartition.Error != nil {
				logger.Error("failed to publish message", zap.Error(ev.TopicPartition.Error))
			}
		}
	}
}
