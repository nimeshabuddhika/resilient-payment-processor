package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/database"
	pkgmodels "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/models"
	pkgrepositories "github.com/nimeshabuddhika/resilient-payment-processor/libs/go-pkg/repositories"
	"github.com/spf13/viper"
	"go.uber.org/zap"
)

// Config holds application configuration loaded from environment variables and optional config file.
type Config struct {
	PrimaryDbAddr string `mapstructure:"PRIMARY_DB_ADDR" validate:"required"`
	ReplicaDbAddr string `mapstructure:"REPLICA_DB_ADDR"`
	MaxDbCons     int32  `mapstructure:"MAX_DB_CONNECTIONS" validate:"min=1"`
	MinDbCons     int32  `mapstructure:"MIN_DB_CONNECTIONS" validate:"min=1"`
}

// loadConfig reads configuration from environment (and optional config file), then validates it.
func loadConfig() (*Config, error) {
	viper.SetEnvPrefix("app") // Prefix for env vars
	viper.AutomaticEnv()

	// Optional: Read from config.yaml if exists
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("./services/user-api/cmd/seed")
	_ = viper.ReadInConfig() // Ignore if no file

	var cfg Config
	if err := pkg.ParseStructEnv(&cfg); err != nil {
		return nil, err
	}
	// Validate after unmarshal
	validate := validator.New()
	if err := validate.Struct(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// main seeds users and their accounts into the database.
// It initializes logging, loads config, connects to the database, runs migrations,
// and performs inserts inside a single transaction.
func main() {
	noOfUsers := flag.Int("noOfUsers", 1000, "Number of users/accounts to seed")
	maxAccountsPerUser := flag.Int("maxAccounts", 2, "Maximum number of accounts per user")
	minAccountBalance := flag.Int("minAccountBalance", 700, "Minimum account balance")
	maxAccountBalance := flag.Int("maxAccountBalance", 1000, "Maximum account balance")

	flag.Parse()

	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	cfg, err := loadConfig()
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
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

	minBal := *minAccountBalance
	maxBal := *maxAccountBalance
	if minBal > maxBal {
		// swap to be safe
		minBal, maxBal = maxBal, minBal
	}

	// Seed data within a transaction to ensure atomicity.
	err = db.WithTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		for userId := 1; userId <= *noOfUsers; userId++ {
			// Create users (ON CONFLICT DO NOTHING to allow re-runs).
			logger.Info("Creating user", zap.Int("userId", userId))

			_, err = userRepo.Create(ctx, tx, pkgmodels.User{
				ID:        int64(userId),
				Username:  fmt.Sprintf("user_%d", userId),
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
				span := maxBal - minBal + 1
				accountBalance := minBal + rand.Intn(span)
				if accountBalance == 1000 {
					logger.Info("accountBalance", zap.Int("accountBalance", accountBalance), zap.Int("userId", userId))
				}
				// Create accounts (ON CONFLICT DO NOTHING to allow re-runs).
				_, err = accountRepo.Create(ctx, tx, pkgmodels.Account{
					UserID:    int64(userId),
					Balance:   float64(accountBalance),
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				})
				if err != nil {
					return err
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
