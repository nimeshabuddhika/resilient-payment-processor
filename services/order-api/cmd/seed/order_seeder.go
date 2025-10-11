package main

import (
	"context"
	"flag"
	"math/rand"
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
	noOfAccountPerUser     int
	noOfOrdersPerAccount   int
	minimumOrderAmount     float64
	maximumOrderAmount     float64
	orderApiUrl            string
	noOfConcurrentRequests int

	logger                  *zap.Logger
	db                      *database.DB
	userRepo                repositories.UserRepository
	accountRepo             repositories.AccountRepository
	ctx                     context.Context
	requestSemaphore        chan struct{}  // Semaphore to limit concurrent requests
	remainingRequestsToMake int            // Remaining requests to make
	mu                      sync.Mutex     // Mutex to protect remainingRequestsToMake
	wg                      sync.WaitGroup // WaitGroup to wait for all requests to complete
}

func main() {
	noOfOrders := flag.Int("noOfOrders", 10, "Number of orders to seed")
	maxConcurrentRequests := flag.Int("maxConcurrentRequests", 2, "Max concurrent requests")
	maxAccountPerUser := flag.Int("maxAccountPerUser", 1, "Max number of accounts per user to seed")
	maxOrdersPerAccount := flag.Int("maxOrdersPerAccount", 1, "Max number of orders per account to seed")
	minOrderAmount := flag.Float64("minOrderAmount", 50.0, "Min order amount")
	maxOrderAmount := flag.Float64("maxOrderAmount", 100.0, "Max order amount")
	orderApiUrl := flag.String("orderApiUrl", "http://localhost:8081", "Order API URL")

	flag.Parse()

	// Initialize logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	cfg, err := configs.Load(logger)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	logger.Info("Config loaded successfully")

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
	logger.Info("DB initialized successfully")

	logger.Info("Seeding data")

	// Initialize repositories
	userRepo := repositories.NewUserRepository()
	accountRepo := repositories.NewAccountRepository()

	minOrder := *minOrderAmount
	maxOrder := *maxOrderAmount
	if minOrder > maxOrder {
		// swap to be safe
		minOrder, maxOrder = maxOrder, minOrder
	}
	if *maxAccountPerUser > 100 {
		logger.Fatal("maxAccountPerUser cannot be greater than 100")
	}
	if *maxOrdersPerAccount > 100 {
		logger.Fatal("maxOrdersPerAccount cannot be greater than 100")
	}

	// Initialize seeder
	seeder := NewSeeder(&SeedOrderConfig{
		requestSemaphore:        make(chan struct{}, *maxConcurrentRequests),
		remainingRequestsToMake: *noOfOrders,
		noOfAccountPerUser:      *maxAccountPerUser,
		noOfOrdersPerAccount:    *maxOrdersPerAccount,
		minimumOrderAmount:      minOrder,
		maximumOrderAmount:      maxOrder,
		orderApiUrl:             *orderApiUrl,
		noOfConcurrentRequests:  *maxConcurrentRequests,
		logger:                  logger,
		db:                      db,
		userRepo:                userRepo,
		accountRepo:             accountRepo,
		ctx:                     ctx,
		mu:                      sync.Mutex{},
	})

	// Start seeder
	seeder.Start()

}

func NewSeeder(conf *SeedOrderConfig) *SeedOrderConfig {
	return conf
}

type SeedOrder struct {
	IdempotencyKey string
	UserId         uuid.UUID
	AccountId      uuid.UUID
	Amount         float64
	Currency       string
}

func (c *SeedOrderConfig) Start() {
	currentTime := time.Now()
	c.logger.Info("Starting seeding", zap.Int("no_or_orders", c.remainingRequestsToMake), zap.Int("no_of_concurrent_request", c.noOfConcurrentRequests))
	userPageNumber := 0
	userPageSize := 2

	for {
		// Get users
		userPageNumber++
		users, err := c.userRepo.GetUsers(c.db, c.ctx, userPageNumber, userPageSize)
		if err != nil {
			c.logger.Fatal("failed to get users", zap.Error(err))
		}
		for _, user := range users {
			c.logger.Info("User", zap.Any("username", user.Username))

			// Get accounts for each user
			noOfAccountsPerUer := rand.Intn(c.noOfAccountPerUser) + 1
			for i := 1; i <= noOfAccountsPerUer; i++ {
				accounts, err := c.accountRepo.GetAccountsByUserID(c.db, c.ctx, user.ID, 1, noOfAccountsPerUer)
				if err != nil {
					c.logger.Fatal("failed to get accounts", zap.Error(err))
				}
				for _, account := range accounts {
					// Arrange orders for each account
					noOfOrders := rand.Intn(c.noOfOrdersPerAccount) + 1
					c.logger.Info("Account", zap.Any("account_id", account.ID), zap.Any("no_or_orders", noOfOrders))

					for j := 1; j <= noOfOrders; j++ {
						orderRequest := SeedOrder{
							IdempotencyKey: uuid.New().String(),
							UserId:         user.ID,
							AccountId:      account.ID,
							Amount:         rand.Float64()*(c.maximumOrderAmount-c.minimumOrderAmount) + c.minimumOrderAmount,
							Currency:       "USD",
						}
						go c.seedOrder(orderRequest)
					}
				}
			}
		}

		// Exit if all requests are done
		c.mu.Lock()
		c.logger.Info("Remaining requests to make", zap.Int("remaining_count", c.remainingRequestsToMake))
		if c.remainingRequestsToMake <= 0 {
			break
		}
		c.mu.Unlock()
	}

	c.mu.Unlock()
	duration := time.Since(currentTime)
	c.logger.Info("Waiting for all requests to complete", zap.Int("remaining_count", c.remainingRequestsToMake), zap.Duration("duration", duration))
	c.wg.Wait()
	duration = time.Since(currentTime)
	c.logger.Info("Seeding completed", zap.Duration("duration", duration))
}

func (c *SeedOrderConfig) seedOrder(request SeedOrder) {
	// Lock to prevent race condition
	c.mu.Lock()
	c.remainingRequestsToMake--
	c.wg.Add(1)
	c.mu.Unlock()

	select {
	case <-c.ctx.Done():
	case c.requestSemaphore <- struct{}{}:
		// Acquired
	}
	defer func() {
		<-c.requestSemaphore // Release semaphore
		c.wg.Done()          // Release wait group
	}()
	c.logger.Info("Seeding order", zap.Any("idempotency_key", request.IdempotencyKey))
	time.Sleep(time.Second * 5)
	// TODO Make the POST /orders API call
	c.logger.Info("Order seeded successfully", zap.Any("idempotency_key", request.IdempotencyKey))
}
