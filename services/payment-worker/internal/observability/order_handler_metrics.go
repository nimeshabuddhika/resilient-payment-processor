package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	MessagesReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pw_order_handler",
			Name:      "messages_received_total",
			Help:      "Kafka messages pulled by the worker",
		},
		[]string{"topic"},
	)

	PaymentsProcessed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pw_order_handler",
			Name:      "processed_total",
			Help:      "Successfully processed payment jobs",
		},
		[]string{"topic"},
	)

	PaymentsFailed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pw_order_handler",
			Name:      "failed_total",
			Help:      "Failed payment jobs by reason",
		},
		[]string{"topic", "reason"},
	)

	DLQPublished = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pw_order_handler",
			Name:      "dlq_total",
			Help:      "Jobs sent to DLQ by reason",
		},
		[]string{"topic", "reason"},
	)

	ProcessLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "pw_order_handler",
			Name:      "process_duration_seconds",
			Help:      "End-to-end processing latency per message",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"topic"},
	)

	InflightJobs = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "pw_order_handler",
			Name:      "inflight_jobs",
			Help:      "Number of jobs currently being processed (semaphore depth)",
		},
	)
)
