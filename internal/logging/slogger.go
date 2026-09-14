package logging

import (
	"log/slog"
	"os"
)

var logLevel = new(slog.LevelVar)

// NewSLogger initializes the logger with JSON formatting and debug level.
func NewSLogger() {
	jLogger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: true,
	}))

	slog.SetDefault(jLogger)

	slog.Info("Logger initialized with JSON formatting and debug level")
}

// SetLogLevel sets the logging level for the logger.
// Valid levels are "debug", "info", "warn", and "error".
// If an invalid level is provided, it defaults to "info".
func SetLogLevel(level string) {
	switch level {
	case "debug":
		logLevel.Set(slog.LevelDebug)
		slog.Debug("Log level set to debug")
	case "info":
		logLevel.Set(slog.LevelInfo)
		slog.Info("Log level set to info")
	case "warn":
		logLevel.Set(slog.LevelWarn)
		slog.Warn("Log level set to warn")
	case "error":
		logLevel.Set(slog.LevelError)
		slog.Error("Log level set to error")
	default:
		logLevel.Set(slog.LevelInfo)
		slog.Info("Log level set to info (default)")
	}
}
