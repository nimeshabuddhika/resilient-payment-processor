package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/dtos"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/payment-worker/configs"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// PaymentProcessor defines the interface for processing payment jobs end-to-end for payment-worker service.
// It encapsulates the business logic for handling payment transactions, including fraud detection, retry handling, and database operations.
type PaymentProcessor interface {
	ProcessPayment(ctx context.Context, paymentJob dtos.PaymentJob) error
}

// Domain-level constants and shared errors for the payment service.
const (
	TransactionProcessingDelay = 5 * time.Second
	RandomFailureDivisor       = 4
	NetworkFailureModulo       = 10 // indicates network-related failure check
)

var (
	ErrInsufficientFunds     = errors.New("insufficient balance")
	ErrFraudDetectionFailed  = errors.New("fraud analysis failed")
	ErrSuspiciousTransaction = errors.New("suspicious transaction")
)

// TransactionResult represents the outcome of a transaction attempt.
type TransactionResult struct {
	EligibleForRetry bool
	Error            error
}

// PaymentProcessorConfig groups all dependencies required by the payment service.
type PaymentProcessorConfig struct {
	Logger        *zap.Logger
	Config        *configs.Config
	AccountRepo   repositories.AccountRepository
	OrderRepo     repositories.OrderRepository
	UserRepo      repositories.UserRepository
	DB            *database.DB
	RedisClient   *redis.Client
	FraudDetector FraudDetector
	EncryptionKey []byte
	RetryChannel  chan<- dtos.PaymentJob
}

// NewPaymentProcessor constructs a PaymentProcessor with the given configuration.
func NewPaymentProcessor(conf PaymentProcessorConfig) PaymentProcessor {
	return &conf
}

