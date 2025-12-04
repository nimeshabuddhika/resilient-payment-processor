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

// Ack records that a message has been fully processed and commits the
// consumer group offset when this creates a new contiguous range.
func (m *CommitManager) Ack(idempotencyKey uuid.UUID, msg *kafka.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	topic := *msg.TopicPartition.Topic
	partition := msg.TopicPartition.Partition
	off := int64(msg.TopicPartition.Offset)
	key := tp{topic: topic, partition: partition}

	// Log each ack for traceability in observability tools.
	m.log.Info("offsetting_message",
		zap.Any("idempotency_key", idempotencyKey),
		zap.String("topic", topic),
		zap.Int32("partition", partition),
		zap.Int64("offset", off),
	)

	// Initialize the watermark for a new partition:
	//
	// The first message we see is at the group’s current committed offset.
	// All offsets < off are already acknowledged at the broker, so our
	// logical "last committed" local watermark starts at off-1.
	if _, ok := m.high[key]; !ok {
		m.high[key] = off - 1
		if m.high[key] < -1 {
			m.high[key] = -1
		}
		m.log.Debug("commit_manager_partition_initialized",
			zap.String("topic", topic),
			zap.Int32("partition", partition),
			zap.Int64("initial_high", m.high[key]),
		)
	}

	// Mark this offset as processed but not yet part of a contiguous range.
	if m.done[key] == nil {
		m.done[key] = make(map[int64]struct{})
	}
	m.done[key][off] = struct{}{}

	// Try to advance the high watermark as long as we have
	next := m.high[key]
	for {
		if _, ok := m.done[key][next+1]; ok {
			next++
			delete(m.done[key], next)
		} else {
			break
		}
	}

	// If the contiguous range advanced, commit the new position.
	if next > m.high[key] {
		tpToCommit := kafka.TopicPartition{
			Topic:     &key.topic,
			Partition: key.partition,
			Offset:    kafka.Offset(next + 1), // commit next offset to consume
		}

		if _, err := m.consumer.CommitOffsets([]kafka.TopicPartition{tpToCommit}); err != nil {
			m.log.Error("offset_commit_failed",
				zap.Any(pkg.IdempotencyKey, idempotencyKey),
				zap.String("topic", key.topic),
				zap.Int32("partition", key.partition),
				zap.Int64("attempted_offset", next),
				zap.Error(err),
			)
			return
		}

		m.high[key] = next
		if len(m.done[key]) == 0 {
			// Free per‑partition state once everything is contiguous.
			delete(m.done, key)
		}

		m.log.Info("offset_committed",
			zap.Any(pkg.IdempotencyKey, idempotencyKey),
			zap.String("topic", key.topic),
			zap.Int32("partition", key.partition),
			zap.Int64("offset", next),
		)
	}
}
