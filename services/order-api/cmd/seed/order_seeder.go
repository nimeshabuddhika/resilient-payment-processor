// cmd/order_seeder_rps/main.go
//
// Order seeder with per-second outbound request throttling.
// - Concurrency is controlled by a fixed worker pool (maxConcurrentRequests)
// - Throughput is controlled by an RPS limiter (token bucket)
// - Uses a single shared HTTP client with keep-alives and timeouts
// - Graceful shutdown on SIGINT/SIGTERM
//
// Example:
//
//	go run ./cmd/order_seeder_rps \
//	  -noOfOrders=20000 \
//	  -maxConcurrentRequests=200 \
//	  -rps=800 \
//	  -userPageSize=200 \
//	  -maxAccountPerUser=2 \
//	  -noOfOrdersPerAccount=1 \
//	  -orderApiUrl=http://localhost:8081
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/database"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/repositories"
	"github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/configs"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

// --------- CLI flags ---------
var (
	noOfOrders             = flag.Int("noOfOrders", 100, "Total number of orders to seed")
	maxConcurrentRequests  = flag.Int("maxConcurrentRequests", 10, "Max in-flight HTTP requests (worker pool size)")
	maxAccountPerUser      = flag.Int("maxAccountPerUser", 1, "Max number of accounts per user to seed (<=100)")
	noOfOrdersPerAccount   = flag.Int("noOfOrdersPerAccount", 1, "Number of orders per account to seed (<=100)")
	minOrderAmount         = flag.Float64("minOrderAmount", 10.0, "Min order amount")
	maxOrderAmount         = flag.Float64("maxOrderAmount", 20.0, "Max order amount")
	orderApiURL            = flag.String("orderApiUrl", "http://localhost:8081", "Order API base URL")
	userPageSize           = flag.Int("userPageSize", 100, "User page size when reading users from DB")
	rps                    = flag.Int("rps", 200, "Global requests-per-second limit for outbound POST /orders")
	rpsBurst               = flag.Int("rpsBurst", 0, "Burst size for the limiter (0 => equals rps)")
	httpClientTimeoutMs    = flag.Int("httpClientTimeoutMs", 4000, "Total HTTP client timeout (ms)")
	responseHeaderTimeoutS = flag.Int("responseHeaderTimeoutS", 3, "Response header timeout (s)")
	idleConnTimeoutS       = flag.Int("idleConnTimeoutS", 30, "HTTP idle connection timeout (s)")
	maxIdleConns           = flag.Int("maxIdleConns", 1000, "Max idle connections in shared HTTP transport")
	maxIdleConnsPerHost    = flag.Int("maxIdleConnsPerHost", 1000, "Max idle connections per host in shared HTTP transport")
)

type SeedOrder struct {
	IdempotencyID uuid.UUID `json:"idempotencyId" binding:"required,uuid"`
	AccountID     uuid.UUID `json:"accountId"     binding:"required,uuid"`
	Amount        float64   `json:"amount"        binding:"required,gt=0,lte=5000"`
	Currency      string    `json:"currency"      binding:"required,oneof=USD CAD"`
}

type job struct {
	userID uuid.UUID
	order  SeedOrder
}

type Seeder struct {
	// config
	noOfAccountPerUser   uint8
	noOfOrdersPerAccount uint8
	minAmount            float64
	maxAmount            float64
	apiURL               string

	// controls
	workers     int
	limiter     *rate.Limiter
	httpClient  *http.Client
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *zap.Logger
	db          *database.DB
	userRepo    repositories.UserRepository
	accountRepo repositories.AccountRepository

	// metrics
	enqueued int64
	sent     int64
	ok       int64
	fail     int64
}

