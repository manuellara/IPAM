package middleware

import (
	"context"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/internal/db"
)

const authPrincipalSessionKey = "auth_principal"

type authContextKey struct{}

// SetAuthenticatedPrincipal stores the authenticated user and roles in the session.
func SetAuthenticatedPrincipal(ctx context.Context, sessionManager *scs.SessionManager, user db.User, roles []string) {
	sessionManager.Put(ctx, authPrincipalSessionKey, newPrincipal(user, roles))
}

// AuthMiddleware checks for authentication in the session and injects the Principal into the request context.
func AuthMiddleware(sessionManager *scs.SessionManager) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := sessionManager.Get(r.Context(), authPrincipalSessionKey).(Principal)
			if !ok {
				GetLoggerFromContext(r.Context()).Error("Failed to retrieve authenticated principal from session")

				sessionManager.Put(r.Context(), "post_login_redirect", r.URL.Path)

				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}

			ctx := context.WithValue(r.Context(), authContextKey{}, principal)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAuthFromContext retrieves the authenticated Principal from the request context.
func GetAuthFromContext(ctx context.Context) (Principal, bool) {
	auth, ok := ctx.Value(authContextKey{}).(Principal)

	return auth, ok
}
