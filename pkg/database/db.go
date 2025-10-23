package database

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nimeshabuddhika/resilient-payment-processor/pkg/utils"
	"go.uber.org/zap"
)

// Config holds database connection details.
type Config struct {
	PrimaryDSN string
	ReadDSNs   []string // Optional; if empty, use primary for reads. Multiple for balancing.
	MaxConns   int32
	MinConns   int32
}

// DB provides read/write routing.
type DB struct {
	writer  *pgxpool.Pool
	readers []*pgxpool.Pool // Multiple for load balancing; fallback to writer if empty.
}

// New creates a DB with connection pools.
func New(ctx context.Context, logger *zap.Logger, cfg Config) (*DB, func(), error) {
	writer, err := newPool(ctx, logger, cfg.PrimaryDSN, cfg.MaxConns, cfg.MinConns)
	if err != nil {
		return nil, nil, err
	}

	readers := make([]*pgxpool.Pool, 0)
	for _, dsn := range cfg.ReadDSNs {
		if utils.IsEmpty(dsn) {
			continue
		}
		reader, err := newPool(ctx, logger, dsn, cfg.MaxConns, cfg.MinConns)
		if err != nil {
			writer.Close()
			for _, r := range readers {
				r.Close()
			}
			return nil, nil, err
		}
		readers = append(readers, reader)
		logger.Info("PostgreSQL replica pool established")
	}
	if len(readers) == 0 {
		readers = []*pgxpool.Pool{writer}
	}

	// Close all pools on exit.
	closer := func() {
		writer.Close()
		logger.Info("PostgreSQL write connection pool closed")
		for _, reader := range readers {
			if reader != writer {
				reader.Close()
			}
		}
		if len(readers) > 1 || (len(readers) == 1 && readers[0] != writer) {
			logger.Info("PostgreSQL read connection pools closed")
		}
	}
	return &DB{writer: writer, readers: readers}, closer, nil
}

func newPool(ctx context.Context, logger *zap.Logger, dsn string, maxConns, minConns int32) (*pgxpool.Pool, error) {
	dsn = fmt.Sprintf("postgres://%s", dsn)
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	config.MaxConns = maxConns
	config.MinConns = minConns
	config.MaxConnLifetime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	maskedDSN := maskDSN(dsn) // Security: hide passwords
	logger.Debug("postgreSQL_connection_pool_established", zap.String("dsn", maskedDSN))
	return pool, nil
}

// maskDSN hides sensitive parts like passwords.
func maskDSN(dsn string) string {
	parts := strings.Split(dsn, "@")
	if len(parts) > 1 {
		auth := strings.Split(parts[0], "://")
		if len(auth) > 1 {
			userPass := strings.Split(auth[1], ":")
			if len(userPass) > 1 {
				return auth[0] + "://*****:*****@" + parts[1]
			}
		}
	}
	return dsn // Fallback
}

// WithTransaction runs fn in a transaction; auto-commits if no error, rolls back otherwise. Recovers panics.
func (db *DB) WithTransaction(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = db.Rollback(ctx, tx)
			panic(p) // Re-throw
		} else if err != nil {
			_ = db.Rollback(ctx, tx)
		} else {
			err = db.Commit(ctx, tx)
		}
	}()
	err = fn(ctx, tx)
	return err
}

// Query routes to a random reader (replica if available).
func (db *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return db.getReader().Query(ctx, sql, args...)
}

// QueryRow routes to a random reader.
func (db *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return db.getReader().QueryRow(ctx, sql, args...)
}

// Exec routes to writer (primary).
func (db *DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return db.writer.Exec(ctx, sql, args...)
}

// Begin routes to writer for transactions.
func (db *DB) Begin(ctx context.Context) (pgx.Tx, error) {
	return db.writer.Begin(ctx)
}

// Commit commits a transaction.
func (db *DB) Commit(ctx context.Context, tx pgx.Tx) error {
	return tx.Commit(ctx)
}

// Rollback rolls back a transaction.
func (db *DB) Rollback(ctx context.Context, tx pgx.Tx) error {
	return tx.Rollback(ctx)
}

func (db *DB) getReader() *pgxpool.Pool {
	if len(db.readers) == 0 {
		return db.writer
	}
	return db.readers[rand.Intn(len(db.readers))]
}
