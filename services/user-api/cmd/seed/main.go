package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/database"
	pkgmodels "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/models"
	pkgrepositories "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/utils"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/user-api/configs"
	"go.uber.org/zap"
)

// main seeds users and their accounts into the database.
// It initializes logging, loads config, connects to the database, runs migrations,
// and performs inserts inside a single transaction.
func main() {
	noOfUsers := flag.Int("noOfUsers", 1000, "Number of users to seed")
	maxAccountsPerUser := flag.Int("maxAccounts", 2, "Max accounts per user")
	minAccountBalance := flag.Float64("minBalance", 700.0, "Min account balance")
	maxAccountBalance := flag.Float64("maxBalance", 1000.0, "Max account balance")
	fraudRate := flag.Float64("fraudRate", 0.01, "Fraud order rate for AI training")

	flag.Parse()

	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	cfg, err := configs.Load()
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	key, err := utils.DecodeString(cfg.AesKey)
	if err != nil {
		logger.Fatal("failed to decode AES key", zap.Error(err))
	}

	// Initialize postgres db
	dbConfig := database.Config{
		PrimaryDSN:  cfg.PrimaryDbAddr,
		ReplicaDSNs: []string{cfg.ReplicaDbAddr},
		MaxConns:    cfg.MaxDbCons,
		MinConns:    cfg.MinDbCons,
	}

	ctx := context.Background()
	db, closer, err := database.New(ctx, logger, dbConfig)
	if err != nil {
		logger.Fatal("failed to init DB", zap.Error(err))
	}
	defer closer()

	// Initialize db migrations
	err = database.RunMigrations(logger, cfg.PrimaryDbAddr)
	if err != nil {
		logger.Fatal("failed to run database migrations", zap.Error(err))
	}

	// Initialize user repository
	userRepo := pkgrepositories.NewUserRepository()
	// Initialize account repository
	accountRepo := pkgrepositories.NewAccountRepository()
	// Initialize order repository
	orderRepo := pkgrepositories.NewOrderRepository()

	minBal := *minAccountBalance
	maxBal := *maxAccountBalance
	if minBal > maxBal {
		// swap to be safe
		minBal, maxBal = maxBal, minBal
	}

	// Seed data within a transaction to ensure atomicity.
	err = db.WithTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for i := 1; i <= *noOfUsers; i++ {
			userID := uuid.New()
			// Create users (ON CONFLICT DO NOTHING to allow re-runs).
			logger.Info("Creating user", zap.Int("i", i), zap.Any("userId", userID))

			_, err = userRepo.Create(ctx, tx, pkgmodels.User{
				ID:        userID,
				Username:  fmt.Sprintf("user_%d", i),
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
			if err != nil {
				return err
			}

			// Create maxAccountsPerUser accounts
			noOfAccounts := rand.Intn(*maxAccountsPerUser) + 1
			for i := 1; i <= noOfAccounts; i++ {
				// Create accounts with a random balance
				bal := *minAccountBalance + rand.Float64()*(*maxAccountBalance-*minAccountBalance)
				balEnc, err := utils.EncryptAES([]byte(fmt.Sprintf("%.2f", bal)), key)
				if err != nil {
					return err
				}
				accID := uuid.New()
				// Create accounts (ON CONFLICT DO NOTHING to allow re-runs).
				_, err = accountRepo.Create(ctx, tx, pkgmodels.Account{
					ID:        accID,
					UserID:    userID,
					Balance:   balEnc,
					Currency:  "CAD",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				})
				if err != nil {
					return err
				}

				// Seed orders with fraud (for AI: train Torch on anomalies).
				noOfOrders := rand.Intn(10) + 1 // Volume for 100k+ sim.
				for k := 0; k < noOfOrders; k++ {
					isFraud := rand.Float64() < *fraudRate
					amt := 50.0 + rand.Float64()*150.0
					if isFraud {
						amt = 1000.0 + rand.Float64()*4000.0 // Anomaly.
					}
					logger.Info("Creating order", zap.Any("userId", userID), zap.Any("accountId", accID), zap.Float64("amt", amt), zap.Bool("isFraud", isFraud))

					_, err := orderRepo.CreateAiDataset(ctx, tx, pkgmodels.Order{
						UserID:         userID,
						AccountID:      accID,
						IdempotencyKey: uuid.New(),
						Amount:         amt,
						Status:         "pending",
						CreatedAt:      time.Now(),
						UpdatedAt:      time.Now(),
					}, isFraud)
					if err != nil {
						return err
					}
				}
			}
		}
		return nil
	})

	if err != nil {
		logger.Fatal("failed to seed data", zap.Error(err))
	}
	logger.Info("Data seeded successfully")
}
