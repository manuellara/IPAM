package database

import (
	"path/filepath"
	"testing"
)

func TestNewInitializesDatabase(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "ipam.db")
	service, err := New(databasePath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer service.Close()

	var version int
	if err := service.DB().QueryRow("SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("schema_migrations query error = %v", err)
	}
	if version != 1 {
		t.Fatalf("migration version = %d, want 1", version)
	}

	var foreignKeys int
	if err := service.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("foreign_keys query error = %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}

	var journalMode string
	if err := service.DB().QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode query error = %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want %q", journalMode, "wal")
	}
	if maxOpen := service.DB().Stats().MaxOpenConnections; maxOpen != 5 {
		t.Fatalf("max open connections = %d, want 5", maxOpen)
	}
}

func TestNewUsesDefaultPath(t *testing.T) {
	t.Chdir(t.TempDir())

	service, err := New("")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := service.DB().Ping(); err != nil {
		t.Fatalf("database ping error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
