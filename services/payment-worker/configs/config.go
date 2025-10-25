package configs

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config holds application configuration for payment-worker.
type Config struct {
	MetricsAddr                   string        `mapstructure:"METRICS_ADDR" validate:"required"`
	KafkaBrokers                  string        `mapstructure:"KAFKA_BROKERS" validate:"required"`
	PrimaryDbAddr                 string        `mapstructure:"PRIMARY_DB_ADDR" validate:"required"`
	ReadDbAddr                    string        `mapstructure:"READ_DB_ADDR"`
	MaxDbCons                     int32         `mapstructure:"MAX_DB_CONNECTIONS" validate:"min=1"`
	MinDbCons                     int32         `mapstructure:"MIN_DB_CONNECTIONS" validate:"min=1"`
	KafkaPartition                uint32        `mapstructure:"KAFKA_PARTITION" validate:"min=1"`
	KafkaOrderTopic               string        `mapstructure:"KAFKA_ORDER_TOPIC" validate:"required"`
	KafkaDLQTopic                 string        `mapstructure:"KAFKA_DLQ_TOPIC" validate:"required"`
	KafkaDLQRetention             time.Duration `mapstructure:"KAFKA_DLQ_RETENTION" validate:"required"`
	KafkaOrderConsumerGroup       string        `mapstructure:"KAFKA_ORDER_CONSUMER_GROUP" validate:"required"`
	KafkaRetryConsumerGroup       string        `mapstructure:"KAFKA_RETRY_CONSUMER_GROUP" validate:"required"`
	KafkaRetryTopic               string        `mapstructure:"KAFKA_RETRY_TOPIC" validate:"required"`
	KafkaRetryDLQTopic            string        `mapstructure:"KAFKA_RETRY_DLQ_TOPIC" validate:"required"`
	KafkaRetryRetention           time.Duration `mapstructure:"KAFKA_RETRY_RETENTION" validate:"required"`
	KafkaRetryDLQRetention        time.Duration `mapstructure:"KAFKA_RETRY_DLQ_RETENTION" validate:"required"`
	RetryBaseBackoff              time.Duration `mapstructure:"RETRY_BASE_BACKOFF" validate:"required"`
	MaxRetryBackoff               time.Duration `mapstructure:"MAX_RETRY_BACKOFF" validate:"required"`
	MaxRetryCount                 int           `mapstructure:"MAX_RETRY_COUNT" validate:"min=1,max=5"`
	AesKey                        string        `mapstructure:"AES_KEY" validate:"required"`
	RedisAddr                     string        `mapstructure:"REDIS_ADDR" validate:"required"`
	MaxReplicaRateLimit           int           `mapstructure:"MAX_REPLICA_RATE_LIMIT" validate:"min=1"`
	MaxOrdersPlacedConcurrentJobs int           `mapstructure:"MAX_ORDERS_PLACED_CONCURRENT_JOBS" validate:"min=1"`
	MaxOrdersRetryConcurrentJobs  int           `mapstructure:"MAX_ORDERS_RETRY_CONCURRENT_JOBS" validate:"min=1"`
	FraudMLServiceAddr            string        `mapstructure:"FRAUD_ML_SERVICE_ADDR"`
	MlRateLimitPerSec             int           `mapstructure:"ML_RATE_LIMIT_PER_SEC" validate:"min=1"`
	MlRequestBurst                int           `mapstructure:"ML_REQUEST_BURST" validate:"min=1"`
	MlRequestMaxThrottleWait      time.Duration `mapstructure:"ML_REQUEST_MAX_THROTTLE_WAIT" validate:"required"` // Throttle wait guard: if the wait time is longer than this to get a token, fail fast
}

func Load(logger *zap.Logger) (*Config, error) {
	viper.SetEnvPrefix("app") // Prefix for env vars
	viper.AutomaticEnv()

	// Default values
	viper.SetDefault("KAFKA_RETRY", "3")
	viper.SetDefault("KAFKA_PARTITION", "4")
	viper.SetDefault("MAX_REPLICA_RATE_LIMIT", "10")
	viper.SetDefault("ORDER_RETRY_THRESHOLD", "1")

	// Optional: Read from config.yaml if exists
	if gin.ReleaseMode == gin.Mode() {
		viper.SetConfigName("config.prod")
	} else if gin.TestMode == gin.Mode() {
		logger.Warn("running_in_test_mode")
		viper.SetConfigName("config.test")
	} else {
		logger.Warn("running_in_development_mode")
		viper.SetConfigName("config.dev")
	}
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./services/payment-worker/configs")
	_ = viper.ReadInConfig() // Ignore if no file

	var cfg Config
	if err := utils.ParseStructEnv(&cfg); err != nil {
		return nil, err
	}

	// Validate after unmarshal
	validate := validator.New()
	if err := validate.Struct(&cfg); err != nil {
		return nil, utils.FormatConfigErrors(logger, err, cfg)
	}
	return &cfg, nil
}
