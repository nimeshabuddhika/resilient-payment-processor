package services

import "go.uber.org/zap"

type KafkaPublisher interface {
}

type KafkaPublisherImpl struct {
	logger *zap.Logger
}

func NewKafkaPublisher(logger *zap.Logger) KafkaPublisher {
	return &KafkaPublisherImpl{
		logger: logger,
	}
}
