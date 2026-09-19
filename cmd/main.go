package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/manuellara/ipam/cmd/controllers/shared"
	"github.com/manuellara/ipam/internal/auth"
	"github.com/manuellara/ipam/internal/config"
	"github.com/manuellara/ipam/internal/database"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/logging"
	"github.com/manuellara/ipam/internal/middleware"
	"github.com/manuellara/ipam/internal/session"
	"github.com/manuellara/ipam/internal/web"
)

func main() {
	// Initialize structured logger
	logging.NewSLogger()
	
	// Load environment variables from the .env file if present
	if err := config.Load(""); err != nil {
		slog.Error("environment loading failed", "error", err)
		os.Exit(1)
	}

	// Initialize database service
	databaseService, err := database.New("")
	if err != nil {
		slog.Error("database initialization failed", "error", err)
		return
	}
	defer databaseService.Close()

	// Initialize session manager
	sessionManager := session.New(databaseService.DB())

	// Initialize sqlc service
	sqlcService := db.New(databaseService.DB())

	// Ensure that the local admin user exists and has the correct password.
	adminPassword := os.Getenv("ADMIN_PASSWORD")
	if err := auth.EnsureLocalAdmin(context.Background(), sqlcService, adminPassword); err != nil {
		slog.Error("local admin bootstrap failed", "error", err)
		os.Exit(1)
	}

	// Initialize middleware stacks
	publicMiddleware := middleware.MiddlewareStack(
		// Middleware execution order: top to bottom
		sessionManager.LoadAndSave,
		middleware.LoggingMiddleware,
		middleware.CsrfMiddleware,
	)

	authenticatedMiddleware := middleware.MiddlewareStack(
		// Middleware execution order: top to bottom
		sessionManager.LoadAndSave,
		middleware.LoggingMiddleware,
		middleware.CsrfMiddleware,
		middleware.AuthMiddleware(sessionManager),
	)

	// Initialize controllers
	loginController := shared.NewLoginController(sqlcService, sessionManager)

	// Initialize the HTTP request multiplexer
	mux := http.NewServeMux()

	// Serve static files from the ./static directory
	fs := http.FileServer(http.Dir("./static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Health check endpoint (no session, no CSRF, no RBAC)
	mux.Handle("/healthz", web.HealthHandler(databaseService))

	// Register routes with their respective handlers and middleware
	loginController.RegisterLoginRoutes(mux, publicMiddleware, authenticatedMiddleware)

	// Run the HTTP server with session management
	if err := http.ListenAndServe(":8080", middleware.RecoverMiddleware(mux)); err != nil {
		slog.Error(fmt.Sprintf("HTTP server stopped: %v", err))

		os.Exit(1)
	}
}
