package shared

import (
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/cmd/views/shared"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
)

// DashboardController handles requests related to the shared dashboard.
type DashboardController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewDashboardController creates a new instance of DashboardController.
func NewDashboardController(store *db.Queries, sessionManager *scs.SessionManager) *DashboardController {
	return &DashboardController{
		store:          store,
		sessionManager: sessionManager,
	}
}

// RegisterDashboardRoutes registers the routes for the shared dashboard.
func (c *DashboardController) RegisterDashboardRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /dashboard", mw(http.HandlerFunc(c.dashboard)))
}

// dashboard handles the HTTP request for the shared dashboard page.
func (c *DashboardController) dashboard(w http.ResponseWriter, r *http.Request) {
	auth, ok := middleware.GetAuthFromContext(r.Context())
	if !ok {
		middleware.GetLoggerFromContext(r.Context()).Error("Unauthorized access to shared dashboard")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	shared.DashboardPage(auth).Render(r.Context(), w)
}
