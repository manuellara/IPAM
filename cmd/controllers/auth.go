package controllers

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
)

// LoginController handles staff login functionality, including rendering the login page and processing login requests.
type LoginController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewLoginController creates a new instance of LoginController with the provided database service and session manager.
func NewLoginController(store *db.Queries, sessionManager *scs.SessionManager) *LoginController {
	return &LoginController{
		store:          store,
		sessionManager: sessionManager,
	}
}

// RegisterLoginRoutes registers the login routes with the provided HTTP multiplexer and middleware.
func (c *LoginController) RegisterLoginRoutes(mux *http.ServeMux, pmw middleware.Middleware, mw middleware.Middleware) {
	mux.Handle("/login", pmw(http.HandlerFunc(c.Login)))
	mux.Handle("/logout", mw(http.HandlerFunc(c.Logout)))
}

// Login handles the HTTP request for the login page.
func (c *LoginController) Login(w http.ResponseWriter, r *http.Request) {
	// Implement login logic here
}

// Logout handles the HTTP request for logging out.
func (c *LoginController) Logout(w http.ResponseWriter, r *http.Request) {
	// Implement logout logic here
}
