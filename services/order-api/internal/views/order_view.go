package views

import (
	"time"

	"github.com/google/uuid"
)

type OrderRequest struct {
	IdempotencyID   uuid.UUID `json:"idempotencyId" binding:"required,uuid"` // Client-provided UUID for idempotency
	AccountID       uuid.UUID `json:"accountId" binding:"required,uuid"`
	Amount          float64   `json:"amount" binding:"required,gt=0,lte=5000"`
	Currency        string    `json:"currency" binding:"required,oneof=USD CAD"`
	Timestamp       time.Time `json:"timestamp"`
	IPAddress       string    `json:"ipAddress"`
	TransactionType string    `json:"transactionType"`
}
