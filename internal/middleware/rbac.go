package middleware

import (
	"encoding/gob"
	"net/http"

	"github.com/manuellara/ipam/internal/db"
)

func init() {
	gob.Register(Principal{})
}

type Role string

const (
	RoleAdmin     Role = "admin"
	RoleApprover  Role = "approver"
	RoleRequester Role = "requester"
	RoleViewer    Role = "viewer"
)

type Principal struct {
	User  db.User
	Roles []Role
}

func newPrincipal(user db.User, roleNames []string) Principal {
	roles := make([]Role, len(roleNames))
	for index, roleName := range roleNames {
		roles[index] = Role(roleName)
	}

	return Principal{User: user, Roles: roles}
}

// HasAnyRole checks if the principal has at least one of the specified roles.
func (p Principal) HasAnyRole(roles ...Role) bool {
	for _, requiredRole := range roles {
		for _, assignedRole := range p.Roles {
			if assignedRole == requiredRole {
				return true
			}
		}
	}

	return false
}

// WithRole wraps a handler with the provided middleware stack and role requirement.
func WithRole(stack Middleware, requiredRole Role, handler http.HandlerFunc) http.Handler {
	return stack(RequireAnyRole(requiredRole)(handler))
}

// RequireAnyRole allows a request when the principal has at least one required role.
func RequireAnyRole(roles ...Role) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := GetAuthFromContext(r.Context())
			if !ok {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			if !principal.HasAnyRole(roles...) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
