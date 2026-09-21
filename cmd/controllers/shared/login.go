package shared

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"strings"

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
	auditActionOIDCLoginSuccess  = "auth.oidc_login_success"
	auditActionOIDCLoginFailure  = "auth.oidc_login_failure"
	auditActionLDAPLoginSuccess  = "auth.ldap_login_success"
	auditActionLDAPLoginFailure  = "auth.ldap_login_failure"
	auditActionLogout            = "auth.logout"
	oidcStateSessionKey          = "oidc_state"
	oidcNonceSessionKey          = "oidc_nonce"
)

// LoginController handles staff login functionality, including rendering the login page and processing login requests.
type LoginController struct {
	store          *db.Queries
	sqlDB          *sql.DB
	sessionManager *scs.SessionManager
}

// NewLoginController creates a new instance of LoginController with the provided database service and session manager.
func NewLoginController(store *db.Queries, sqlDB *sql.DB, sessionManager *scs.SessionManager) *LoginController {
	return &LoginController{
		store:          store,
		sqlDB:          sqlDB,
		sessionManager: sessionManager,
	}
}

// RegisterLoginRoutes registers the login routes with the provided HTTP multiplexer and middleware.
func (c *LoginController) RegisterLoginRoutes(mux *http.ServeMux, pmw middleware.Middleware, mw middleware.Middleware) {
	if pmw == nil {
		pmw = func(next http.Handler) http.Handler { return next }
	}
	if mw == nil {
		mw = func(next http.Handler) http.Handler { return next }
	}
	mux.Handle("GET /login", pmw(http.HandlerFunc(c.login)))
	mux.Handle("POST /login", pmw(http.HandlerFunc(c.handleLoginPost)))
	mux.Handle("POST /logout", mw(http.HandlerFunc(c.logout)))
	mux.Handle("GET /auth/oidc/login", pmw(http.HandlerFunc(c.oidcLogin)))
	mux.Handle("GET /auth/oidc/callback", pmw(http.HandlerFunc(c.oidcCallback)))
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

// strPtrOrNil returns a pointer to s, or nil if s is empty.
func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

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

// handleLDAPLoginPost authenticates against the configured LDAP/AD server
// via search-then-bind, provisions a user on first successful login, and
// establishes the normal IPAM session.
func (c *LoginController) handleLDAPLoginPost(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	cfg, err := c.store.GetLDAPConfig(r.Context())
	if err != nil || !auth.ConfigEnabled(cfg.Enabled) || cfg.Server == nil || cfg.Port == nil || cfg.BaseDn == nil || cfg.BindDn == nil || cfg.BindPassword == nil || cfg.UserFilter == nil {
		c.auditLogin(r.Context(), nil, auditActionLDAPLoginFailure, "ldap not configured")
		c.renderLoginPage(w, r, "LDAP sign-in is not configured.")
		return
	}

	ldapUser, err := auth.AuthenticateLDAP(*cfg.Server, *cfg.Port, *cfg.BaseDn, *cfg.BindDn, *cfg.BindPassword, *cfg.UserFilter, username, password, cfg.CaCert)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Warn("ldap authentication failed", "error", err)
		c.auditLogin(r.Context(), nil, auditActionLDAPLoginFailure, "authentication failed")
		c.renderLoginPage(w, r, "Invalid username or password.")
		return
	}

	user, err := c.store.GetLDAPUser(r.Context(), &ldapUser.DN)
	if err == sql.ErrNoRows {
		displayName := ldapUser.CN
		if displayName == "" {
			displayName = username
		}
		var email *string
		if ldapUser.Email != "" {
			email = &ldapUser.Email
		}

		txErr := db.WithTx(r.Context(), c.sqlDB, func(q *db.Queries) error {
			var txErr error
			user, txErr = q.CreateLDAPUser(r.Context(), db.CreateLDAPUserParams{
				DisplayName: displayName, Email: email, LdapDn: &ldapUser.DN,
			})
			if txErr != nil {
				return txErr
			}
			return q.AssignViewerRole(r.Context(), user.ID)
		})
		err = txErr
	}
	if err != nil {
		c.auditLogin(r.Context(), nil, auditActionLDAPLoginFailure, "user provisioning failed")
		http.Error(w, "Unable to provision LDAP user", http.StatusInternalServerError)
		return
	}

	roles, err := c.store.GetUserRoleNames(r.Context(), user.ID)
	if err != nil {
		c.auditLogin(r.Context(), &user.ID, auditActionLDAPLoginFailure, "role lookup failed")
		http.Error(w, "Unable to establish session", http.StatusInternalServerError)
		return
	}
	if err := c.sessionManager.RenewToken(r.Context()); err != nil {
		c.auditLogin(r.Context(), &user.ID, auditActionLDAPLoginFailure, "session renew failed")
		http.Error(w, "Unable to establish session", http.StatusInternalServerError)
		return
	}
	middleware.SetAuthenticatedPrincipal(r.Context(), c.sessionManager, user, roles)
	c.auditLogin(r.Context(), &user.ID, auditActionLDAPLoginSuccess, "")
	c.redirectAfterLogin(w, r)
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

// oidcLogin starts the authorization-code flow with the configured provider.
func (c *LoginController) oidcLogin(w http.ResponseWriter, r *http.Request) {
	cfg, err := c.store.GetOIDCConfig(r.Context())
	if err != nil || !auth.ConfigEnabled(cfg.Enabled) || cfg.IssuerUrl == nil || cfg.ClientID == nil || cfg.ClientSecret == nil || cfg.RedirectUrl == nil {
		http.Error(w, "OIDC sign-in is not configured", http.StatusNotFound)
		return
	}

	oidcClient, err := auth.NewOIDCClient(r.Context(), *cfg.IssuerUrl, *cfg.ClientID, *cfg.ClientSecret, *cfg.RedirectUrl)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("oidc provider discovery failed", "error", err)
		http.Error(w, "Unable to connect to the OIDC provider", http.StatusBadGateway)
		return
	}

	state, err := auth.RandomOIDCString()
	if err != nil {
		http.Error(w, "Unable to start OIDC sign-in", http.StatusInternalServerError)
		return
	}
	nonce, err := auth.RandomOIDCString()
	if err != nil {
		http.Error(w, "Unable to start OIDC sign-in", http.StatusInternalServerError)
		return
	}
	c.sessionManager.Put(r.Context(), oidcStateSessionKey, state)
	c.sessionManager.Put(r.Context(), oidcNonceSessionKey, nonce)

	http.Redirect(w, r, oidcClient.AuthorizationURL(state, nonce), http.StatusFound)
}

