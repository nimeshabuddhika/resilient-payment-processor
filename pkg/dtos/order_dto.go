package dtos

import (
	"time"
)

type OrderAIDto struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"userId"`
	AccountID           string    `json:"accountId"`
	IdempotencyKey      string    `json:"idempotencyKey"`
	Amount              string    `json:"amount"`
	Currency            string    `json:"currency"`
	Message             string    `json:"message"`
	IsFraud             bool      `json:"isFraud"`             // Fraud status
	TransactionVelocity int       `json:"transactionVelocity"` // Orders per hour for user (simulated)
	IpAddress           string    `json:"ipAddress"`           // Simulated IPv4/IPv6 for geo-anomalies
	AmountDeviation     float64   `json:"amountDeviation"`     // Deviation from user's avg order amount
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}
