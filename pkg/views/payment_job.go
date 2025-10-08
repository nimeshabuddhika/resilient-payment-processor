package views

import (
	"time"

	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
)

type PaymentJob struct {
	ID             string          `json:"id"`
	UserID         string          `json:"userId"`
	AccountID      string          `json:"accountId"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Amount         float64         `json:"amount"`
	Status         pkg.OrderStatus `json:"status"`
	RetryCount     int             `json:"retryCount"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}
