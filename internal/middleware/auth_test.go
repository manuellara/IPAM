package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/internal/db"
)

func TestNewPrincipalBuildsRoleSlice(t *testing.T) {
	principal := newPrincipal(
		db.User{ID: 1, DisplayName: "Current Name", Active: 1},
		[]string{"admin", "viewer"},
	)

	if principal.User.DisplayName != "Current Name" {
		t.Fatalf("display name = %q, want %q", principal.User.DisplayName, "Current Name")
	}
	if !principal.HasAnyRole(RoleAdmin) || !principal.HasAnyRole(RoleViewer) {
		t.Fatal("principal does not contain the supplied roles")
	}
}

func TestWithRoleWrapsHandlerWithStackAndRole(t *testing.T) {
	stackCalled := false
	stack := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			stackCalled = true
			principal := newPrincipal(db.User{ID: 1}, []string{"requester"})
			ctx := context.WithValue(r.Context(), authContextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	handlerCalled := false
	handler := WithRole(stack, RoleRequester, func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusNoContent)
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/requests", nil))

	if !stackCalled {
		t.Fatal("middleware stack was not called")
	}
	if !handlerCalled {
		t.Fatal("handler was not called")
	}
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAuthMiddlewareRestoresCachedPrincipal(t *testing.T) {
	sessionManager := scs.New()
	login := sessionManager.LoadAndSave(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetAuthenticatedPrincipal(
			r.Context(),
			sessionManager,
			db.User{ID: 1, DisplayName: "Current Name", Active: 1},
			[]string{"viewer"},
		)
		w.WriteHeader(http.StatusNoContent)
	}))

	loginResponse := httptest.NewRecorder()
	login.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodPost, "/login", nil))

	protected := sessionManager.LoadAndSave(AuthMiddleware(sessionManager)(
		RequireAnyRole(RoleViewer)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := GetAuthFromContext(r.Context())
			if !ok {
				t.Fatal("principal missing from request context")
			}
			if principal.User.ID != 1 {
				t.Fatalf("user ID = %d, want 1", principal.User.ID)
			}
			w.WriteHeader(http.StatusNoContent)
		})),
	))

	request := httptest.NewRequest(http.MethodGet, "/requests", nil)
	request.AddCookie(loginResponse.Result().Cookies()[0])
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestRequireAnyRole(t *testing.T) {
	tests := []struct {
		name          string
		assignedRoles []Role
		requiredRoles []Role
		wantStatus    int
	}{
		{
			name:          "allows any matching role",
			assignedRoles: []Role{RoleViewer},
			requiredRoles: []Role{RoleRequester, RoleViewer},
			wantStatus:    http.StatusNoContent,
		},
		{
			name:          "roles are not hierarchical",
			assignedRoles: []Role{RoleAdmin},
			requiredRoles: []Role{RoleRequester},
			wantStatus:    http.StatusForbidden,
		},
		{
			name:          "rejects a missing principal",
			requiredRoles: []Role{RoleViewer},
			wantStatus:    http.StatusUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := RequireAnyRole(test.requiredRoles...)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.assignedRoles != nil {
				request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, Principal{Roles: test.assignedRoles}))
			}

			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}
