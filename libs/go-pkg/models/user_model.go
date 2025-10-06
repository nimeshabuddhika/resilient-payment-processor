package pkgmodels

import "time"

// User maps to table `users`
type User struct {
	ID        int64     `gorm:"primaryKey;autoIncrement;column:id"`
	Username  string    `gorm:"size:255;unique;not null;column:username"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
	// Associations
	Accounts []Account `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Orders   []Order   `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
}

// TableName overrides the table name used by GORM
func (User) TableName() string {
	return "users"
}
