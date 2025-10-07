package pkgmodels

import (
	"time"

	"github.com/google/uuid"
)

// Order maps to table `orders`
type Order struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	AccountID      uuid.UUID
	IdempotencyKey uuid.UUID
	Amount         float64
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
