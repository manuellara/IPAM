package requester

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexedwards/scs/v2"
	views "github.com/manuellara/ipam/cmd/views/requester"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
)

type RequestController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewRequestController creates a new instance of RequestController.
func NewRequestController(store *db.Queries, sessionManager *scs.SessionManager) *RequestController {
	return &RequestController{
		store:          store,
		sessionManager: sessionManager,
	}
}

// RegisterRequestRoutes registers the HTTP routes for handling requests.
func (c *RequestController) RegisterRequestRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /requests/new", middleware.WithRole(mw, middleware.RoleRequester, c.newForm))
	mux.Handle("GET /requests/new/fields", middleware.WithRole(mw, middleware.RoleRequester, c.schemeFields))
	mux.Handle("POST /requests", middleware.WithRole(mw, middleware.RoleRequester, c.create))
}

// newForm handles the GET /requests/new route and renders the request form.
func (c *RequestController) newForm(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	schemes, err := c.store.ListNamingSchemes(r.Context())
	if err != nil {
		logger.Error("list naming schemes", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := views.RequestFormData{Schemes: toSchemeOptions(schemes)}
	if len(schemes) > 0 {
		fields, err := c.loadSchemeFields(r.Context(), schemes[0].ID)
		if err != nil {
			logger.Error("load scheme fields", "error", err, "scheme_id", schemes[0].ID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		data.Fields = fields
	}

	principal, _ := middleware.GetAuthFromContext(r.Context())
	views.RequestFormPage(principal, data).Render(r.Context(), w)
}

// schemeFields backs the FRAGMENT GET /requests/new/fields?scheme_id= -- returns the fields for the specified naming scheme.
func (c *RequestController) schemeFields(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	schemeID, err := strconv.ParseInt(r.URL.Query().Get("scheme_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid scheme_id", http.StatusBadRequest)
		return
	}

	fields, err := c.loadSchemeFields(r.Context(), schemeID)
	if err != nil {
		logger.Error("load scheme fields", "error", err, "scheme_id", schemeID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	views.SchemeFields(fields).Render(r.Context(), w)
}

// loadSchemeFields groups the scheme's active token values by token type.
// Reuses ListNamingSchemeTokenValues (IPAM-18) rather than adding
// per-token queries.
// Returns the fields in a format suitable for rendering the request form.
func (c *RequestController) loadSchemeFields(ctx context.Context, schemeID int64) (views.RequestFormFields, error) {
	scheme, err := c.store.GetNamingScheme(ctx, schemeID)
	if err != nil {
		return views.RequestFormFields{}, err
	}

	values, err := c.store.ListNamingSchemeTokenValues(ctx, schemeID)
	if err != nil {
		return views.RequestFormFields{}, err
	}

	fields := views.RequestFormFields{SchemeID: schemeID, NamingMode: scheme.NamingMode}
	for _, v := range values {
		if v.Active == 0 {
			continue
		}
		opt := views.TokenOption{Code: v.Code, Label: v.Label}
		switch v.Token {
		case "site":
			fields.SiteOptions = append(fields.SiteOptions, opt)
		case "env":
			fields.EnvOptions = append(fields.EnvOptions, opt)
		case "app":
			fields.AppOptions = append(fields.AppOptions, opt)
		case "role":
			fields.RoleOptions = append(fields.RoleOptions, opt)
		}
	}
	return fields, nil
}

// create handles the POST /requests route and creates a new request based on the submitted form data.
func (c *RequestController) create(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	schemeID, err := strconv.ParseInt(r.FormValue("scheme_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid scheme_id", http.StatusBadRequest)
		return
	}
	siteCode := r.FormValue("site_code")
	envCode := r.FormValue("env_code")
	appCode := strings.TrimSpace(r.FormValue("app_code"))
	roleCode := strings.TrimSpace(r.FormValue("role_code"))
	manualName := strings.TrimSpace(r.FormValue("manual_name"))

	scheme, err := c.store.GetNamingScheme(r.Context(), schemeID)
	if err != nil {
		http.Error(w, "scheme not found", http.StatusNotFound)
		return
	}

	// Server-side mirror of the trigger-enforced naming-mode fields
	// (trg_requests_naming_mode_insert) -- gives a clean inline error
	// instead of a raw SQL constraint failure, same layering as
	// ValidateTokenCode + the token-length trigger (IPAM-16).
	if scheme.NamingMode == "generated" && (appCode == "" || roleCode == "") {
		c.renderFormError(w, r, schemeID, "app and role are required for this naming scheme")
		return
	}
	if scheme.NamingMode == "manual" && manualName == "" {
		c.renderFormError(w, r, schemeID, "virtual server name is required for this naming scheme")
		return
	}

	subnetID, err := c.store.GetActiveSiteEnvSubnetMap(r.Context(), db.GetActiveSiteEnvSubnetMapParams{
		SiteCode:       siteCode,
		EnvCode:        envCode,
		NamingSchemeID: schemeID,
	})
	if err != nil {
		c.renderFormError(w, r, schemeID, "no subnet is mapped for this site, env, and naming scheme combination -- contact an admin")
		return
	}
	_ = subnetID // resolved again at approval time (IPAM-22); this call is only the submission-time existence check

	principal, _ := middleware.GetAuthFromContext(r.Context())
	params := db.CreateRequestParams{
		RequesterID:    principal.User.ID,
		NamingSchemeID: schemeID,
		SiteCode:       siteCode,
		EnvCode:        envCode,
	}
	if scheme.NamingMode == "generated" {
		params.AppCode = &appCode
		params.RoleCode = &roleCode
	} else {
		params.ManualName = &manualName
	}

	req, err := c.store.CreateRequest(r.Context(), params)
	if err != nil {
		logger.Error("create request", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/requests/"+strconv.FormatInt(req.ID, 10), http.StatusSeeOther)
}

// renderFormError renders the request form with an error message.
func (c *RequestController) renderFormError(w http.ResponseWriter, r *http.Request, schemeID int64, msg string) {

	schemes, err := c.store.ListNamingSchemes(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	fields, err := c.loadSchemeFields(r.Context(), schemeID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	principal, _ := middleware.GetAuthFromContext(r.Context())
	views.RequestFormPage(principal, views.RequestFormData{
		Schemes:   toSchemeOptions(schemes),
		Fields:    fields,
		FormError: msg,
	}).Render(r.Context(), w)
}

// toSchemeOptions converts a list of naming schemes from the database into a list of scheme options suitable for rendering in the request form.
func toSchemeOptions(schemes []db.ListNamingSchemesRow) []views.SchemeOption {
	opts := make([]views.SchemeOption, 0, len(schemes))
	for _, s := range schemes {
		opts = append(opts, views.SchemeOption{ID: s.ID, Name: s.Name})
	}
	return opts
}
