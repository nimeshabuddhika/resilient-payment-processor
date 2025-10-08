package cache

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config holds production-ready Redis options.
// Example envs you might map into these:
// - Addr: "redis:6379" or "prod-redis.example.com:6379"
// - Username/Password for ACL-auth setups
// - DB: logical DB index (0 by default)
// - UseTLS: true for managed Redis providers
type Config struct {
	Addr            string
	Username        string
	Password        string
	DB              int
	UseTLS          bool
	DialTimeout     time.Duration
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	PoolSize        int
	MinIdleConns    int
	MaxRetries      int
	MaxRetryBackoff time.Duration
	MinRetryBackoff time.Duration
}

// New returns a configured redis.Client and verifies connectivity with PING.
// Call Close() on the returned client during shutdown.
func New(ctx context.Context, cfg Config) (*redis.Client, func(), error) {
	opts := &redis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  defaultDuration(cfg.DialTimeout, 3*time.Second),
		ReadTimeout:  defaultDuration(cfg.ReadTimeout, 2*time.Second),
		WriteTimeout: defaultDuration(cfg.WriteTimeout, 2*time.Second),
		PoolSize:     defaultInt(cfg.PoolSize, 10),
		MinIdleConns: defaultInt(cfg.MinIdleConns, 2),
		MaxRetries:   defaultInt(cfg.MaxRetries, 3),
	}

	// Backoff
	if cfg.MinRetryBackoff > 0 {
		opts.MinRetryBackoff = cfg.MinRetryBackoff
	} else {
		opts.MinRetryBackoff = 50 * time.Millisecond
	}
	if cfg.MaxRetryBackoff > 0 {
		opts.MaxRetryBackoff = cfg.MaxRetryBackoff
	} else {
		opts.MaxRetryBackoff = 500 * time.Millisecond
	}

	// TLS for production (e.g., managed Redis)
	if cfg.UseTLS {
		opts.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	client := redis.NewClient(opts)

	// Health check
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, err
	}

	closer := func() {
		_ = client.Close()
	}

	return client, closer, nil
}

func defaultDuration(v, d time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return d
}

func defaultInt(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}
