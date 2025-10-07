package configs

import (
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config holds application configuration loaded from environment variables and optional config file.
type Config struct {
	PrimaryDbAddr string `mapstructure:"PRIMARY_DB_ADDR" validate:"required"`
	ReplicaDbAddr string `mapstructure:"REPLICA_DB_ADDR"`
	MaxDbCons     int32  `mapstructure:"MAX_DB_CONNECTIONS" validate:"min=1"`
	MinDbCons     int32  `mapstructure:"MIN_DB_CONNECTIONS" validate:"min=1"`
	AesKey        string `mapstructure:"AES_KEY" validate:"required"`
}

// Load reads configuration from environment (and optional config file), then validates it.
func Load(logger *zap.Logger) (*Config, error) {
	viper.SetEnvPrefix("app") // Prefix for env vars
	viper.AutomaticEnv()

	// Optional: Read from config.yaml if exists
	if gin.ReleaseMode == gin.Mode() {
		viper.SetConfigName("config.prod")
	} else if gin.TestMode == gin.Mode() {
		logger.Warn("running in test mode")
		viper.SetConfigName("config.test")
	} else {
		logger.Warn("running in development mode")
		viper.SetConfigName("config.dev")
	}
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./services/user-api/configs")
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