// oidcCallback completes the authorization-code flow, provisions the user,
// and establishes the normal IPAM session.
func (c *LoginController) oidcCallback(w http.ResponseWriter, r *http.Request) {
	expectedState := c.sessionManager.PopString(r.Context(), oidcStateSessionKey)
	if expectedState == "" || subtle.ConstantTimeCompare([]byte(expectedState), []byte(r.URL.Query().Get("state"))) != 1 {
		c.auditLogin(r.Context(), nil, auditActionOIDCLoginFailure, "invalid state")
		http.Error(w, "Invalid OIDC state", http.StatusBadRequest)
		return
	}
	nonce := c.sessionManager.PopString(r.Context(), oidcNonceSessionKey)
	if r.URL.Query().Get("error") != "" {
		c.auditLogin(r.Context(), nil, auditActionOIDCLoginFailure, "sign-in cancelled")
		c.renderLoginPage(w, r, "OIDC sign-in was cancelled.")
		return
	}

	cfg, err := c.store.GetOIDCConfig(r.Context())
	if err != nil || !auth.ConfigEnabled(cfg.Enabled) || cfg.IssuerUrl == nil || cfg.ClientID == nil || cfg.ClientSecret == nil || cfg.RedirectUrl == nil {
		http.Error(w, "OIDC sign-in is not configured", http.StatusNotFound)
		return
	}
	oidcClient, err := auth.NewOIDCClient(r.Context(), *cfg.IssuerUrl, *cfg.ClientID, *cfg.ClientSecret, *cfg.RedirectUrl)
	if err != nil {
		http.Error(w, "Unable to connect to the OIDC provider", http.StatusBadGateway)
		return
	}
	claims, err := oidcClient.VerifyCode(r.Context(), r.URL.Query().Get("code"), nonce)
	if err != nil {
		c.auditLogin(r.Context(), nil, auditActionOIDCLoginFailure, "authentication failed")
		http.Error(w, "OIDC authentication failed", http.StatusUnauthorized)
		return
	}

	user, err := c.store.GetOIDCUser(r.Context(), &claims.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		displayName := strings.TrimSpace(claims.Name)
		if displayName == "" {
			displayName = strings.TrimSpace(claims.PreferredUsername)
		}
		if displayName == "" {
			displayName = claims.Subject
		}
		var email *string
		if claims.Email != "" {
			email = &claims.Email
		}
		// Create the OIDC user and assign the viewer role within a transaction to ensure atomicity.
		txErr := db.WithTx(r.Context(), c.sqlDB, func(q *db.Queries) error {
			var txErr error
			user, txErr = q.CreateOIDCUser(r.Context(), db.CreateOIDCUserParams{
				DisplayName: displayName, Email: email, OidcSubject: &claims.Subject,
			})
			if txErr != nil {
				return txErr
			}
			return q.AssignViewerRole(r.Context(), user.ID)
		})
		err = txErr
	}
	if err != nil {
		c.auditLogin(r.Context(), nil, auditActionOIDCLoginFailure, "provisioning failed")
		http.Error(w, "Unable to provision OIDC user", http.StatusInternalServerError)
		return
	}
	roles, err := c.store.GetUserRoleNames(r.Context(), user.ID)
	if err != nil {
		c.auditLogin(r.Context(), &user.ID, auditActionOIDCLoginFailure, "unable to establish session")
		http.Error(w, "Unable to establish session", http.StatusInternalServerError)
		return
	}
	if err := c.sessionManager.RenewToken(r.Context()); err != nil {
		c.auditLogin(r.Context(), &user.ID, auditActionOIDCLoginFailure, "unable to establish session")
		http.Error(w, "Unable to establish session", http.StatusInternalServerError)
		return
	}
	c.auditLogin(r.Context(), &user.ID, auditActionOIDCLoginSuccess, "login successful")
	middleware.SetAuthenticatedPrincipal(r.Context(), c.sessionManager, user, roles)
	c.redirectAfterLogin(w, r)
}

// auditLogin creates an audit log entry for login-related actions.
func (c *LoginController) auditLogin(ctx context.Context, userID *int64, action, detail string) {
	params := db.CreateAuditLogParams{
		ActorUserID: userID,
		Action:      action,
		TargetType:  strPtrOrNil("user"),
	}
	if detail != "" {
		params.Detail = strPtrOrNil(detail)
	}
	if err := c.store.CreateAuditLog(ctx, params); err != nil {
		middleware.GetLoggerFromContext(ctx).Error("audit log creation failed", "error", err)
	}
}