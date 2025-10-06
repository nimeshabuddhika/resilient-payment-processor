package models

import "time"

type OrderDB struct {
	ID        int64     `db:"id"`
	UserID    string    `db:"userId"`
	AccountID string    `db:"accountId"`
	Amount    float64   `db:"amount"`
	Currency  string    `db:"currency"`
	CreatedAt time.Time `db:"timestamp"`
	UpdatedAt int64     `db:"updated_at"`
}
