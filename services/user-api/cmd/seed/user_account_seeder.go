package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/dtos"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/models"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
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
	exportData := flag.Bool("exportData", true, "Export AI dataset to JSON after seeding")

	flag.Parse()

	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	cfg, err := configs.Load(logger)
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
		logger.Fatal("failed_to_init_DB", zap.Error(err))
	}
	defer closer()

	// Initialize db migrations
	err = database.RunMigrations(logger, cfg.PrimaryDbAddr)
	if err != nil {
		logger.Fatal("failed_to_run_database_migrations", zap.Error(err))
	}

	// Initialize repositories
	userRepo := repositories.NewUserRepository()
	accountRepo := repositories.NewAccountRepository()
	orderRepo := repositories.NewOrderRepository()

	minBal := *minAccountBalance
	maxBal := *maxAccountBalance
	if minBal > maxBal {
		// swap to be safe
		minBal, maxBal = maxBal, minBal
	}

	// Track user history for feature generation (simulated during seeding)
	type userHistory struct {
		orderCount  int
		totalAmount float64
		avgAmount   float64
	}
	userHistories := make(map[uuid.UUID]*userHistory)

	// Seed data within a transaction to ensure atomicity.
	err = db.WithTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for i := 1; i <= *noOfUsers; i++ {
			userID := uuid.New()
			// Create users (ON CONFLICT DO NOTHING to allow re-runs).
			logger.Info("creating_user", zap.Int("i", i), zap.Any("user_id", userID))

			_, err = userRepo.Create(ctx, tx, models.User{
				ID:        userID,
				Username:  fmt.Sprintf("user_%d", i),
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			})
			if err != nil {
				return err
			}

			// Initialize user history
			userHistories[userID] = &userHistory{}

			// Create maxAccountsPerUser accounts
			noOfAccounts := rand.Intn(*maxAccountsPerUser) + 1
			for j := 1; j <= noOfAccounts; j++ {
				// Create accounts with a random balance
				bal := *minAccountBalance + rand.Float64()*(*maxAccountBalance-*minAccountBalance)
				balEnc, err := utils.EncryptAES(utils.Float64ToByte(bal), key)
				if err != nil {
					return err
				}
				accID := uuid.New()
				// Create accounts (ON CONFLICT DO NOTHING to allow re-runs).
				_, err = accountRepo.Create(ctx, tx, models.Account{
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

					// Generate features
					hist := userHistories[userID]
					hist.orderCount++
					velocity := 1 + rand.Intn(5) // Simulated orders/hour (1-5 normal)
					if isFraud {
						velocity += rand.Intn(6) // Bias higher (up to 10) for fraud
					}
					deviation := 0.0
					if hist.orderCount > 1 {
						deviation = math.Abs(amt-hist.avgAmount) / hist.avgAmount
					}

					// Removed overriding bias to avoid label inconsistency

					logger.Info("creating_order", zap.Any("user_id", userID), zap.Any("account_id", accID), zap.Float64("amt", amt), zap.Bool("is_fraud", isFraud))

					// Assume models.Order extended with: TransactionVelocity int, IpAddress string, AmountDeviation float64
					_, err = orderRepo.CreateAiDataset(ctx, tx, models.OrderAIModel{
						UserID:              userID,
						AccountID:           accID,
						IdempotencyKey:      uuid.New(),
						Amount:              roundFloatToTwo(amt),
						Currency:            "CAD",
						CreatedAt:           time.Now(),
						UpdatedAt:           time.Now(),
						IsFraud:             isFraud,
						TransactionVelocity: velocity,
						AmountDeviation:     deviation,
					})
					if err != nil {
						return err
					}

					// Update history after features (consistent with calc)
					hist.totalAmount += amt
					hist.avgAmount = hist.totalAmount / float64(hist.orderCount)
				}
			}
		}
		return nil
	})

	if err != nil {
		logger.Fatal("failed_to_seed_data", zap.Error(err))
	}
	logger.Info("data_seeded_successfully")

	// Optional export
	if *exportData {
		orderPageNumber := 1 // Start from page 1
		orderPageSize := 100 // Larger page size for efficiency
		orderList := make([]dtos.OrderAIDto, 0)
		for {
			// Assume orderRepo.GetAllAiDataset(ctx, db) returns []models.AiOrder or similar
			orders, err := orderRepo.GetAllAiDataset(ctx, db, orderPageNumber, orderPageSize) // Implement this in repo
			if err != nil {
				logger.Fatal("failed_to_export_dataset", zap.Error(err))
			}
			// Break if there is no data
			if len(orders) == 0 {
				break
			}

			for _, order := range orders {
				orderList = append(orderList, order.ToOrderAIDto())
			}
			orderPageNumber++
		}

		orderResult := dtos.OrderResult{
			Orders:    orderList,
			Count:     len(orderList),
			CreatedAt: time.Now(),
		}
		jsonData, err := json.Marshal(orderResult)
		if err != nil {
			logger.Fatal("failed_to_marshal_dataset", zap.Error(err))
		}
		// create ai folder if not exist
		path := filepath.Join(".", "ai")
		err = os.MkdirAll(path, os.ModePerm)
		if err != nil {
			logger.Fatal("failed_to_create_dir", zap.Error(err), zap.String("directory", path))
		}
		// Write order data
		path = filepath.Join(path, "ai_dataset.json")
		err = os.WriteFile(path, jsonData, 0644)
		if err != nil {
			logger.Fatal("failed_to_write_json", zap.Error(err))
		}
		logger.Info("dataset_exported_successfully", zap.String("file", path), zap.Int("data_count", len(orderList)))
	}
}

func roundFloatToTwo(val float64) float64 {
	return math.Round(val*100) / 100
}
