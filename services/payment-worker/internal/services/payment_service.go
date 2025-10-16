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

var (
	// ErrInsufficientFunds indicates available funds are not enough for the requested debit.
	ErrInsufficientFunds = errors.New("insufficient funds")

	// ErrFraudDetectionFailed indicates the fraud engine errored in a transient way.
	ErrFraudDetectionFailed = errors.New("fraud detection failed")

	// ErrSuspiciousTransaction indicates fraud engine flagged the transaction.
	ErrSuspiciousTransaction = errors.New("suspicious transaction detected")
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
		p.Logger.Error("failed_to_begin_database_transaction", zap.Error(err))
		return err
	}
	defer func() {
		commitErr := p.DB.Commit(ctx, tx)
		if commitErr != nil {
			p.Logger.Error("failed_to_commit_database_transaction", zap.Error(commitErr))
		} else {
			p.Logger.Info("database_transaction_committed_successfully")
		}
	}()

	if !locked {
		p.Logger.Info("account_locked_by_another_process", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("account_id", job.AccountID))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusRetrying, "Account locked by another process")
		p.RetryChannel <- job // send to retry order topic
		return fmt.Errorf("account locked by another process")
	}
	defer p.RedisClient.Del(ctx, lockKey) // Release after
	p.Logger.Info("lock_acquired_successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("account_id", job.AccountID))

	// Decrypt transaction amount
	transactionAmount, err := utils.DecryptToFloat64(job.Amount, p.EncryptionKey)
	if err != nil {
		p.Logger.Error("failed_to_decrypt_transaction_amount", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Decryption of transaction amount failed")
		return err
	}

	// Perform fraud detection asynchronously
	var fraudWG sync.WaitGroup
	fraudWG.Add(1)
	fraudStatusChan := make(chan FraudStatus, 1)
	go p.FraudDetector.Analyze(ctx, &fraudWG, fraudStatusChan, transactionAmount, job)

	// Check account balance
	account, accountBalance, err := p.checkAccountBalance(ctx, tx, job, transactionAmount)
	if err != nil {
		p.Logger.Error("failed_to_check_account_balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		return err
	}

	// Wait for fraud analysis completion
	p.Logger.Info("waiting_for_fraud_analysis_to_complete", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	fraudWG.Wait()
	close(fraudStatusChan)

	// Handle fraud report
	fraudStatus := <-fraudStatusChan
	err = p.handleFraudResult(ctx, job, fraudStatus, tx)
	if err != nil {
		p.Logger.Error("failed_to_handle_fraud_result", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		return err
	}
	p.Logger.Info("fraud_check_passed_successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))

	// Process transaction asynchronously
	var paymentWG sync.WaitGroup
	paymentWG.Add(1)
	paymentResultChan := make(chan TransactionResult, 1)
	go p.processTransaction(ctx, &paymentWG, paymentResultChan, transactionAmount, job)

	// Wait for transaction processing
	p.Logger.Info("waiting_for_transaction_processing_to_complete", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))
	paymentWG.Wait()
	close(paymentResultChan)

	// Handle transaction result
	result := <-paymentResultChan
	err = p.handleTransactionResult(ctx, tx, job, result)
	if err != nil {
		return err
	}
	p.Logger.Info("transaction_processed_successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey))

	// Update account balance, avg/count
	if err = p.UpdateBalanceAvgCount(ctx, tx, job, account, accountBalance, transactionAmount); err != nil {
		p.Logger.Error("failed_to_update_account_balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Balance update failed")
		return err
	}
	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusSuccess, "Payment completed successfully")
	return nil
}

// handleTransactionResult updates the order status and retry flow based on the transaction outcome.
func (p *PaymentProcessorConfig) handleTransactionResult(ctx context.Context, tx pgx.Tx, job dtos.PaymentJob, result TransactionResult) error {
	if result.Error == nil {
		return nil
	}
	if result.EligibleForRetry {
		p.RetryChannel <- job
		p.Logger.Error("transaction_failed_initiating retry", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(result.Error))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusRetrying, "Retrying due to transient failure")
		return result.Error
	}
	p.Logger.Error("transaction_failed_no_retry_attempt", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(result.Error))
	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Non-retryable failure occurred")
	return result.Error
}

// checkAccountBalance verifies if the account has sufficient funds for the transaction.
func (p *PaymentProcessorConfig) checkAccountBalance(ctx context.Context, tx pgx.Tx, paymentJob dtos.PaymentJob, transactionAmount float64) (models.Account, float64, error) {
	var account models.Account
	var balance float64
	var err error
	account, err = p.AccountRepo.FindByID(ctx, paymentJob.AccountID)
	if err != nil {
		p.Logger.Error("failed_to_find_account", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "Account not found")
		return models.Account{}, 0, err
	}

	balance, err = utils.DecryptToFloat64(account.Balance, p.EncryptionKey)
	if err != nil {
		p.Logger.Error("failed_to_decrypt_account_balance", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey), zap.Error(err))
		p.updateOrderStatus(ctx, tx, paymentJob.IdempotencyKey, pkg.OrderStatusFailed, "Balance decryption failed")
		return models.Account{}, 0, err
	}
	if (balance - transactionAmount) < 0 {
		p.Logger.Error("insufficient_funds_detected", zap.Any(pkg.IdempotencyKey, paymentJob.IdempotencyKey))
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
		p.Logger.Error("fraud_detection_failed_retrying", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))
		p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusRetrying, "Retrying due to fraud detection failure")
		p.RetryChannel <- job
		return ErrFraudDetectionFailed
	}
	p.Logger.Error("suspicious_transaction_detected_non_retryable", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Any("report", fraudStatus))
	p.updateOrderStatus(ctx, tx, job.IdempotencyKey, pkg.OrderStatusFailed, "Suspicious transaction detected")
	return ErrSuspiciousTransaction
}

