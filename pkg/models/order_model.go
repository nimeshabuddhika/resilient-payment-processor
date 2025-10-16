package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/dtos"
)

// Order maps to table `orders`
type Order struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      uuid.UUID
	IdempotencyKey uuid.UUID
	Amount         string
	Currency       string
	Status         pkg.OrderStatus
	Message        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (o Order) ToPaymentJob() dtos.PaymentJob {
	return dtos.PaymentJob{
		ID:             o.ID,
		UserID:         o.UserID,
		AccountID:      o.AccountID,
		IdempotencyKey: o.IdempotencyKey,
		Amount:         o.Amount,
		CreatedAt:      o.CreatedAt,
		UpdatedAt:      o.UpdatedAt,
	}
}

// OrderAIModel maps to table orders_ai_dataset
type OrderAIModel struct {
	ID                  uuid.UUID
	UserID              uuid.UUID
	AccountID           uuid.UUID
	IdempotencyKey      uuid.UUID
	Amount              float64
	Currency            string
	IsFraud             bool    // Fraud status
	TransactionVelocity int     // Orders per hour for user (simulated)
	AmountDeviation     float64 // Deviation from user's avg order amount
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (o OrderAIModel) ToOrderAIDto() dtos.OrderAIDto {
	return dtos.OrderAIDto{
		ID:                  o.ID.String(),
		UserID:              o.UserID.String(),
		AccountID:           o.AccountID.String(),
		IdempotencyKey:      o.IdempotencyKey.String(),
		Amount:              o.Amount,
		Currency:            o.Currency,
		IsFraud:             o.IsFraud,
		TransactionVelocity: o.TransactionVelocity,
		AmountDeviation:     o.AmountDeviation,
		CreatedAt:           o.CreatedAt,
		UpdatedAt:           o.UpdatedAt,
	}
}