// ProcessPayment orchestrates the end-to-end payment flow for a single job.
// It handles decryption, fraud detection, transaction processing, balance updates, and state management.
func (p *PaymentProcessorConfig) ProcessPayment(ctx context.Context, job dtos.PaymentJob) error {
	// Acquire lock on account to prevent race conditions
	lockKey := fmt.Sprintf("lock:account:%s", job.AccountID.String()) // Use UUID as string
	locked, err := p.RedisClient.SetNX(ctx, lockKey, "locked", 20*time.Second).Result()
	if err != nil {
		p.Logger.Error("Failed to acquire lock", zap.Error(err))
		return err
	}
	// Begin database transaction
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		p.Logger.Error("Failed to begin database transaction", zap.Error(err))
		return err
	}
	defer func() {
		commitErr := p.DB.Commit(ctx, tx)
		if commitErr != nil {
			p.Logger.Error("Failed to commit database transaction", zap.Error(commitErr))
		} else {
			p.Logger.Info("Database transaction committed successfully")
		}
	}()

	if !locked {
		p.Logger.Info("Account locked by another process", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("account_id", job.AccountID))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusRetrying, "Account locked by another process")
		p.RetryChannel <- job // send to retry order topic
		return fmt.Errorf("account locked by another process")
	}
	defer p.RedisClient.Del(ctx, lockKey) // Release after
	p.Logger.Info("Lock acquired successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("account_id", job.AccountID))

	// Decrypt transaction amount
	transactionAmount, err := utils.DecryptToFloat64(job.Amount, p.EncryptionKey)
	if err != nil {
		p.Logger.Error("Failed to decrypt transaction amount", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Decryption of transaction amount failed")
		return err
	}

	// Perform fraud detection asynchronously
	var fraudWG sync.WaitGroup
	fraudWG.Add(1)
	fraudStatusChan := make(chan FraudStatus, 1)
	go p.FraudDetector.Analyze(ctx, &fraudWG, fraudStatusChan, transactionAmount, job)

	// Check account balance
	_, accountBalance, err := p.checkAccountBalance(ctx, tx, job, transactionAmount)
	if err != nil {
		return err
	}

	// Wait for fraud analysis completion
	p.Logger.Info("Waiting for fraud analysis to complete", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	fraudWG.Wait()
	close(fraudStatusChan)

	// Handle fraud report
	fraudStatus := <-fraudStatusChan
	err = p.handleFraudResult(ctx, job, fraudStatus, tx)
	if err != nil {
		return err
	}
	p.Logger.Info("Fraud check passed successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))

	// Process transaction asynchronously
	var paymentWG sync.WaitGroup
	paymentWG.Add(1)
	paymentResultChan := make(chan TransactionResult, 1)
	go p.processTransaction(ctx, &paymentWG, paymentResultChan, transactionAmount, job)

	// Wait for transaction processing
	p.Logger.Info("Waiting for transaction processing to complete", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	paymentWG.Wait()
	close(paymentResultChan)

	// Handle transaction result
	result := <-paymentResultChan
	err = p.handleTransactionResult(ctx, tx, job, result)
	if err != nil {
		return err
	}
	p.Logger.Info("Transaction processed successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))

	// Update account balance
	newBalance := accountBalance - transactionAmount
	encryptedBalance, err := utils.EncryptAES(utils.Float64ToByte(newBalance), p.EncryptionKey)
	if err != nil {
		p.Logger.Error("Failed to encrypt new balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Encryption of new balance failed")
		return err
	}
	if err = p.UserRepo.UpdateBalanceByAccountID(ctx, tx, job.AccountID, encryptedBalance); err != nil {
		p.Logger.Error("Failed to update account balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Balance update failed")
		return err
	}

	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusSuccess, "Payment completed successfully")

	// TODO: Implement Redis lock release logic
	return nil
}

// handleTransactionResult updates the order status and retry flow based on the transaction outcome.
func (p *PaymentProcessorConfig) handleTransactionResult(ctx context.Context, tx pgx.Tx, job dtos.PaymentJob, result TransactionResult) error {
	if result.Error == nil {
		return nil
	}
	if result.EligibleForRetry {
		p.RetryChannel <- job
		p.Logger.Error("Transaction failed, initiating retry", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(result.Error))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusRetrying, "Retrying due to transient failure")
		return result.Error
	}
	p.Logger.Error("Transaction failed, no retry attempt", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(result.Error))
	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Non-retryable failure occurred")
	return result.Error
}

// checkAccountBalance verifies if the account has sufficient funds for the transaction.
func (p *PaymentProcessorConfig) checkAccountBalance(ctx context.Context, tx pgx.Tx, paymentJob dtos.PaymentJob, transactionAmount float64) (models.Account, float64, error) {
	var account models.Account
	var balance float64
	var err error
	account, err = p.AccountRepo.FindByID(ctx, tx, paymentJob.AccountID)
	if err != nil {
		p.Logger.Error("Failed to find account", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "Account not found")
		return models.Account{}, 0, err
	}

	balance, err = utils.DecryptToFloat64(account.Balance, p.EncryptionKey)
	if err != nil {
		p.Logger.Error("Failed to decrypt account balance", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "Balance decryption failed")
		return models.Account{}, 0, err
	}
	if (balance - transactionAmount) < 0 {
		p.Logger.Error("Insufficient funds detected", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Float64("accountBalance", balance), zap.Float64("transactionAmount", transactionAmount))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "Insufficient funds")
		return models.Account{}, 0, ErrInsufficientFunds
	}
	return account, balance, nil
}

// handleFraudResult routes the payment for retry or failure based on the fraud analysis report.
func (p *PaymentProcessorConfig) handleFraudResult(ctx context.Context, job dtos.PaymentJob, fraudStatus FraudStatus, tx pgx.Tx) error {
	if fraudStatus.FraudFlag == FraudFlagClean {
		return nil
	}
	if fraudStatus.IsEligibleForRetry {
		p.Logger.Error("Fraud detection failed", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusRetrying, "Retrying due to fraud detection failure")
		p.RetryChannel <- job
		return ErrFraudDetectionFailed
	}
	p.Logger.Error("Suspicious transaction detected", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))
	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Suspicious transaction detected")
	return ErrSuspiciousTransaction
}

// updateOrderStatus updates the order status in the database and logs the outcome.
func (p *PaymentProcessorConfig) updateOrderStatus(ctx context.Context, tx pgx.Tx, idempotencyKey uuid.UUID, status pkg.OrderStatus, message string) {
	if err := p.OrderRepo.UpdateStatusIdempotencyID(ctx, tx, idempotencyKey, status, message); err != nil {
		p.Logger.Error("Failed to update order status", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(err), zap.Any("orderStatus", status))
		// TODO: Implement order DLQ logic
		return
	}
	p.Logger.Info("Order status updated successfully", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Any("orderStatus", status))
}

// processTransaction simulates the actual payment processing and reports the result.
func (p *PaymentProcessorConfig) processTransaction(ctx context.Context, paymentWG *sync.WaitGroup, resultChan chan TransactionResult, amount float64, paymentJob dtos.PaymentJob) {
	defer paymentWG.Done()

	// Simulate processing time with context cancellation support
	select {
	case <-ctx.Done():
		resultChan <- TransactionResult{EligibleForRetry: true, Error: ctx.Err()}
		return
	case <-time.After(TransactionProcessingDelay):
		// Continue with processing
	}

	// Simulate random failure
	if int32(paymentJob.IdempotencyKey.ID()%RandomFailureDivisor) == 0 {
		resultChan <- TransactionResult{EligibleForRetry: false, Error: errors.New("transaction failed due to simulated account issue")}
		return
	}

	// Simulate network-related failure
	if time.Now().Minute()%NetworkFailureModulo == 0 {
		resultChan <- TransactionResult{EligibleForRetry: true, Error: errors.New("transaction failed due to simulated network issue")}
		return
	}

	resultChan <- TransactionResult{EligibleForRetry: false, Error: nil}
}
