package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/cmd/views/admin"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
	"github.com/manuellara/ipam/internal/subnets"
	"github.com/mattn/go-sqlite3"
)

type SiteEnvMapController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewSiteEnvMapController creates a new instance of SiteEnvMapController with the given store and session manager.
func NewSiteEnvMapController(store *db.Queries, sessionManager *scs.SessionManager) *SiteEnvMapController {
	return &SiteEnvMapController{
		store: store, 
		sessionManager: sessionManager,
	}
}

// RegisterSiteEnvMapRoutes registers the HTTP routes for managing site-env-subnet mappings.
func (c *SiteEnvMapController) RegisterSiteEnvMapRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /admin/site-env-map", middleware.WithRole(mw, middleware.RoleAdmin, c.list))
	mux.Handle("GET /admin/site-env-map/new", middleware.WithRole(mw, middleware.RoleAdmin, c.newForm))
	mux.Handle("POST /admin/site-env-map", middleware.WithRole(mw, middleware.RoleAdmin, c.create))
	mux.Handle("GET /admin/site-env-map/{id}/edit", middleware.WithRole(mw, middleware.RoleAdmin, c.editForm))
	mux.Handle("POST /admin/site-env-map/{id}", middleware.WithRole(mw, middleware.RoleAdmin, c.update))
}

// list handles the HTTP request for listing all site-env-subnet mappings.
func (c *SiteEnvMapController) list(w http.ResponseWriter, r *http.Request) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	rows, err := c.store.ListSiteEnvSubnetMapWithDetails(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("site+env map list failed", "error", err)
		http.Error(w, "Unable to load mappings", http.StatusInternalServerError)
		return
	}

	viewRows := make([]admin.SiteEnvMapRow, 0, len(rows))
	for _, row := range rows {
	label := ""
	if row.SubnetLabel != nil {
		label = *row.SubnetLabel
	}

	free := 0
	if prefix, err := subnets.ParseCIDR(row.SubnetCidr); err == nil {
		util := subnets.ComputeUtilization(prefix, int(row.ReservedCount), int(row.UsedCount))
		free = util.Free
	} else {
		middleware.GetLoggerFromContext(r.Context()).Warn("site+env map: unparseable subnet CIDR", "cidr", row.SubnetCidr, "error", err)
	}

	viewRows = append(viewRows, admin.SiteEnvMapRow{
		ID:          row.ID,
		SchemeName:  row.SchemeName,
		SiteCode:    row.SiteCode,
		EnvCode:     row.EnvCode,
		SubnetCIDR:  row.SubnetCidr,
		SubnetLabel: label,
		Active:      row.Active != 0,
		Free:        free,
	})
}

	admin.SiteEnvMapListPage(principal, viewRows).Render(r.Context(), w)
}

// newForm handles the HTTP request for displaying the form to create a new site-env-subnet mapping.
func (c *SiteEnvMapController) newForm(w http.ResponseWriter, r *http.Request) {
	c.renderNewForm(w, r, "")
}

// renderNewForm renders the form for creating a new site-env-subnet mapping, including any error message.
func (c *SiteEnvMapController) renderNewForm(w http.ResponseWriter, r *http.Request, errMsg string) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	schemes, subnets, tokensJSON, err := c.formOptions(r)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("site+env map form options failed", "error", err)
		http.Error(w, "Unable to load form", http.StatusInternalServerError)
		return
	}

	data := admin.SiteEnvMapFormData{Active: true, ErrMsg: errMsg}
	admin.SiteEnvMapFormPage(principal, data, schemes, subnets, tokensJSON).Render(r.Context(), w)
}

// editForm handles the HTTP request for displaying the form to edit an existing site-env-subnet mapping.
func (c *SiteEnvMapController) editForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	c.renderEditForm(w, r, id, "")
}

// renderEditForm renders the form for editing an existing site-env-subnet mapping, including any error message.
func (c *SiteEnvMapController) renderEditForm(w http.ResponseWriter, r *http.Request, id int64, errMsg string) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	m, err := c.store.GetSiteEnvSubnetMap(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		middleware.GetLoggerFromContext(r.Context()).Error("site+env map lookup failed", "error", err)
		http.Error(w, "Unable to load mapping", http.StatusInternalServerError)
		return
	}

	schemes, subnets, tokensJSON, err := c.formOptions(r)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("site+env map form options failed", "error", err)
		http.Error(w, "Unable to load form", http.StatusInternalServerError)
		return
	}

	data := admin.SiteEnvMapFormData{
		ID:             m.ID,
		IsEdit:         true,
		NamingSchemeID: m.NamingSchemeID,
		SiteCode:       m.SiteCode,
		EnvCode:        m.EnvCode,
		SubnetID:       m.SubnetID,
		Active:         m.Active != 0,
		ErrMsg:         errMsg,
	}
	admin.SiteEnvMapFormPage(principal, data, schemes, subnets, tokensJSON).Render(r.Context(), w)
}

