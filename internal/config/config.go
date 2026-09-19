package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/joho/godotenv"
)

// Load loads environment variables from a .env file if present.
// It prefers existing process environment values and only fills missing ones.
func Load(path string) error {
	if path == "" {
		path = filepath.Join(".", ".env")
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("check env file: %w", err)
	}

	if err := godotenv.Load(path); err != nil {
		return fmt.Errorf("load env file %q: %w", path, err)
	}
	return nil
}