// updateOrderStatus updates the order status in the database and logs the outcome.
func (p *PaymentProcessorConfig) updateOrderStatus(ctx context.Context, tx pgx.Tx, idempotencyKey uuid.UUID, status pkg.OrderStatus, message string) {
	affectedRows, err := p.OrderRepo.UpdateStatusByIdempotencyID(ctx, tx, idempotencyKey, status, message)
	if err != nil {
		p.Logger.Error("failed_to_update_order_status", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Error(err), zap.Any("order_status", status))
		// TODO: Implement order DLQ logic
		return
	}
	p.Logger.Info("order_status_updated_successfully", zap.Any(pkg.IdempotencyKey, idempotencyKey), zap.Any("order_status", status), zap.Int64("affected_rows", affectedRows))
}

// processTransaction simulates the actual payment processing and reports the result.
func (p *PaymentProcessorConfig) processTransaction(ctx context.Context, paymentWG *sync.WaitGroup, resultChan chan TransactionResult, amount float64, paymentJob dtos.PaymentJob) {
	defer paymentWG.Done()

	// Simulate processing time with context cancellation support
	select {
	case <-ctx.Done():
		resultChan <- TransactionResult{EligibleForRetry: true, Error: ctx.Err()}
		return
	default:
		// Continue with processing
	}

	resultChan <- TransactionResult{EligibleForRetry: false, Error: nil}
}

// UpdateBalanceAvgCount updates the account balance and avg/count in the database.
func (p *PaymentProcessorConfig) UpdateBalanceAvgCount(ctx context.Context, tx pgx.Tx, job dtos.PaymentJob, account models.Account, accountBalance float64, transactionAmount float64) error {
	// Update account balance
	newBalance := accountBalance - transactionAmount
	encryptedBalance, err := utils.EncryptAES(utils.Float64ToByte(newBalance), p.EncryptionKey)
	if err != nil {
		p.Logger.Error("failed_to_encrypt_new_balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		return fmt.Errorf("failed to encrypt new balance: %w", err)
	}
	account.Balance = encryptedBalance

	// Update avg and count
	count := account.OrderCount + 1
	avgOrderAmount, err := utils.DecryptToFloat64(account.AvgOrderAmount, p.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt avg order amount: %w", err)
	}
	newAvg := ((avgOrderAmount * float64(account.OrderCount)) + transactionAmount) / float64(count)
	newAvgEnc, err := utils.EncryptAES(utils.Float64ToByte(newAvg), p.EncryptionKey)
	if err != nil {
		p.Logger.Error("failed_to_encrypt_new_avg_order_amount", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		return fmt.Errorf("failed to encrypt new avg order amount: %w", err)
	}
	account.OrderCount = count
	account.AvgOrderAmount = newAvgEnc

	affectedRows, err := p.UserRepo.UpdateBalanceCountAvgByAccountID(ctx, tx, account)
	if err != nil {
		p.Logger.Error("failed_to_update_account_balance", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Error(err))
		return err
	}
	// Cache
	cacheKey := fmt.Sprintf(RedisAvgKey, account.ID.String())
	p.RedisClient.Set(ctx, cacheKey, fmt.Sprintf("%f", newAvg), DeviationCacheTTL)
	p.Logger.Info("account_balance_updated_successfully", zap.Any(pkg.IdempotencyKey, job.IdempotencyKey), zap.Int64("affected_rows", affectedRows))
	return nil
}
