package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg"
	"github.com/stretchr/testify/assert"
)

type ApiResponse struct {
	Data map[string]interface{} `json:"data"`
}
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details"`
}

func PostRequestWithHeaders(t *testing.T, url string, payload interface{}, headers map[string]string) (*http.Response, error) {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// Ensure required tracing headers exist for middleware
	if req.Header.Get(pkg.HeaderRequestId) == "" {
		req.Header.Set(pkg.HeaderRequestId, uuid.New().String())
	}
	client := &http.Client{}
	t.Logf("Request POST %s with headers", url)
	resp, err := client.Do(req)
	if resp != nil {
		t.Logf("Response POST %s: Status %d", url, resp.StatusCode)
	}
	t.Cleanup(func() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
	})
	return resp, err
}

func GetTraceId(resp *http.Response) string {
	return resp.Header.Get(pkg.HeaderTraceId)
}

func DecodeSuccess(r io.Reader) (ApiResponse, error) {
	var out ApiResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func DecodeError(r io.Reader) (ErrorResponse, error) {
	var out ErrorResponse
	if err := json.NewDecoder(r).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// BuildAndStartPaymentWorker builds the payment-worker binary and starts it as a child process
// in its own process group so we can terminate it (and any children) reliably from tests.
func BuildAndStartPaymentWorker(t *testing.T, env map[string]string) (*exec.Cmd, func()) {
	t.Helper()

	// Build a temporary binary for the worker
	tmpDir := t.TempDir()
	bin := filepath.Join(tmpDir, "payment-worker-testbin")
	build := exec.Command("go", "build", "-o", bin, "../../services/payment-worker/cmd/main.go")
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		assert.FailNow(t, "failed to build payment-worker", err.Error())
	}

	cmd := exec.Command(bin)

	// Attach test-provided environment variables to the process
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Put the process in its own group so we can signal the whole group
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		assert.FailNow(t, "failed to start payment-worker", err.Error())
	}

	cleanup := func() {
		if cmd.Process == nil {
			return
		}
		// Try graceful shutdown first
		if runtime.GOOS != "windows" {
			pgid, err := syscall.Getpgid(cmd.Process.Pid)
			if err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGINT)
			} else {
				_ = cmd.Process.Signal(syscall.SIGINT)
			}
		} else {
			_ = cmd.Process.Kill()
		}
		// Wait with timeout, then force kill if needed
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
			return
		case <-time.After(10 * time.Second):
			if runtime.GOOS != "windows" {
				pgid, err := syscall.Getpgid(cmd.Process.Pid)
				if err == nil {
					_ = syscall.Kill(-pgid, syscall.SIGKILL)
				}
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}

	return cmd, cleanup
}
