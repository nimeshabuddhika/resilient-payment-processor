package dtos

type PredictRequest struct {
	Amount              float64 `json:"amount"`
	TransactionVelocity int     `json:"transactionVelocity"`
	AmountDeviation     float64 `json:"amountDeviation"`
}

type PredictResponse struct {
	FraudProbability float64 `json:"fraudProbability"`
	IsFraud          bool    `json:"isFraud"`
	Threshold        float64 `json:"threshold"`
}
