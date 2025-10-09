package testutils

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartOrderAPIServer starts the order-api service by running `go run ./services/order-api/cmd` on a free port.
// It returns the base URL and a cleanup function that should be deferred in tests.
func StartOrderAPIServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	// Start a disposable Postgres for tests
	dsnNoProto, pgTerminate := startPostgresForTests(t)

	cmd := exec.Command("go", "run", "./services/order-api/cmd")

	repoRoot := findRepoRoot()
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}

	// Pass env vars for the service
	env := os.Environ()
	env = append(env, fmt.Sprintf("APP_PORT=%d", port))

	env = append(env, "APP_KAFKA_BROKERS=localhost:9092")
	env = append(env, "APP_KAFKA_TOPIC=orders-test")
	env = append(env, "APP_AES_KEY=Zk6IWX04Qm7ThZ5dJi8Xo4zyb8g9wfcxr5jxa1i3JKU=")
	env = append(env, fmt.Sprintf("APP_PRIMARY_DB_ADDR=%s", dsnNoProto))
	env = append(env, fmt.Sprintf("APP_REPLICA_DB_ADDR=%s", dsnNoProto))
	env = append(env, "GIN_MODE=test")
	cmd.Env = env

	// Capture stdout/stderr to help with debugging if startup fails
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		pgTerminate()
		t.Fatalf("failed to start order-api: %v", err)
	}

	// Stream logs to testing output asynchronously
	go streamToTesting(t, stdout)
	go streamToTesting(t, stderr)

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait for readiness with timeout, allow time for migrations
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := waitForReady(ctx, baseURL+"/health"); err != nil {
		// If not ready, stop process and fail
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		pgTerminate()
		t.Fatalf("order-api failed to become ready: %v", err)
	}

	// Seed database records
	seedDB(t, dsnNoProto)

	cleanup = func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		pgTerminate()
	}
	return baseURL, cleanup
}

// startPostgresForTests starts a PostgreSQL testcontainer and prepares the DB for the app.
// It returns a DSN formatted without the `postgres://` prefix to match the app's expectations
// (the app prepends the protocol internally), and a termination func for cleanup.
var seededUserID uuid.UUID
var seededAccountID uuid.UUID

func GetSeededIDs() (userID uuid.UUID, accountID uuid.UUID) {
	return seededUserID, seededAccountID
}

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

	// Build connection string from mapped port
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

func streamToTesting(t *testing.T, r io.ReadCloser) {
	t.Helper()
	defer r.Close()
	s := bufio.NewScanner(r)
	for s.Scan() {
		line := s.Text()
		// Reduce noise from gin debug logs in CI
		if strings.TrimSpace(line) != "" {
			t.Log(line)
		}
	}
}

// findRepoRoot attempts to find the repo root by walking up from the current file.
func findRepoRoot() string {
	// Start from the directory of this file
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(file)
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "..", "..", "go.mod")
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Clean(filepath.Join(dir, "..", ".."))
		}
		dir = filepath.Clean(filepath.Join(dir, ".."))
	}
	return ""
}
