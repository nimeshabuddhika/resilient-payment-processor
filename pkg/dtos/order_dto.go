package dtos

import (
	"time"
)

type OrderAIDto struct {
	ID                  string    `json:"id"`
	UserID              string    `json:"userId"`
	AccountID           string    `json:"accountId"`
	IdempotencyKey      string    `json:"idempotencyKey"`
	Amount              float64   `json:"amount"`
	Currency            string    `json:"currency"`
	IsFraud             bool      `json:"isFraud"`             // Fraud status
	TransactionVelocity int       `json:"transactionVelocity"` // Orders per hour for user (simulated)
	AmountDeviation     float64   `json:"amountDeviation"`     // Deviation from user's avg order amount
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type OrderResult struct {
	Orders    []OrderAIDto `json:"orders"`
	Count     int          `json:"count"`
	CreatedAt time.Time    `json:"createdAt"`
}
