package tests

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	ckafka "github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	appsvc "github.com/nimeshabuddhika/resilient-payment-processor/services/order-api/app"
	"github.com/testcontainers/testcontainers-go"
	tckafkamod "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartOrderAPIServer starts the order-api HTTP server in-process using NewApp.
// It returns the base URL and a cleanup function that should be deferred in tests.
func StartOrderAPIServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	// Start disposable containers: Postgres and Kafka concurrently
	type pgResult struct {
		dsnNoProto string
		terminate  func()
		err        error
	}
	type kResult struct {
		bootstrap string
		terminate func()
		err       error
	}
	pgCh := make(chan pgResult, 1)
	kCh := make(chan kResult, 1)
	go func() {
		dsn, term, err := startPostgresForTests()
		pgCh <- pgResult{dsnNoProto: dsn, terminate: term, err: err}
	}()
	go func() {
		boot, term, err := startKafkaForTests()
		kCh <- kResult{bootstrap: boot, terminate: term, err: err}
	}()

	var (
		pgRes = <-pgCh
		kRes  = <-kCh
	)
	if pgRes.err != nil || kRes.err != nil {
		// Best-effort cleanup if any started successfully
		if pgRes.err == nil && pgRes.terminate != nil {
			pgRes.terminate()
		}
		if kRes.err == nil && kRes.terminate != nil {
			kRes.terminate()
		}
		t.Fatalf("failed to start dependencies: postgres err=%v, kafka err=%v", pgRes.err, kRes.err)
	}
	dsnNoProto, pgTerminate := pgRes.dsnNoProto, pgRes.terminate
	kBootstrap, kTerminate := kRes.bootstrap, kRes.terminate
	// Ensure the Kafka topic exists before starting the app
	ensureKafkaTopic(t, kBootstrap, kafkaTopic, 4)

	// Configure environment variables
	_ = os.Setenv("APP_PORT", fmt.Sprintf("%d", port))
	_ = os.Setenv("APP_KAFKA_BROKERS", kBootstrap)
	_ = os.Setenv("APP_KAFKA_TOPIC", kafkaTopic)
	_ = os.Setenv("APP_AES_KEY", "Zk6IWX04Qm7ThZ5dJi8Xo4zyb8g9wfcxr5jxa1i3JKU=")
	_ = os.Setenv("APP_PRIMARY_DB_ADDR", dsnNoProto)
	_ = os.Setenv("APP_REPLICA_DB_ADDR", dsnNoProto)
	_ = os.Setenv("GIN_MODE", "test")

	// Build app and run server
	pkg.InitLogger()
	logger := pkg.Logger
	ctx := context.Background()
	srv, appCleanup, err := appsvc.NewApp(ctx, logger)
	if err != nil {
		pgTerminate()
		kTerminate()
		t.Fatalf("failed to build order-api app: %v", err)
	}

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	go func() {
		_ = srv.ListenAndServe()
	}()

	// Wait for readiness with timeout, allow time for migrations
	wctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := waitForReady(wctx, baseURL+"/health"); err != nil {
		_ = srv.Close()
		appCleanup()
		pgTerminate()
		kTerminate()
		t.Fatalf("order-api failed to become ready: %v", err)
	}

	// Seed database records now that migrations are applied
	seedDB(t, dsnNoProto)

	cleanup = func() {
		// Graceful shutdown
		ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(ctx)
		appCleanup()
		pgTerminate()
		kTerminate()
	}
	return baseURL, cleanup
}

// startPostgresForTests starts a PostgreSQL testcontainer and prepares the DB for the app.
// It returns a DSN formatted without the `postgres://` prefix to match the app's expectations
// (the app prepends the protocol internally), and a termination func for cleanup.
var seededUserID uuid.UUID
var seededAccountID uuid.UUID
var kafkaBootstrap string
var kafkaTopic string = "orders-test"

func GetSeededIDs() (userID uuid.UUID, accountID uuid.UUID) {
	return seededUserID, seededAccountID
}

func GetKafkaBootstrap() string { return kafkaBootstrap }
func GetKafkaTopic() string     { return kafkaTopic }

func startPostgresForTests() (dsnNoProto string, terminate func(), err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const (
		user     = "db_user"
		password = "db_password"
		dbName   = "resilient_payment_processor"
	)

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     user,
			"POSTGRES_PASSWORD": password,
			"POSTGRES_DB":       dbName,
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	pgC, e := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if e != nil {
		err = fmt.Errorf("failed to start postgres test container: %w", e)
		return
	}

	// Build connection string
	host, e := pgC.Host(ctx)
	if e != nil {
		_ = pgC.Terminate(context.Background())
		err = fmt.Errorf("failed to get postgres host: %w", e)
		return
	}
	port, e := pgC.MappedPort(ctx, "5432/tcp")
	if e != nil {
		_ = pgC.Terminate(context.Background())
		err = fmt.Errorf("failed to get mapped port: %w", e)
		return
	}
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, host, port.Port(), dbName)

	// Prepare DB: create extension and schema
	if e := func() error {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		conn, err := pgx.Connect(cctx, connStr)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(cctx) }()
		if _, err := conn.Exec(cctx, "CREATE EXTENSION IF NOT EXISTS pgcrypto;"); err != nil {
			return err
		}
		if _, err := conn.Exec(cctx, "CREATE SCHEMA IF NOT EXISTS svc_schema;"); err != nil {
			return err
		}
		return nil
	}(); e != nil {
		_ = pgC.Terminate(context.Background())
		err = fmt.Errorf("failed to prepare postgres database: %w", e)
		return
	}

	terminate = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = pgC.Terminate(ctx)
	}

	// The app expects DSN without protocol and it adds it internally.
	// Convert postgres://user:pass@host:port/db?params -> user:pass@host:port/db?params
	dsnNoProto = strings.TrimPrefix(connStr, "postgres://")
	return
}

