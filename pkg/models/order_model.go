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
