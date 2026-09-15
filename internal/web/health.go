package web

import (
	"net/http"

	"github.com/manuellara/ipam/internal/database"
)

// HealthHandler returns an HTTP handler that checks the health of the database.
func HealthHandler(dbs *database.DBService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := dbs.Ping(r.Context()); err != nil {
			http.Error(w, "db unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}
}
