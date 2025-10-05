package views

import "time"

type OrderRequest struct {
	UserID          string    `json:"userId" binding:"required"`
	AccountID       string    `json:"accountId" binding:"required"`
	Amount          float64   `json:"amount" binding:"required,gt=0"`
	Timestamp       time.Time `json:"timestamp" binding:"required"` // RFC3339
	IPAddress       string    `json:"ipAddress"`
	DeviceID        string    `json:"deviceId"`
	TransactionType string    `json:"transactionType"`
}
