package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
)

// Order maps to table `orders`
type Order struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      uuid.UUID
	IdempotencyKey uuid.UUID
	Amount         float64
	Status         pkg.OrderStatus
	Message        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (o Order) ToPaymentJob() views.PaymentJob {
	return views.PaymentJob{
		ID:             o.ID.String(),
		UserID:         o.UserID.String(),
		AccountID:      o.AccountID.String(),
		IdempotencyKey: o.IdempotencyKey.String(),
		Amount:         o.Amount,
		Status:         o.Status,
		CreatedAt:      o.CreatedAt,
		UpdatedAt:      o.UpdatedAt,
	}
}
