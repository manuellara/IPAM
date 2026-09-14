package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type mwLoggerKey struct{}

// statusRecorder is a wrapper around http.ResponseWriter that captures the HTTP status code of the response.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code and writes the header to the underlying ResponseWriter.
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// LoggingMiddleware is a middleware that logs the details of each HTTP request and its response status.
// It also measures the duration of the request processing.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		logger := slog.Default().With(
			slog.String("request_id", uuid.NewString()),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("uri", r.URL.RequestURI()),
		)
		r = r.WithContext(AddLoggerToContext(r.Context(), logger))

		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)

		logger.Info("request completed",
			slog.Int("status", rec.status),
			slog.Duration("duration", time.Duration(time.Since(start).Milliseconds())), // Log the duration in milliseconds
		)
	})
}

// AddLoggerToContext adds a logger to the context and returns the updated context.
func AddLoggerToContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, mwLoggerKey{}, l)
}

// GetLoggerFromContext retrieves the logger from the context. If no logger is found, it returns the default logger.
// To print an error message in the log, you can use the logger retrieved from the context to log the error.
// For example: GetLoggerFromContext(ctx).Error("an error occurred", slog.String("error", err.Error()))
// GetLoggerFromContext.Debug("debug message", slog.String("key", "value")) or
func GetLoggerFromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(mwLoggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
