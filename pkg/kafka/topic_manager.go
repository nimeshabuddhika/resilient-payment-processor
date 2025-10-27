package kafkautils

import (
	"context"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"go.uber.org/zap"
)

type KafkaConfig struct {
	BootstrapServers string
	Topics           []TopicConfig
}

type TopicConfig struct {
	Topic             string
	NumPartitions     int
	ReplicationFactor int
	Config            map[string]string
}

// InitKafkaTopics creates the specified Kafka topics.
// It retries up to 2 minutes in case of failure.
// Returns an error if any topic creation fails.
func InitKafkaTopics(logger *zap.Logger, ctx context.Context, cnf KafkaConfig) error {
	config := &kafka.ConfigMap{"bootstrap.servers": cnf.BootstrapServers}
	admin, err := kafka.NewAdminClient(config)
	if err != nil {
		return fmt.Errorf("failed to create admin client: %w", err)
	}
	defer admin.Close()

	// Assign topics
	var topics []kafka.TopicSpecification
	for _, topic := range cnf.Topics {
		t := kafka.TopicSpecification{
			Topic:             topic.Topic,
			NumPartitions:     topic.NumPartitions,
			ReplicationFactor: topic.ReplicationFactor,
			Config:            topic.Config,
		}
		topics = append(topics, t)
	}

	// Create topics
	operation := func() error {
		results, err := admin.CreateTopics(ctx, topics, kafka.SetAdminOperationTimeout(30*time.Second))
		if err != nil {
			return fmt.Errorf("failed to create topics: %w", err)
		}
		for _, result := range results {
			if result.Error.Code() != kafka.ErrNoError && result.Error.Code() != kafka.ErrTopicAlreadyExists {
				return fmt.Errorf("kafka topic %s creation failed: %v", result.Topic, result.Error)
			}
			logger.Info("Kafka topic created", zap.String("topic", result.Topic))
		}
		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = 2 * time.Minute // Retry for up to 2 minutes
	return backoff.Retry(operation, b)
}
