package dtos

type PredictRequest struct {
	Amount              float64 `json:"amount"`
	TransactionVelocity int     `json:"transactionVelocity"`
	AmountDeviation     float64 `json:"amountDeviation"`
}

type PredictResponse struct {
	Score     float64 `json:"score"`
	IsFraud   bool    `json:"isFraud"`
	Threshold float64 `json:"threshold"`
}
