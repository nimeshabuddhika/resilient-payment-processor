package views

import (
	"time"

	"github.com/google/uuid"
)

type PaymentJob struct {
	ID             uuid.UUID `json:"id" validate:"required,uuid"`
	UserID         uuid.UUID `json:"userId" validate:"required,uuid"`
	AccountID      uuid.UUID `json:"accountId" validate:"required,uuid"`
	IdempotencyKey uuid.UUID `json:"idempotencyKey" validate:"required,uuid"`
	Amount         string    `json:"amount" validate:"required"`
	RetryCount     int       `json:"retryCount" validate:"min=0"`
	CreatedAt      time.Time `json:"createdAt" validate:"required"`
	UpdatedAt      time.Time `json:"updatedAt" validate:"required"`
}
