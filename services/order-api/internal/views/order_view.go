package views

import "time"

type OrderRequest struct {
	AccountID       string    `json:"accountId" binding:"required"`
	Amount          float64   `json:"amount" binding:"required,gt=0,lte=1500"`
	Currency        string    `json:"currency" binding:"required,oneof=USD CAD"`
	Timestamp       time.Time `json:"timestamp"`
	IPAddress       string    `json:"ipAddress"`
	TransactionType string    `json:"transactionType"`
}
