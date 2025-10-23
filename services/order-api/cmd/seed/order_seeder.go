package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/configs"
	"go.uber.org/zap"
)

type SeedOrderConfig struct {
	noOfAccountPerUser     uint8
	noOfOrdersPerAccount   uint8
	minimumOrderAmount     float64
	maximumOrderAmount     float64
	orderApiUrl            string
	noOfConcurrentRequests uint32

	logger           *zap.Logger
	db               *database.DB
	userRepo         repositories.UserRepository
	accountRepo      repositories.AccountRepository
	ctx              context.Context
	requestSemaphore chan struct{} // Semaphore to limit concurrent requests
	wg               sync.WaitGroup
}

func main() {
	noOfOrders := flag.Int("noOfOrders", 100, "Number of orders to seed")
	maxConcurrentRequests := flag.Int("maxConcurrentRequests", 5, "Max concurrent requests")
	maxAccountPerUser := flag.Int("maxAccountPerUser", 1, "Max number of accounts per user to seed")
	noOfOrdersPerAccount := flag.Int("noOfOrdersPerAccount", 1, "Number of orders per account to seed")
	minOrderAmount := flag.Float64("minOrderAmount", 10.0, "Min order amount")
	maxOrderAmount := flag.Float64("maxOrderAmount", 20.0, "Max order amount")
	orderApiUrl := flag.String("orderApiUrl", "http://localhost:8081", "Order API URL")

	flag.Parse()

	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	cfg, err := configs.Load(logger)
	if err != nil {
		logger.Fatal("failed_to_load_config", zap.Error(err))
	}
	logger.Info("Config loaded successfully")

	// Initialize postgres db
	dbConfig := database.Config{
		PrimaryDSN: cfg.PrimaryDbAddr,
		ReadDSNs:   []string{cfg.ReadDbAddr},
		MaxConns:   cfg.MaxDbCons,
		MinConns:   cfg.MinDbCons,
	}
	ctx := context.Background()
	db, closer, err := database.New(ctx, logger, dbConfig)
	if err != nil {
		logger.Fatal("failed to init DB", zap.Error(err))
	}
	defer closer()
	logger.Info("db_initialized_successfully")

	logger.Info("seeding_data")

	// Initialize repositories
	userRepo := repositories.NewUserRepository(db)
	accountRepo := repositories.NewAccountRepository(db)

	minOrder := *minOrderAmount
	maxOrder := *maxOrderAmount
	if minOrder > maxOrder {
		minOrder, maxOrder = maxOrder, minOrder
	}
	if *maxAccountPerUser > 100 {
		logger.Fatal("maxAccountPerUser_cannot_be_greater_than_100")
	}
	if *noOfOrdersPerAccount > 100 {
		logger.Fatal("noOfOrdersPerAccount_cannot_be_greater_than_100")
	}

	// Initialize seeder
	seeder := &SeedOrderConfig{
		noOfAccountPerUser:     uint8(*maxAccountPerUser),
		noOfOrdersPerAccount:   uint8(*noOfOrdersPerAccount),
		minimumOrderAmount:     minOrder,
		maximumOrderAmount:     maxOrder,
		orderApiUrl:            *orderApiUrl,
		noOfConcurrentRequests: uint32(*maxConcurrentRequests),
		logger:                 logger,
		db:                     db,
		userRepo:               userRepo,
		accountRepo:            accountRepo,
		ctx:                    ctx,
		requestSemaphore:       make(chan struct{}, uint32(*maxConcurrentRequests)),
	}

	// Start seeder
	seeder.Start(*noOfOrders)
}

type SeedOrder struct {
	IdempotencyID uuid.UUID `json:"idempotencyId" binding:"required,uuid"` // Client-provided UUID for idempotency
	AccountID     uuid.UUID `json:"accountId" binding:"required,uuid"`
	Amount        float64   `json:"amount" binding:"required,gt=0,lte=5000"`
	Currency      string    `json:"currency" binding:"required,oneof=USD CAD"`
}

