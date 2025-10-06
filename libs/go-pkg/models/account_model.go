package pkgmodels

import "time"

// Account maps to table `accounts`
type Account struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id"`
	UserID    int64     `gorm:"not null;index:idx_accounts_user_id;column:user_id"`
	User      User      `gorm:"constraint:OnDelete:CASCADE"`
	Balance   float64   `gorm:"type:numeric(15,2);not null;default:0.00;column:balance"`
	Currency  string    `gorm:"size:3;not null;default:USD;column:currency"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
	// Associations
	Orders []Order `gorm:"foreignKey:AccountID;constraint:OnDelete:CASCADE"`
}

// TableName overrides the name.
func (Account) TableName() string { return "accounts" }
