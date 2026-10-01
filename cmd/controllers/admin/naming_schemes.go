package admin

import (
	"net/http"
	"strconv"

	"github.com/alexedwards/scs/v2"
	views "github.com/manuellara/ipam/cmd/views/admin"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
	"github.com/manuellara/ipam/internal/naming"
)

type NamingSchemeController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewNamingSchemeController creates a new instance of NamingSchemeController.
func NewNamingSchemeController(store *db.Queries, sessionManager *scs.SessionManager) *NamingSchemeController {
	return &NamingSchemeController{store: store, sessionManager: sessionManager}
}

// RegisterNamingSchemeRoutes registers the HTTP routes for managing naming schemes.
func (c *NamingSchemeController) RegisterNamingSchemeRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /admin/naming-schemes", middleware.WithRole(mw, middleware.RoleAdmin, c.list))
	mux.Handle("GET /admin/naming-schemes/{id}", middleware.WithRole(mw, middleware.RoleAdmin, c.detail))
	mux.Handle("POST /admin/naming-schemes/{id}/tokens", middleware.WithRole(mw, middleware.RoleAdmin, c.createToken))
	mux.Handle("POST /admin/naming-schemes/{id}/tokens/{tokenID}/deactivate", middleware.WithRole(mw, middleware.RoleAdmin, c.deactivateToken))
}

// list handles the HTTP request for listing all naming schemes.
func (c *NamingSchemeController) list(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	schemes, err := c.store.ListNamingSchemes(r.Context())
	if err != nil {
		logger.Error("list naming schemes", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	rows := make([]views.NamingSchemeRow, 0, len(schemes))
	for _, s := range schemes {
		rows = append(rows, views.NamingSchemeRow{
			ID:         s.ID,
			Name:       s.Name,
			NamingMode: s.NamingMode,
		})
	}

	principal, _ := middleware.GetAuthFromContext(r.Context())
	views.NamingSchemeListPage(principal, views.NamingSchemeListData{Rows: rows}).Render(r.Context(), w)
}

func (c *NamingSchemeController) detail(w http.ResponseWriter, r *http.Request) {
	c.render(w, r, "")
}

// render loads the scheme + its token values and renders the detail page,
// with formErr set when called after a failed create (invalid length or
// duplicate code) so the form re-renders inline instead of redirecting.
func (c *NamingSchemeController) render(w http.ResponseWriter, r *http.Request, formErr string) {
	logger := middleware.GetLoggerFromContext(r.Context())

	schemeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid scheme id", http.StatusBadRequest)
		return
	}

	scheme, err := c.store.GetNamingScheme(r.Context(), schemeID)
	if err != nil {
		logger.Error("get naming scheme", "error", err, "scheme_id", schemeID)
		http.Error(w, "scheme not found", http.StatusNotFound)
		return
	}

	values, err := c.store.ListNamingSchemeTokenValues(r.Context(), schemeID)
	if err != nil {
		logger.Error("list naming scheme token values", "error", err, "scheme_id", schemeID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := views.NamingSchemeDetailData{
		SchemeID:   scheme.ID,
		SchemeName: scheme.Name,
		NamingMode: scheme.NamingMode,
		FormError:  formErr,
	}
	if scheme.Template != nil {
		data.Template = *scheme.Template
	}

	for _, v := range values {
		row := views.TokenValueRow{ID: v.ID, Code: v.Code, Label: v.Label, Active: v.Active != 0}
		switch v.Token {
		case "site":
			data.SiteValues = append(data.SiteValues, row)
		case "env":
			data.EnvValues = append(data.EnvValues, row)
		case "app":
			data.AppValues = append(data.AppValues, row)
		case "role":
			data.RoleValues = append(data.RoleValues, row)
		}
	}

	principal, _ := middleware.GetAuthFromContext(r.Context())
	views.NamingSchemeDetailPage(principal, data).Render(r.Context(), w)
}

// NewNamingSchemeController creates a new instance of NamingSchemeController.
func (c *NamingSchemeController) createToken(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	schemeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid scheme id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	token := r.FormValue("token")
	code := r.FormValue("code")
	label := r.FormValue("label")

	scheme, err := c.store.GetNamingScheme(r.Context(), schemeID)
	if err != nil {
		logger.Error("get naming scheme", "error", err, "scheme_id", schemeID)
		http.Error(w, "scheme not found", http.StatusNotFound)
		return
	}

	if err := naming.ValidateTokenCode(code, int(scheme.TokenLength)); err != nil {
		c.render(w, r, err.Error())
		return
	}

	_, err = c.store.CreateNamingSchemeTokenValue(r.Context(), db.CreateNamingSchemeTokenValueParams{
		SchemeID: schemeID,
		Token:    token,
		Code:     code,
		Label:    label,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			c.render(w, r, "that code already exists for this token on this scheme")
			return
		}
		logger.Error("create naming scheme token value", "error", err, "scheme_id", schemeID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/naming-schemes/"+strconv.FormatInt(schemeID, 10), http.StatusSeeOther)
}

// deactivateToken handles the deactivation of a token value for a naming scheme.
func (c *NamingSchemeController) deactivateToken(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	schemeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid scheme id", http.StatusBadRequest)
		return
	}
	tokenID, err := strconv.ParseInt(r.PathValue("tokenID"), 10, 64)
	if err != nil {
		http.Error(w, "invalid token id", http.StatusBadRequest)
		return
	}

	err = c.store.DeactivateNamingSchemeTokenValue(r.Context(), db.DeactivateNamingSchemeTokenValueParams{
		ID:       tokenID,
		SchemeID: schemeID,
	})
	if err != nil {
		logger.Error("deactivate naming scheme token value", "error", err, "scheme_id", schemeID, "token_id", tokenID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/naming-schemes/"+strconv.FormatInt(schemeID, 10), http.StatusSeeOther)
}
