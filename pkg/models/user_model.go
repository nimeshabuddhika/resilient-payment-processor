package models

import (
	"time"

	"github.com/google/uuid"
)

// User maps to table `users`
type User struct {
	ID        uuid.UUID
	Username  string
	CreatedAt time.Time
	UpdatedAt time.Time
	// Associations
	Accounts []Account
}
