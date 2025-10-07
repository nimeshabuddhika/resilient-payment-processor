package models

import (
	"time"

	"github.com/google/uuid"
)

type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusProcessed OrderStatus = "processed"
	OrderStatusFailed    OrderStatus = "failed"
)

// Order maps to table `orders`
type Order struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      uuid.UUID
	IdempotencyKey uuid.UUID
	Amount         float64
	Status         OrderStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
