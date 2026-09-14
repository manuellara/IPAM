package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	_ "github.com/mattn/go-sqlite3"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"

	"github.com/manuellara/ipam/migrations"
)

const defaultPath = "ipam.db"

// DBService owns the application's SQLite connection and schema migrations.
type DBService struct {
	db *sql.DB
}

// New opens a SQLite database, enables foreign keys, and applies embedded
// migrations before returning a ready-to-use service.
func New(path string) (*DBService, error) {
	if path == "" {
		path = defaultPath
	}

	db, err := sql.Open("sqlite3", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)

	service := &DBService{db: db}
	if err := service.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return service, nil
}

// DB returns the underlying database handle for queries and transactions.
func (s *DBService) DB() *sql.DB {
	return s.db
}

// Close releases the database connection.
func (s *DBService) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// initialize sets up the database by enabling foreign keys, pinging the database,
// and applying any pending migrations. It returns an error if any of these steps fail.
func (s *DBService) initialize(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}

	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	driver, err := migratesqlite3.WithInstance(s.db, &migratesqlite3.Config{})
	if err != nil {
		return fmt.Errorf("create migration driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", source, "sqlite3", driver)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}

	if err := migrator.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// sqliteDSN constructs the Data Source Name for the SQLite database, ensuring
// foreign keys are enabled and setting a busy timeout.
func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	if path == ":memory:" {
		path = "file::memory:?cache=shared"
		separator = "&"
	}
	return path + separator + "_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000"
}
