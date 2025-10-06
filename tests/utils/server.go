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
)

// StartOrderAPIServer starts the order-api service by running `go run ./services/order-api/cmd` on a free port.
// It returns the base URL and a cleanup function that should be deferred in tests.
func StartOrderAPIServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()

	port, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	cmd := exec.Command("go", "run", "./services/order-api/cmd")
	// Ensure the working directory is the repository root where go.mod resides.
	// Try to locate go.mod upwards from this file.
	repoRoot := findRepoRoot()
	if repoRoot != "" {
		cmd.Dir = repoRoot
	}

	// Pass env vars for the service
	env := os.Environ()
	env = append(env, fmt.Sprintf("APP_PORT=%d", port))
	// empty brokers for tests
	env = append(env, "APP_KAFKA_BROKERS=")
	cmd.Env = env

	// Capture stdout/stderr to help with debugging if startup fails
	stderr, _ := cmd.StderrPipe()
	stdout, _ := cmd.StdoutPipe()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start order-api: %v", err)
	}

	// Stream logs to testing output asynchronously
	go streamToTesting(t, stdout)
	go streamToTesting(t, stderr)

	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait for readiness with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := waitForReady(ctx, baseURL+"/health"); err != nil {
		// If not ready, stop process and fail
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("order-api failed to become ready: %v", err)
	}

	cleanup = func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return baseURL, cleanup
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

// findRepoRoot attempts to find the repo root (where go.mod is) by walking up from the current file.
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
