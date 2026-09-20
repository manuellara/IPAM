package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/manuellara/ipam/cmd/views/admin"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
)

// AuthSettingsController handles the authentication settings page.
type AuthSettingsController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewAuthSettingsController creates a new AuthSettingsController.
func NewAuthSettingsController(store *db.Queries, sessionManager *scs.SessionManager) *AuthSettingsController {
	return &AuthSettingsController{
		store:          store,
		sessionManager: sessionManager,
	}
}

// RegisterAuthSettingsRoutes registers the admin authentication settings route.
func (c *AuthSettingsController) RegisterAuthSettingsRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /admin/auth-settings", middleware.WithRole(mw, middleware.RoleAdmin, c.authSettings))
	mux.Handle("POST /admin/auth-settings/oidc", middleware.WithRole(mw, middleware.RoleAdmin, c.updateOIDC))
	mux.Handle("POST /admin/auth-settings/ldap", middleware.WithRole(mw, middleware.RoleAdmin, c.updateLDAP))
}

// authSettings renders the authentication settings page.
func (c *AuthSettingsController) authSettings(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	oidcConfig, err := c.store.GetOIDCConfig(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("oidc config lookup failed", "error", err)
		http.Error(w, "Unable to load authentication settings", http.StatusInternalServerError)
		return
	}

	ldapConfig, err := c.store.GetLDAPConfig(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("ldap config lookup failed", "error", err)
		http.Error(w, "Unable to load authentication settings", http.StatusInternalServerError)
		return
	}

	admin.AuthSettingsPage(principal, oidcConfig, ldapConfig).Render(r.Context(), w)
}

// updateOIDC handles the POST request to update the OIDC authentication settings.
func (c *AuthSettingsController) updateOIDC(w http.ResponseWriter, r *http.Request) {
	issuerURL := strings.TrimSpace(r.FormValue("issuer_url"))
	if checkboxToInt64(r.FormValue("enabled")) != 0 && issuerURL == "" {
		http.Error(w, "OIDC issuer URL is required when OIDC is enabled", http.StatusBadRequest)
		return
	}
	if issuerURL != "" {
		if err := validateOIDCIssuer(r.Context(), issuerURL); err != nil {
			middleware.GetLoggerFromContext(r.Context()).Warn("oidc issuer discovery failed", "issuer_url", issuerURL, "error", err)
			http.Error(w, "OIDC issuer URL could not be resolved", http.StatusBadRequest)
			return
		}
	}

	err := c.updateOIDCConfig(r, issuerURL)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("auth settings update failed", "error", err)
		http.Error(w, "Unable to save authentication settings", http.StatusInternalServerError)
		return
	}
	c.auditAuthSettingsUpdate(r, "auth_settings.oidc_updated")

	http.Redirect(w, r, "/admin/auth-settings", http.StatusSeeOther)
}

// updateLDAP handles the POST request to update the LDAP authentication settings.
func (c *AuthSettingsController) updateLDAP(w http.ResponseWriter, r *http.Request) {
	enabled := checkboxToInt64(r.FormValue("enabled")) != 0
	portStr := strings.TrimSpace(r.FormValue("port"))

	var port *int64
	if portStr != "" {
		p, err := parseLDAPPort(portStr)
		if err != nil {
			http.Error(w, "LDAP port must be between 1 and 65535", http.StatusBadRequest)
			return
		}
		port = &p
	} else if enabled {
		http.Error(w, "LDAP port is required when LDAP is enabled", http.StatusBadRequest)
		return
	}

	if err := c.updateLDAPConfig(r, port); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("auth settings update failed", "error", err)
		http.Error(w, "Unable to save authentication settings", http.StatusInternalServerError)
		return
	}
	c.auditAuthSettingsUpdate(r, "auth_settings.ldap_updated")

	http.Redirect(w, r, "/admin/auth-settings", http.StatusSeeOther)
}

// auditAuthSettingsUpdate creates an audit log entry for changes to the authentication settings.
func (c *AuthSettingsController) auditAuthSettingsUpdate(r *http.Request, action string) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	if err := c.store.CreateAuditLog(r.Context(), db.CreateAuditLogParams{
		ActorUserID: &principal.User.ID,
		Action:      action,
		TargetType:  stringPointer("auth_settings"),
		TargetID:    stringPointer("1"),
	}); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("auth settings audit log creation failed", "action", action, "error", err)
	}
}

// updateOIDCConfig updates the OIDC configuration in the database.
func (c *AuthSettingsController) updateOIDCConfig(r *http.Request, issuerURL string) error {
	return c.store.UpdateOIDCConfig(r.Context(), db.UpdateOIDCConfigParams{
		Enabled:      checkboxToInt64(r.FormValue("enabled")),
		IssuerUrl:    stringPointer(issuerURL),
		ClientID:     stringPointer(r.FormValue("client_id")),
		ClientSecret: r.FormValue("client_secret"),
		RedirectUrl:  stringPointer(r.FormValue("redirect_url")),
	})
}

// validateOIDCIssuer validates the OIDC issuer URL by attempting to create a new OIDC provider.
func validateOIDCIssuer(parent context.Context, issuerURL string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()

	_, err := oidc.NewProvider(ctx, issuerURL)
	return err
}

// updateLDAPConfig updates the LDAP configuration in the database.
func (c *AuthSettingsController) updateLDAPConfig(r *http.Request, port *int64) error {
	return c.store.UpdateLDAPConfig(r.Context(), db.UpdateLDAPConfigParams{
		Enabled:      checkboxToInt64(r.FormValue("enabled")),
		Server:       stringPointer(r.FormValue("server")),
		Port:         port,
		BaseDn:       stringPointer(r.FormValue("base_dn")),
		BindDn:       stringPointer(r.FormValue("bind_dn")),
		BindPassword: r.FormValue("bind_password"),
		UserFilter:   stringPointer(r.FormValue("user_filter")),
	})
}

// parseLDAPPort parses the LDAP port from a string and ensures it is within the valid range (1-65535).
func parseLDAPPort(value string) (int64, error) {
	port, err := strconv.ParseInt(value, 10, 64)
	if err != nil || port < 1 || port > 65535 {
		return 0, strconv.ErrRange
	}
	return port, nil
}

// checkboxToInt64 converts a string value to an int64 representing whether the configuration is enabled (1) or disabled (0).
func checkboxToInt64(value string) int64 {
	if value == "1" {
		return 1
	}
	return 0
}

// stringPointer returns a pointer to the given string value, or nil if the string is empty.
func stringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
