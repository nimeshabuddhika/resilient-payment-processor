package database

import (
	"embed"
	"errors"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrations embed.FS

// RunMigrations applies schema migrations using primary writer.
func RunMigrations(logger *zap.Logger, primaryDSN string) error {
	d, err := iofs.New(migrations, "migrations")
	if err != nil {
		return err
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, "pgx5://"+primaryDSN)
	if err != nil {
		return err
	}
	defer func(m *migrate.Migrate) {
		_, _ = m.Close()
	}(m)

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	logger.Info("Database migrations applied")
	return nil
}
