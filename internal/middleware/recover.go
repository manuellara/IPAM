package middleware

import (
	"net/http"

	"log/slog"
)

// RecoverMiddleware is a middleware that recovers from panics in HTTP handlers.
// It logs the panic and returns a 500 Internal Server Error response.
func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered", "error", err, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}