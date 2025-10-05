package models

type OrderDB struct {
	UserID    string  `db:"userId"`
	AccountID string  `db:"accountId"`
	Amount    float64 `db:"amount"`
}
