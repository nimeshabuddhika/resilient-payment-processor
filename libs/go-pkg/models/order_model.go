package pkgmodels

import (
	"time"

	"github.com/google/uuid"
)

// Order maps to table `orders`
type Order struct {
	ID             int64     `gorm:"primaryKey;autoIncrement;column:id"`
	UserID         int64     `gorm:"not null;index:idx_orders_user_id;column:user_id"`
	User           User      `gorm:"constraint:OnDelete:CASCADE"`
	AccountID      int64     `gorm:"not null;column:account_id"`
	Account        Account   `gorm:"constraint:OnDelete:CASCADE"`
	IdempotencyKey uuid.UUID `gorm:"type:uuid;not null;index:idx_accounts_idempotency_key;column:idempotency_key"`
	Amount         float64   `gorm:"type:numeric(15,2);not null;column:amount"`
	Status         string    `gorm:"size:50;not null;default:pending;column:status"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

// TableName overrides the table name for Order
func (Order) TableName() string { return "orders" }
