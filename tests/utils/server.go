package testutils

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

	// Start disposable containers: Postgres and Kafka
	dsnNoProto, pgTerminate := startPostgresForTests(t)
	kBootstrap, kTerminate := startKafkaForTests(t)
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

func startPostgresForTests(t *testing.T) (dsnNoProto string, terminate func()) {
	t.Helper()

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
	pgC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start postgres test container: %v", err)
	}

	// Build connection string
	host, err := pgC.Host(ctx)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("failed to get postgres host: %v", err)
	}
	port, err := pgC.MappedPort(ctx, "5432/tcp")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("failed to get mapped port: %v", err)
	}
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", user, password, host, port.Port(), dbName)

	// Prepare DB: create extension and schema
	if err := func() error {
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
	}(); err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("failed to prepare postgres database: %v", err)
	}

	terminate = func() {
		// best-effort terminate
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = pgC.Terminate(ctx)
	}

	// The app expects DSN without protocol and it adds it internally.
	// Convert postgres://user:pass@host:port/db?params -> user:pass@host:port/db?params
	dsnNoProto = strings.TrimPrefix(connStr, "postgres://")
	return dsnNoProto, terminate
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

func startKafkaForTests(t *testing.T) (bootstrap string, terminate func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	kc, err := tckafkamod.RunContainer(ctx)
	if err != nil {
		t.Fatalf("failed to start kafka test container: %v", err)
	}

	// Derive bootstrap from mapped port
	host, err := kc.Host(ctx)
	if err != nil {
		_ = kc.Terminate(context.Background())
		t.Fatalf("failed to get kafka host: %v", err)
	}
	mapped, err := kc.MappedPort(ctx, "9092/tcp")
	if err != nil {
		// try alternative default external port used by some images
		mapped, err = kc.MappedPort(ctx, "9093/tcp")
		if err != nil {
			_ = kc.Terminate(context.Background())
			t.Fatalf("failed to get kafka mapped port: %v", err)
		}
	}
	bootstrap = fmt.Sprintf("%s:%s", host, mapped.Port())
	kafkaBootstrap = bootstrap

	terminate = func() {
		ctx, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		_ = kc.Terminate(ctx)
	}
	return bootstrap, terminate
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
