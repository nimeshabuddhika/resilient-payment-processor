package main

import (
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"go.uber.org/zap"
)

func main() {
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	for i := 1; i <= 10; i++ {
		if i == 3 {
			logger.Info("test", zap.Int("i", i))
			break
		}
		logger.Info("bottom of loop", zap.Int("i", i))
	}

	logger.Info("end")
}
