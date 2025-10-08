package services

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/views"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type PaymentService interface {
	HandlePayment(ctx context.Context, paymentJob views.PaymentJob) error
}

type TransactionStatus struct {
	EligibleForRetry bool
	Err              error
}

type PaymentServiceConfig struct {
	Logger        *zap.Logger
	Config        *configs.Config
	AccountRepo   repositories.AccountRepository
	OrderRepo     repositories.OrderRepository
	UserRepo      repositories.UserRepository
	DB            *database.DB
	RedisClient   *redis.Client
	FraudDetector FraudDetector
	AESKey        []byte
	RetryChan     chan<- views.PaymentJob
}

func NewPaymentService(conf PaymentServiceConfig) PaymentService {
	return &conf
}

func (p PaymentServiceConfig) HandlePayment(ctx context.Context, job views.PaymentJob) error {
	// Begin transaction
	tx, err := p.DB.Begin(ctx)
	defer func() {
		transErr := p.DB.Commit(ctx, tx)
		p.Logger.Info("transaction committed", zap.Error(transErr))
	}()

	//decrypt transaction amount
	transactionAmount, err := utils.DecryptToFloat64(job.Amount, p.AESKey)
	if err != nil {
		p.Logger.Error("failed to decrypt transaction amount", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "failed to decrypt transaction amount")
		return err
	}

	// Check if the transaction is fraudulent
	var fraudWG sync.WaitGroup
	fraudWG.Add(1)
	fraudStatusChan := make(chan FraudStatus, 1)
	go p.FraudDetector.Analyze(ctx, &fraudWG, fraudStatusChan, transactionAmount, job)

	// Acquire redis lock with pending balance

	// Check if balance is enough to process transaction

	_, accountBalance, err := p.checkAccountBalance(ctx, tx, job, transactionAmount)
	if err != nil {
		return err
	}

	// Wait for fraud analysis to complete
	p.Logger.Info("waiting for fraud analysis to complete", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	fraudWG.Wait()
	close(fraudStatusChan)

	// Handle fraud report
	fraudStatus := <-fraudStatusChan
	err = p.handleFraudReport(ctx, job, fraudStatus, tx)
	if err != nil {
		return err
	}
	p.Logger.Info("transaction is not fraudulent", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))

	// process transaction
	var paymentWG sync.WaitGroup
	paymentWG.Add(1)
	paymentStatusChan := make(chan TransactionStatus, 1)
	// Initiate payment processing
	go p.processTransaction(ctx, &paymentWG, paymentStatusChan, transactionAmount, job)

	// wait for payment processing to complete
	p.Logger.Info("waiting for payment processing to complete", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	paymentWG.Wait()
	close(paymentStatusChan)

	// Read payment status
	transStatus := <-paymentStatusChan
	err = p.handlePaymentStatus(ctx, tx, job, transStatus)
	if err != nil {
		return err
	}

	p.Logger.Info("payment processed successfully, updating account balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))

	// update account balance
	newBalance := accountBalance - transactionAmount
	newBalanceEnc, _ := utils.EncryptAES(utils.Float64ToByte(newBalance), p.AESKey)
	err = p.UserRepo.UpdateBalanceByAccountId(ctx, tx, job.AccountID, newBalanceEnc)
	if err != nil {
		p.Logger.Error("failed to update account balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "failed to update account balance")
		return err
	}

	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusSuccess, "payment processed successfully")

	// release redis lock and pending balance
	return nil
}

func (p PaymentServiceConfig) handlePaymentStatus(ctx context.Context, tx pgx.Tx, paymentJob views.PaymentJob, transStatus TransactionStatus) error {
	// handle success payment
	if transStatus.Err == nil {
		return nil
	} else if transStatus.EligibleForRetry { // handle retryable payment error
		// send it to retry channel
		p.RetryChan <- paymentJob

		p.Logger.Error("transaction failed, initiating retry process", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(transStatus.Err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusRetying, "transaction failed, initiating retry process")
		return transStatus.Err
	}
	// handle non-retryable payment error
	p.Logger.Error("transaction failed, no retry attempt", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(transStatus.Err))
	p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "transaction failed, no retry possible")
	return transStatus.Err
}

// checkAccountBalance checks if the account balance is sufficient to process the transaction
func (p PaymentServiceConfig) checkAccountBalance(ctx context.Context, tx pgx.Tx, paymentJob views.PaymentJob, transactionAmount float64) (models.Account, float64, error) {
	var account models.Account
	var accountBalance float64
	var err error
	if account, err = p.AccountRepo.FindById(ctx, tx, paymentJob.AccountID); err != nil {
		p.Logger.Error("failed to find account", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "failed to find account")
		return models.Account{}, 0, err
	}

	if accountBalance, err = utils.DecryptToFloat64(account.Balance, p.AESKey); err != nil {
		p.Logger.Error("failed to convert account balance to float64", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "failed to convert account balance to float64")
		return models.Account{}, 0, err
	}
	if (accountBalance - transactionAmount) < 0 {
		p.Logger.Error("insufficient balance", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Any("account_balance", accountBalance), zap.Any("transaction_amount", transactionAmount))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "insufficient balance")
		return models.Account{}, 0, errors.New("insufficient balance")
	}
	return account, accountBalance, nil
}

func (p PaymentServiceConfig) handleFraudReport(ctx context.Context, paymentJob views.PaymentJob, fraudStatus FraudStatus, tx pgx.Tx) error {
	if fraudStatus.FraudFlag == FraudFlagClean {
		return nil
	}

	if fraudStatus.IsEligibleForRetry {
		p.Logger.Error("fraud analysis failed", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Any("report", fraudStatus))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusRetying, "fraud analysis failed, retying transaction")
		// send to retry channel
		p.RetryChan <- paymentJob

		return errors.New("fraud analysis failed")
	}

	p.Logger.Error("suspicious transaction", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Any("report", fraudStatus))
	p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "suspicious transaction, no retry possible")
	return errors.New("suspicious transaction")
}