// create handles the HTTP request for creating a new site-env-subnet mapping.
func (c *SiteEnvMapController) create(w http.ResponseWriter, r *http.Request) {
	siteCode, envCode, schemeID, subnetID, _, err := parseSiteEnvMapForm(r)
	if err != nil {
		c.renderNewForm(w, r, err.Error())
		return
	}

	_, err = c.store.CreateSiteEnvSubnetMap(r.Context(), db.CreateSiteEnvSubnetMapParams{
		SiteCode:       siteCode,
		EnvCode:        envCode,
		NamingSchemeID: schemeID,
		SubnetID:       subnetID,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			c.renderNewForm(w, r, "A mapping for this scheme + site + env already exists.")
			return
		}
		middleware.GetLoggerFromContext(r.Context()).Error("site+env map create failed", "error", err)
		http.Error(w, "Unable to save mapping", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/site-env-map", http.StatusSeeOther)
}

// update handles the HTTP request for updating an existing site-env-subnet mapping.
func (c *SiteEnvMapController) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	siteCode, envCode, schemeID, subnetID, active, err := parseSiteEnvMapForm(r)
	if err != nil {
		c.renderEditForm(w, r, id, err.Error())
		return
	}

	err = c.store.UpdateSiteEnvSubnetMap(r.Context(), db.UpdateSiteEnvSubnetMapParams{
		SiteCode:       siteCode,
		EnvCode:        envCode,
		NamingSchemeID: schemeID,
		SubnetID:       subnetID,
		Active:         boolToInt64(active),
		ID:             id,
	})
	if err != nil {
		if isUniqueConstraintErr(err) {
			c.renderEditForm(w, r, id, "A mapping for this scheme + site + env already exists.")
			return
		}
		middleware.GetLoggerFromContext(r.Context()).Error("site+env map update failed", "error", err)
		http.Error(w, "Unable to save mapping", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/site-env-map", http.StatusSeeOther)
}

// formOptions gathers the dropdown/embedded-JSON data shared by both
// the new and edit forms, including naming schemes, active subnets, and site/env token values.
func (c *SiteEnvMapController) formOptions(r *http.Request) ([]admin.SchemeOption, []admin.SubnetOption, string, error) {
	schemeRows, err := c.store.ListNamingSchemes(r.Context())
	if err != nil {
		return nil, nil, "", err
	}
	schemes := make([]admin.SchemeOption, 0, len(schemeRows))
	for _, s := range schemeRows {
		schemes = append(schemes, admin.SchemeOption{ID: s.ID, Name: s.Name})
	}

	subnetRows, err := c.store.ListActiveSubnets(r.Context())
	if err != nil {
		return nil, nil, "", err
	}
	subnets := make([]admin.SubnetOption, 0, len(subnetRows))
	for _, s := range subnetRows {
		subnets = append(subnets, admin.SubnetOption{ID: s.ID, Label: s.Cidr})
	}

	tokenRows, err := c.store.ListActiveSiteEnvTokenValues(r.Context())
	if err != nil {
		return nil, nil, "", err
	}
	tokensJSON, err := admin.SchemeTokensJSON(tokenRows)
	if err != nil {
		return nil, nil, "", err
	}

	return schemes, subnets, tokensJSON, nil
}

// parseSiteEnvMapForm parses the form submission for creating or updating a site-env-subnet mapping.
func parseSiteEnvMapForm(r *http.Request) (siteCode, envCode string, schemeID, subnetID int64, active bool, err error) {
	if err := r.ParseForm(); err != nil {
		return "", "", 0, 0, false, errors.New("invalid form submission")
	}

	siteCode = strings.TrimSpace(r.FormValue("site_code"))
	envCode = strings.TrimSpace(r.FormValue("env_code"))
	if siteCode == "" || envCode == "" {
		return "", "", 0, 0, false, errors.New("Site and Env are required")
	}

	schemeID, err = strconv.ParseInt(r.FormValue("naming_scheme_id"), 10, 64)
	if err != nil {
		return "", "", 0, 0, false, errors.New("A naming scheme is required")
	}

	subnetID, err = strconv.ParseInt(r.FormValue("subnet_id"), 10, 64)
	if err != nil {
		return "", "", 0, 0, false, errors.New("A subnet is required")
	}

	active = r.FormValue("active") == "1"

	return siteCode, envCode, schemeID, subnetID, active, nil
}

// boolToInt64 converts a boolean value to its corresponding int64 representation (1 for true, 0 for false).
func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// isUniqueConstraintErr reports whether err is a SQLite UNIQUE
// constraint violation -- used to turn the site_env_subnet_map
// (site_code, env_code, naming_scheme_id) index into a friendly
// inline form error instead of a raw 500.
// It specifically checks for the UNIQUE constraint violation on the site_env_subnet_map table.
func isUniqueConstraintErr(err error) bool {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrConstraint
	}
	return false
}
