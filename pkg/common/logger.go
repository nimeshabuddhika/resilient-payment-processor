package common

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.Logger

// InitLogger initializes the global Logger based on the current environment (e.g., development, QA, production).
func InitLogger() {
	env := os.Getenv("APP_ENV")
	var config zap.Config

	if env == "dev" || env == "qa" {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else { // pre. prod, or default
		config = zap.NewProductionConfig()
		config.OutputPaths = []string{"stdout"}
		config.ErrorOutputPaths = []string{"stderr"}
	}

	logger, err := config.Build()
	if err != nil {
		panic(err)
	}
	Logger = logger
}