func (c *SeedOrderConfig) Start(totalOrders int) {
	startTime := time.Now()
	c.logger.Info("start_seeding", zap.Int("no_of_orders", totalOrders), zap.Uint32("no_of_concurrent_requests", c.noOfConcurrentRequests))

	totalRemaining := totalOrders
	userPageNumber := 1 // Start from page 1
	userPageSize := 100 // Larger page size for efficiency

	for {
		// Get users
		users, err := c.userRepo.FindUsers(c.ctx, userPageNumber, userPageSize)
		if err != nil {
			c.logger.Fatal("failed_to_get_users", zap.Error(err))
		}
		if len(users) == 0 {
			if totalRemaining > 0 {
				c.logger.Fatal("not_enough_users_to_seed_all_orders", zap.Int("remaining", totalRemaining))
			}
			break
		}

		for _, user := range users {
			if totalRemaining <= 0 {
				break
			}
			c.logger.Info("processing_user", zap.String("username", user.Username))

			// Fetch user accounts
			accounts, err := c.accountRepo.GetAccountsByUserID(c.ctx, user.ID, 1, int(c.noOfAccountPerUser))
			if err != nil {
				c.logger.Fatal("failed_to_get_accounts", zap.Error(err))
			}

			for _, account := range accounts {
				if totalRemaining <= 0 {
					break
				}

				c.logger.Info("processing_account", zap.String("account_id", account.ID.String()))

				for j := 1; j <= int(c.noOfOrdersPerAccount) && totalRemaining > 0; j++ {
					orderRequest := SeedOrder{
						IdempotencyID: uuid.New(),
						AccountID:     account.ID,
						Amount:        rand.Float64()*(c.maximumOrderAmount-c.minimumOrderAmount) + c.minimumOrderAmount,
						Currency:      account.Currency,
					}
					c.wg.Add(1)
					go c.seedOrder(user.ID, orderRequest)
					totalRemaining--
				}
			}
		}

		if totalRemaining <= 0 {
			break
		}
		userPageNumber++
		time.Sleep(100 * time.Millisecond)
	}

	duration := time.Since(startTime)
	c.logger.Info("waiting_for_all_requests_to_complete", zap.Int("remaining", totalRemaining), zap.Duration("duration", duration))
	c.wg.Wait()
	duration = time.Since(startTime)
	c.logger.Info("seeding_completed", zap.Duration("duration", duration))
}

func (c *SeedOrderConfig) seedOrder(userId uuid.UUID, request SeedOrder) {
	defer c.wg.Done()

	select {
	case <-c.ctx.Done():
		return
	case c.requestSemaphore <- struct{}{}:
	}
	defer func() { <-c.requestSemaphore }()

	c.logger.Info("seeding_order", zap.Any(pkg.IdempotencyKey, request.IdempotencyID))

	// Send request to order API
	c.postRequest(userId, request)

	c.logger.Info("order_seeded_successfully", zap.Any(pkg.IdempotencyKey, request.IdempotencyID))
}

// postRequest sends a POST request to the order API
func (c *SeedOrderConfig) postRequest(userId uuid.UUID, request SeedOrder) {
	client := &http.Client{}
	body, _ := json.Marshal(request)
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodPost, c.orderApiUrl+"/api/v1/orders", bytes.NewBuffer(body))

	// Add headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(pkg.HeaderRequestId, uuid.New().String())
	req.Header.Set(pkg.HeaderUserId, userId.String())

	resp, err := client.Do(req)
	if err != nil {
		c.logger.Error("api_call_failed", zap.Error(err))
	}
	defer resp.Body.Close()

	if http.StatusCreated != resp.StatusCode {
		c.logger.Error("api_call_failed", zap.Any(pkg.IdempotencyKey, request.IdempotencyID), zap.Int("status_code", resp.StatusCode))
		return
	}
	traceId := resp.Header.Get(pkg.HeaderTraceId)
	c.logger.Info("api_call_completed", zap.Any(pkg.IdempotencyKey, request.IdempotencyID), zap.String(pkg.TraceId, traceId))
}
