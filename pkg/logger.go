package pkg

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.Logger

// InitLogger initializes the global Logger based on the current environment (e.g., development, QA, production).
func InitLogger() {
	ginMode := gin.Mode()
	var config zap.Config

	if gin.ReleaseMode == ginMode { // pre. prod, or default
		config = zap.NewProductionConfig()
		config.OutputPaths = []string{"stdout"}
		config.ErrorOutputPaths = []string{"stderr"}
		config.Sampling = &zap.SamplingConfig{
			Initial:    1000, // let the first 1,000 occurrences through unconditionally, then
			Thereafter: 100,  // only log every 100 th occurrence after that.
		}
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.Sampling = nil // disable sampling
	}
	logger, err := config.Build(zap.AddStacktrace(zap.DPanicLevel))
	if err != nil {
		panic(err)
	}

	Logger = logger
}
