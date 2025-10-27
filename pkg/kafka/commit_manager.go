package kafkautils

import (
	"sync"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"go.uber.org/zap"
)

type tp struct {
	topic     string
	partition int32
}

type CommitManager struct {
	mu       sync.Mutex
	high     map[tp]int64              // last committed offset per partition
	done     map[tp]map[int64]struct{} // processed offsets not yet committed
	consumer *kafka.Consumer
	log      *zap.Logger
}

func NewCommitManager(c *kafka.Consumer, l *zap.Logger) *CommitManager {
	return &CommitManager{
		high:     make(map[tp]int64),
		done:     make(map[tp]map[int64]struct{}),
		consumer: c,
		log:      l,
	}
}

func (m *CommitManager) Ack(idempotencyKey uuid.UUID, msg *kafka.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log.Info("offsetting_message",
		zap.Any("idempotency_key", idempotencyKey),
		zap.String("topic", *msg.TopicPartition.Topic),
		zap.Int32("partition", msg.TopicPartition.Partition),
		zap.Int64("offset", int64(msg.TopicPartition.Offset)))

	key := tp{topic: *msg.TopicPartition.Topic, partition: msg.TopicPartition.Partition}
	off := int64(msg.TopicPartition.Offset)

	if m.done[key] == nil {
		m.done[key] = map[int64]struct{}{}
	}
	m.done[key][off] = struct{}{}

	next := m.high[key]
	for {
		if _, ok := m.done[key][next+1]; ok {
			next++
			delete(m.done[key], next)
		} else {
			break
		}
	}

	if next > m.high[key] {
		tpToCommit := kafka.TopicPartition{Topic: &key.topic, Partition: key.partition, Offset: kafka.Offset(next + 1)}
		if _, err := m.consumer.CommitOffsets([]kafka.TopicPartition{tpToCommit}); err != nil {
			m.log.Error("offset_commit_failed",
				zap.Any(pkg.IdempotencyKey, idempotencyKey),
				zap.String("topic", key.topic),
				zap.Int32("partition", key.partition),
				zap.Int64("attempted_offset", next), zap.Error(err))
			return
		}
		m.high[key] = next
		m.log.Info("offset_committed",
			zap.Any(pkg.IdempotencyKey, idempotencyKey),
			zap.String("topic", key.topic),
			zap.Int32("partition", key.partition),
			zap.Int64("offset", next))
	}
}
