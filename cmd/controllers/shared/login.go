package shared

import (
	"net/http"

	"github.com/alexedwards/argon2id"
	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/cmd/views/shared"
	"github.com/manuellara/ipam/internal/auth"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
)

const (
	auditActionLocalLoginSuccess = "auth.local_login_success"
	auditActionLocalLoginFailure = "auth.local_login_failure"
	auditActionLogout            = "auth.logout"
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
	mux.Handle("GET /login", pmw(http.HandlerFunc(c.login)))
	mux.Handle("POST /login", pmw(http.HandlerFunc(c.handleLoginPost)))
	mux.Handle("POST /logout", mw(http.HandlerFunc(c.logout)))
}

// Login handles the HTTP request for the login page.
func (c *LoginController) login(w http.ResponseWriter, r *http.Request) {
	c.renderLoginPage(w, r, "")
}

// renderLoginPage renders the login page with the provided error message and configuration flags.
func (c *LoginController) renderLoginPage(w http.ResponseWriter, r *http.Request, errMsg string) {
	oidcCfg, err := c.store.GetOIDCConfig(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("oidc config lookup failed", "error", err)
	}
	ldapCfg, err := c.store.GetLDAPConfig(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("ldap config lookup failed", "error", err)
	}

	shared.LoginPage(errMsg, auth.ConfigEnabled(oidcCfg.Enabled), auth.ConfigEnabled(ldapCfg.Enabled)).Render(r.Context(), w)
}

// HandleLoginPost processes submitted local admin credentials.
func (c *LoginController) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	switch r.FormValue("method") {
	case "ldap":
		c.handleLDAPLoginPost(w, r)
	default:
		c.handleLocalLoginPost(w, r)
	}
}

// strPtr returns a pointer to the given string. Useful for optional string fields in database operations.
func strPtr(s string) *string { return &s }

// handleLocalLoginPost handles the login process for the local administrator account. It verifies the submitted password, manages the session, and logs audit events.
func (c *LoginController) handleLocalLoginPost(w http.ResponseWriter, r *http.Request) {
	password := r.FormValue("password")

	admin, err := c.store.GetLocalAdminUser(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("local admin lookup failed during login", "error", err)
		err = c.store.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
			Action: auditActionLocalLoginFailure,
			Detail: strPtr("lookup failed"),
		})
		if err != nil {
			middleware.GetLoggerFromContext(r.Context()).Error("audit log creation failed", "error", err)
		}
		c.renderLoginPage(w, r, "Invalid username or password.")
		return
	}

	if admin.PasswordHash == nil {
		err = c.store.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
			ActorUserID: &admin.ID,
			Action:      auditActionLocalLoginFailure,
			Detail:      strPtr("no password set"),
		})
		if err != nil {
			middleware.GetLoggerFromContext(r.Context()).Error("audit log creation failed", "error", err)
		}
		c.renderLoginPage(w, r, "Invalid username or password.")
		return
	}

	match, err := argon2id.ComparePasswordAndHash(password, *admin.PasswordHash)
	if err != nil || !match {
		err = c.store.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
			ActorUserID: &admin.ID,
			Action:      auditActionLocalLoginFailure,
			Detail:      strPtr("password mismatch"),
		})
		if err != nil {
			middleware.GetLoggerFromContext(r.Context()).Error("audit log creation failed", "error", err)
		}
		c.renderLoginPage(w, r, "Invalid username or password.")
		return
	}

	if err := c.sessionManager.RenewToken(r.Context()); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("session renew failed", "error", err)
		c.renderLoginPage(w, r, "Something went wrong. Please try again.")
		return
	}

	roleNames, err := c.store.GetUserRoleNames(r.Context(), admin.ID)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to load user roles", "error", err)
		c.renderLoginPage(w, r, "Something went wrong. Please try again.")
		return
	}
	middleware.SetAuthenticatedPrincipal(r.Context(), c.sessionManager, admin, roleNames)

	err = c.store.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		ActorUserID: &admin.ID,
		Action:      auditActionLocalLoginSuccess,
	})
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("audit log creation failed", "error", err)
	}

	// Redirect the user to the page they originally tried to access, or "/" if none was stored.
	c.redirectAfterLogin(w, r)
}

// handleLDAPLoginPost is a placeholder until IPAM-30 lands. The LDAP form
// only renders when ldap_config.enabled = 1, which is false by default,
// so this path shouldn't be reachable yet in normal use.
func (c *LoginController) handleLDAPLoginPost(w http.ResponseWriter, r *http.Request) {
	c.renderLoginPage(w, r, "LDAP login is not yet available.")
}

// Logout destroys the session.
func (c *LoginController) logout(w http.ResponseWriter, r *http.Request) {
	if userID := c.sessionManager.GetInt64(r.Context(), "userID"); userID != 0 {
		if err := c.store.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
			ActorUserID: &userID,
			Action:      auditActionLogout,
		}); err != nil {
			middleware.GetLoggerFromContext(r.Context()).Error("audit log creation failed", "error", err)
		}
	}

	if err := c.sessionManager.Destroy(r.Context()); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("session destroy failed", "error", err)
	}

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// redirectAfterLogin sends the user to wherever they originally tried to
// go before being bounced to /login, or "/" if there's nothing stored.
func (c *LoginController) redirectAfterLogin(w http.ResponseWriter, r *http.Request) {
	redirectTo := middleware.SafeRedirectPath(
		c.sessionManager.PopString(r.Context(), middleware.PostLoginRedirectSessionKey),
	)
	http.Redirect(w, r, redirectTo, http.StatusSeeOther)
}
