package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadReadsDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	if err := os.WriteFile(path, []byte("ADMIN_PASSWORD=from_test_value\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}

	oldValue, hadOldValue := os.LookupEnv("ADMIN_PASSWORD")
	if err := os.Unsetenv("ADMIN_PASSWORD"); err != nil {
		t.Fatalf("unset env var: %v", err)
	}
	defer func() {
		if hadOldValue {
			_ = os.Setenv("ADMIN_PASSWORD", oldValue)
		} else {
			_ = os.Unsetenv("ADMIN_PASSWORD")
		}
	}()

	if err := Load(path); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if got := os.Getenv("ADMIN_PASSWORD"); got != "from_test_value" {
		t.Fatalf("ADMIN_PASSWORD = %q, want %q", got, "from_test_value")
	}
}
