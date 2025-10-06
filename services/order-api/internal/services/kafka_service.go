package services

import "go.uber.org/zap"

type KafkaPublisher interface {
}

type KafkaPublisherImpl struct {
	logger    *zap.Logger
	kafkaAddr string
}

func NewKafkaPublisher(logger *zap.Logger, kafkaAddr string) KafkaPublisher {
	return &KafkaPublisherImpl{
		logger:    logger,
		kafkaAddr: kafkaAddr,
	}
}