func main() {
	flag.Parse()

	// logger
	pkg.InitLogger()
	logger := pkg.Logger
	defer logger.Sync()

	// config file(s)
	cfg, err := configs.Load(logger)
	if err != nil {
		logger.Fatal("failed_to_load_config", zap.Error(err))
	}
	logger.Info("config_loaded_successfully")

	// DB (primary + read)
	dbConfig := database.Config{
		PrimaryDSN: cfg.PrimaryDbAddr,
		ReadDSNs:   []string{cfg.ReadDbAddr},
		MaxConns:   cfg.MaxDbCons,
		MinConns:   cfg.MinDbCons,
	}
	rootCtx := context.Background()
	db, closer, err := database.New(rootCtx, logger, dbConfig)
	if err != nil {
		logger.Fatal("failed_to_init_db", zap.Error(err))
	}
	defer closer()
	logger.Info("db_initialized_successfully")

	// graceful shutdown
	ctx, cancel := signal.NotifyContext(rootCtx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// http client (shared)
	client := &http.Client{
		Timeout: time.Duration(*httpClientTimeoutMs) * time.Millisecond,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          *maxIdleConns,
			MaxIdleConnsPerHost:   *maxIdleConnsPerHost,
			IdleConnTimeout:       time.Duration(*idleConnTimeoutS) * time.Second,
			ResponseHeaderTimeout: time.Duration(*responseHeaderTimeoutS) * time.Second,
			ForceAttemptHTTP2:     true,
			DialContext: (&net.Dialer{
				Timeout:   3 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}

	// repositories
	userRepo := repositories.NewUserRepository(db)
	accountRepo := repositories.NewAccountRepository(db)

	// inputs validation / normalization
	minA, maxA := *minOrderAmount, *maxOrderAmount
	if minA > maxA {
		minA, maxA = maxA, minA
	}
	if *maxAccountPerUser > 100 {
		logger.Fatal("maxAccountPerUser_cannot_be_greater_than_100")
	}
	if *noOfOrdersPerAccount > 100 {
		logger.Fatal("noOfOrdersPerAccount_cannot_be_greater_than_100")
	}
	if *rps <= 0 {
		logger.Fatal("rps_must_be_positive")
	}
	burst := *rpsBurst
	if burst <= 0 {
		burst = *rps
	}

	// rate limiter: tokens refill at rps, burst capacity 'burst'
	limiter := rate.NewLimiter(rate.Limit(*rps), burst)

	seeder := &Seeder{
		noOfAccountPerUser:   uint8(*maxAccountPerUser),
		noOfOrdersPerAccount: uint8(*noOfOrdersPerAccount),
		minAmount:            minA,
		maxAmount:            maxA,
		apiURL:               *orderApiURL,
		workers:              *maxConcurrentRequests,
		limiter:              limiter,
		httpClient:           client,
		ctx:                  ctx,
		cancel:               cancel,
		logger:               logger,
		db:                   db,
		userRepo:             userRepo,
		accountRepo:          accountRepo,
	}

	start := time.Now()
	logger.Info("start_seeding",
		zap.Int("no_of_orders", *noOfOrders),
		zap.Int("workers", seeder.workers),
		zap.Int("rps", *rps),
		zap.Int("burst", burst),
		zap.Int("user_page_size", *userPageSize),
	)

	if err := seeder.Run(*noOfOrders, *userPageSize); err != nil {
		logger.Error("seeding_failed", zap.Error(err))
		os.Exit(1)
	}

	elapsed := time.Since(start)
	logger.Info("seeding_completed",
		zap.Duration("duration", elapsed),
		zap.Int64("enqueued", seeder.enqueued),
		zap.Int64("sent", seeder.sent),
		zap.Int64("success", seeder.ok),
		zap.Int64("failed", seeder.fail),
	)
}

func (s *Seeder) Run(totalOrders int, userPageSize int) error {
	jobs := make(chan job, min(totalOrders, 10000)) // bounded buffer

	// progress reporter (1s)
	var wg sync.WaitGroup
	stopProg := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(1 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-stopProg:
				return
			case <-t.C:
				enq := atomic.LoadInt64(&s.enqueued)
				sent := atomic.LoadInt64(&s.sent)
				ok := atomic.LoadInt64(&s.ok)
				fail := atomic.LoadInt64(&s.fail)
				s.logger.Info("progress_tick",
					zap.Int64("enqueued", enq),
					zap.Int64("sent", sent),
					zap.Int64("success", ok),
					zap.Int64("failed", fail),
				)
			}
		}
	}()

	// workers
	var workersWG sync.WaitGroup
	workersWG.Add(s.workers)
	for i := 0; i < s.workers; i++ {
		go func(workerID int) {
			defer workersWG.Done()
			for j := range jobs {
				// throttle by RPS before sending the request
				if err := s.limiter.Wait(s.ctx); err != nil {
					s.logger.Warn("limiter_wait_interrupted", zap.Error(err))
					return
				}
				s.sendOrder(j.userID, j.order)
			}
		}(i)
	}

	// enqueue jobs by paging users (no artificial sleeps)
	remaining := totalOrders
	page := 1

	for {
		if remaining <= 0 {
			break
		}
		select {
		case <-s.ctx.Done():
			break
		default:
		}

		users, err := s.userRepo.FindUsers(s.ctx, page, userPageSize)
		if err != nil {
			close(jobs)
			close(stopProg)
			wg.Wait()
			return fmt.Errorf("failed_to_get_users: %w", err)
		}
		if len(users) == 0 {
			if remaining > 0 {
				s.logger.Error("not_enough_users_to_seed_all_orders", zap.Int("remaining", remaining))
			}
			break
		}

		for _, u := range users {
			if remaining <= 0 {
				break
			}
			s.logger.Info("processing_user", zap.String("username", u.Username))

			accounts, err := s.accountRepo.GetAccountsByUserID(s.ctx, u.ID, 1, int(s.noOfAccountPerUser))
			if err != nil {
				close(jobs)
				close(stopProg)
				wg.Wait()
				return fmt.Errorf("failed_to_get_accounts: %w", err)
			}

			for _, acc := range accounts {
				if remaining <= 0 {
					break
				}
				s.logger.Info("processing_account", zap.String("account_id", acc.ID.String()))

				for j := 0; j < int(s.noOfOrdersPerAccount) && remaining > 0; j++ {
					orderReq := SeedOrder{
						IdempotencyID: uuid.New(),
						AccountID:     acc.ID,
						Amount:        rand.Float64()*(s.maxAmount-s.minAmount) + s.minAmount,
						Currency:      acc.Currency,
					}
					select {
					case <-s.ctx.Done():
						break
					case jobs <- job{userID: u.ID, order: orderReq}:
						atomic.AddInt64(&s.enqueued, 1)
						remaining--
					}
				}
			}
		}

		page++
	}

	// drain
	close(jobs)
	workersWG.Wait()
	close(stopProg)
	wg.Wait()
	return nil
}

func (s *Seeder) sendOrder(userID uuid.UUID, reqBody SeedOrder) {
	start := time.Now()
	atomic.AddInt64(&s.sent, 1)
	s.logger.Info("seeding_order", zap.Any(pkg.IdempotencyKey, reqBody.IdempotencyID))

	// build request
	body, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, s.apiURL+"/api/v1/orders", bytes.NewBuffer(body))
	if err != nil {
		atomic.AddInt64(&s.fail, 1)
		s.logger.Error("build_request_failed", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(pkg.HeaderRequestId, uuid.New().String())
	req.Header.Set(pkg.HeaderUserId, userID.String())

	// send
	resp, err := s.httpClient.Do(req)
	if err != nil {
		atomic.AddInt64(&s.fail, 1)
		s.logger.Error("api_call_failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	lat := time.Since(start)
	if resp.StatusCode != http.StatusCreated {
		atomic.AddInt64(&s.fail, 1)
		s.logger.Error("api_call_failed",
			zap.Any(pkg.IdempotencyKey, reqBody.IdempotencyID),
			zap.Int("status_code", resp.StatusCode),
			zap.Duration("latency", lat),
		)
		return
	}

	traceID := resp.Header.Get(pkg.HeaderTraceId)
	atomic.AddInt64(&s.ok, 1)
	s.logger.Info("api_call_completed",
		zap.Any(pkg.IdempotencyKey, reqBody.IdempotencyID),
		zap.String(pkg.TraceId, traceID),
		zap.Duration("latency", lat),
	)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
