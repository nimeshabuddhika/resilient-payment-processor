package configs

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/utils"
	"github.com/spf13/viper"
)

type Config struct {
	Port          string `mapstructure:"PORT" validate:"required"`
	KafkaBrokers  string `mapstructure:"KAFKA_BROKERS" validate:"required"`
	PrimaryDbAddr string `mapstructure:"PRIMARY_DB_ADDR" validate:"required"`
	ReplicaDbAddr string `mapstructure:"REPLICA_DB_ADDR"`
	MaxDbCons     int32  `mapstructure:"MAX_DB_CONNECTIONS" validate:"min=1"`
	MinDbCons     int32  `mapstructure:"MIN_DB_CONNECTIONS" validate:"min=1"`
}

func Load() (*Config, error) {
	viper.SetEnvPrefix("app") // Prefix for env vars
	viper.AutomaticEnv()

	// Default values
	viper.SetDefault("PORT", "8080")
	viper.SetDefault("MAX_DB_CONNECTIONS", "10")
	viper.SetDefault("MIN_DB_CONNECTIONS", "2")

	// Optional: Read from config.yaml if exists
	if gin.ReleaseMode == gin.Mode() {
		viper.SetConfigName("config.prod")
	} else {
		viper.SetConfigName("config.dev")
	}
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./services/order-api/configs")
	_ = viper.ReadInConfig() // Ignore if no file

	var cfg Config
	if err := utils.ParseStructEnv(&cfg); err != nil {
		return nil, err
	}
	// Validate after unmarshal
	validate := validator.New()
	if err := validate.Struct(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}
