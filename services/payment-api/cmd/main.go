package main

import "github.com/nimeshabuddhika/resilient-job-go/pkg/common"

func main() {
	common.InitLogger()
	common.Logger.Info("Payment API started")
}
