package services

import "context"

type PaymentService interface {
	ProcessPayment(ctx context.Context, payment *Payment) error
}