func seedDB(t *testing.T, dsnNoProto string) {
	// Connect and insert a test user and account with encrypted balance
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connStr := "postgres://" + dsnNoProto
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("failed to connect to postgres for seeding: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	// Use the same AES key as configured
	const aesKeyB64 = "Zk6IWX04Qm7ThZ5dJi8Xo4zyb8g9wfcxr5jxa1i3JKU="
	key, err := utils.DecodeString(aesKeyB64)
	if err != nil {
		t.Fatalf("failed to decode AES key: %v", err)
	}
	encBal, err := utils.EncryptAES(utils.Float64ToByte(1000.0), key)
	if err != nil {
		t.Fatalf("failed to encrypt seed balance: %v", err)
	}

	seededUserID = uuid.New()
	seededAccountID = uuid.New()

	if _, err := conn.Exec(ctx, `INSERT INTO users (id, username) VALUES ($1, $2)`, seededUserID, "test-user"); err != nil {
		t.Fatalf("failed to insert seed user: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO accounts (id, user_id, balance, currency) VALUES ($1, $2, $3, $4)`, seededAccountID, seededUserID, encBal, "USD"); err != nil {
		t.Fatalf("failed to insert seed account: %v", err)
	}
}

// EnsureKafkaTopic exposes ensureKafkaTopic for other test packages.
func EnsureKafkaTopic(t *testing.T, bootstrap, topic string, partitions int) {
	ensureKafkaTopic(t, bootstrap, topic, partitions)
}

// StartPostgresForTests exposes startPostgresForTests for other test packages.
func StartPostgresForTests() (dsnNoProto string, terminate func(), err error) {
	return startPostgresForTests()
}

// StartKafkaForTests exports the Kafka test container starter for reuse in other integration tests.
func StartKafkaForTests() (bootstrap string, terminate func(), err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	kc, e := tckafkamod.RunContainer(ctx)
	if e != nil {
		err = fmt.Errorf("failed to start kafka test container: %w", e)
		return
	}

	// Derive bootstrap from mapped port
	host, e := kc.Host(ctx)
	if e != nil {
		_ = kc.Terminate(context.Background())
		err = fmt.Errorf("failed to get kafka host: %w", e)
		return
	}
	mapped, e := kc.MappedPort(ctx, "9092/tcp")
	if e != nil {
		// try alternative default external port used by some images
		mapped, e = kc.MappedPort(ctx, "9093/tcp")
		if e != nil {
			_ = kc.Terminate(context.Background())
			err = fmt.Errorf("failed to get kafka mapped port: %w", e)
			return
		}
	}
	bootstrap = fmt.Sprintf("%s:%s", host, mapped.Port())
	kafkaBootstrap = bootstrap

	terminate = func() {
		ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = kc.Terminate(ctx)
	}
	return
}

// Backward-compatible unexported wrapper used by StartOrderAPIServer
func startKafkaForTests() (bootstrap string, terminate func(), err error) {
	return StartKafkaForTests()
}

func ensureKafkaTopic(t *testing.T, bootstrap, topic string, partitions int) {
	admin, err := ckafka.NewAdminClient(&ckafka.ConfigMap{"bootstrap.servers": bootstrap})
	if err != nil {
		t.Logf("kafka admin create failed: %v", err)
		return
	}
	defer admin.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	specs := []ckafka.TopicSpecification{{
		Topic:             topic,
		NumPartitions:     partitions,
		ReplicationFactor: 1,
	}}
	_, _ = admin.CreateTopics(ctx, specs)
}

func getFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// StartRedisForTests spins up a Redis container and returns host:port and a terminate function.
func StartRedisForTests() (addr string, terminate func(), err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(60 * time.Second),
	}
	rc, e := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if e != nil {
		err = fmt.Errorf("failed to start redis test container: %w", e)
		return
	}

	host, e := rc.Host(ctx)
	if e != nil {
		_ = rc.Terminate(context.Background())
		err = fmt.Errorf("failed to get redis host: %w", e)
		return
	}
	mapped, e := rc.MappedPort(ctx, "6379/tcp")
	if e != nil {
		_ = rc.Terminate(context.Background())
		err = fmt.Errorf("failed to get redis mapped port: %w", e)
		return
	}
	addr = fmt.Sprintf("%s:%s", host, mapped.Port())

	terminate = func() {
		ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = rc.Terminate(ctx)
	}
	return
}

func waitForReady(ctx context.Context, url string) error {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("timeout waiting for %s", url)
		}
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 500 { // health might be 200
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
}
