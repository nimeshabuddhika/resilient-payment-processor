package pkgmodels

import (
	"time"

	"github.com/google/uuid"
)

// Account maps to table `accounts`
type Account struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Balance   string // Encrypted string.
	Currency  string
	CreatedAt time.Time
	UpdatedAt time.Time
}