// updateOrderDbStatus is a helper function to update order status in the database
func (p PaymentServiceConfig) updateOrderStatus(ctx context.Context, tx pgx.Tx, idempotencyKey uuid.UUID, status pkg.OrderStatus, message string) {
	err := p.OrderRepo.UpdateStatusIdempotencyID(ctx, tx, idempotencyKey, status, message)
	if err != nil {
		p.Logger.Error("failed to update order status", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(err), zap.Any("order_status", status))
		// send to orderDLQ
	}
	p.Logger.Info("order status updated successfully", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Any("order_status", status))
}

func (p PaymentServiceConfig) processTransaction(ctx context.Context, paymentWG *sync.WaitGroup, statusChan chan TransactionStatus, _ float64, paymentJob views.PaymentJob) {
	defer paymentWG.Done()

	// simulate processing time with context cancellation support
	select {
	case <-ctx.Done():
		statusChan <- TransactionStatus{EligibleForRetry: true, Err: ctx.Err()}
		return
	case <-time.After(5 * time.Second):
		// continue processing
	}

	// simulate random failure
	if int32(paymentJob.IdempotencyKey.ID()%4) == 0 {
		p.Logger.Error("transaction failed, no retry attempt", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey))
		statusChan <- TransactionStatus{EligibleForRetry: false, Err: errors.New("transaction failed due to simulated account issue")}
		return
	}

	// simulate 10 minute network issue
	if time.Now().Minute()%10 == 0 {
		p.Logger.Error("transaction failed, retry attempt", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey))
		statusChan <- TransactionStatus{EligibleForRetry: true, Err: errors.New("transaction failed due to simulated network traffic")}
		return
	}

	p.Logger.Info("transaction processed successfully", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey))
	statusChan <- TransactionStatus{EligibleForRetry: false, Err: nil}
}
