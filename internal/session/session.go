// Package session configures application sessions backed by SQLite.
package session

import (
	"database/sql"
	"net/http"
	"time"

	"github.com/alexedwards/scs/sqlite3store"
	"github.com/alexedwards/scs/v2"
)

const lifetime = 12 * time.Hour

// New returns an SCS manager that persists sessions in the application's database.
func New(database *sql.DB) *scs.SessionManager {
	manager := scs.New()
	manager.Store = sqlite3store.New(database)
	manager.Lifetime = lifetime
	manager.IdleTimeout = 30 * time.Minute
	manager.Cookie.Name = "ipam_session"
	manager.Cookie.HttpOnly = true
	manager.Cookie.Secure = true
	manager.Cookie.SameSite = http.SameSiteLaxMode
	manager.Cookie.Path = "/"

	return manager
}
