package admin

import (
	"net/http"
	"strconv"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/cmd/views/admin"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
	"github.com/manuellara/ipam/internal/subnets"
)

// SubnetsController handles the admin subnet pages.
type SubnetsController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewSubnetsController creates a new SubnetsController.
func NewSubnetsController(store *db.Queries, sessionManager *scs.SessionManager) *SubnetsController {
	return &SubnetsController{
		store:          store,
		sessionManager: sessionManager,
	}
}

// RegisterSubnetsRoutes registers the admin subnet routes.
func (c *SubnetsController) RegisterSubnetsRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /admin/subnets", mw(middleware.RequireAnyRole(middleware.RoleAdmin, middleware.RoleViewer)(http.HandlerFunc(c.list))))
	mux.Handle("GET /admin/subnets/new", middleware.WithRole(mw, middleware.RoleAdmin, c.newForm))
	mux.Handle("POST /admin/subnets", middleware.WithRole(mw, middleware.RoleAdmin, c.create))
	mux.Handle("GET /admin/subnets/{id}/edit", middleware.WithRole(mw, middleware.RoleAdmin, c.editForm))
	mux.Handle("POST /admin/subnets/{id}", middleware.WithRole(mw, middleware.RoleAdmin, c.update))
}

// list handles the rendering of the admin subnet list page. It retrieves the list of subnets from the database,
// computes their utilization, and passes the data to the view for rendering. Unauthorized users receive a 401 response.
func (c *SubnetsController) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	principal, ok := middleware.GetAuthFromContext(ctx)
	if !ok {
		middleware.GetLoggerFromContext(ctx).Error("unauthorized access to subnet list")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	rows, err := c.store.ListSubnetsWithCounts(ctx)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Error("failed to list subnets", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	subnetRows := make([]admin.SubnetRow, 0, len(rows))
	for _, row := range rows {
		prefix, err := subnets.ParseCIDR(row.Cidr)
		if err != nil {
			middleware.GetLoggerFromContext(ctx).Error("subnet has invalid stored CIDR", "id", row.ID, "cidr", row.Cidr, "error", err)
			continue
		}

		label := ""
		if row.Label != nil {
			label = *row.Label
		}

		util := subnets.ComputeUtilization(prefix, int(row.ReservedCount), int(row.UsedCount))
		subnetRows = append(subnetRows, admin.SubnetRow{
			ID:       row.ID,
			CIDR:     row.Cidr,
			Label:    label,
			Active:   row.Active == 1,
			SiteEnvs: row.SiteEnvs,
			Util:     util,
		})
	}

	admin.SubnetsListPage(principal, subnetRows).Render(ctx, w)
}

// newForm handles the rendering of the new subnet form page. It retrieves the authenticated principal from the context
// and passes it to the view for rendering. Unauthorized users receive a 401 response.
func (c *SubnetsController) newForm(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.GetAuthFromContext(r.Context())
	if !ok {
		middleware.GetLoggerFromContext(r.Context()).Error("unauthorized access to new subnet form")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	admin.SubnetFormPage(principal, admin.SubnetFormData{IsEdit: false, Active: true}).Render(r.Context(), w)
}

// editForm handles the rendering of the edit subnet form page. It retrieves the authenticated principal from the context
// and the subnet details from the store, then passes them to the view for rendering. Unauthorized users receive a 401 response.
func (c *SubnetsController) editForm(w http.ResponseWriter, r *http.Request) {
	principal, ok := middleware.GetAuthFromContext(r.Context())
	if !ok {
		middleware.GetLoggerFromContext(r.Context()).Error("unauthorized access to edit subnet form")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid subnet id", http.StatusBadRequest)
		return
	}

	subnet, err := c.store.GetSubnet(r.Context(), id)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to load subnet", "id", id, "error", err)
		http.Error(w, "subnet not found", http.StatusNotFound)
		return
	}

	allocCount, err := c.store.CountActiveAllocationsForSubnet(r.Context(), id)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to count allocations", "id", id, "error", err)
	}

	label := ""
	if subnet.Label != nil {
		label = *subnet.Label
	}

	c.renderEditForm(w, r, principal, admin.SubnetFormData{
		ID:                    subnet.ID,
		IsEdit:                true,
		CIDR:                  subnet.Cidr,
		Label:                 label,
		Active:                subnet.Active == 1,
		ActiveAllocationCount: allocCount,
	})
}

// create handles the creation of a new subnet. It validates the input, checks for conflicts with existing subnets,
// and inserts the new subnet into the store. On success, it redirects to the subnets list page; on failure, it renders the form with an error.
func (c *SubnetsController) create(w http.ResponseWriter, r *http.Request) {
	cidr := r.FormValue("cidr")
	label := r.FormValue("label")

	existing, err := c.store.ListActiveSubnets(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to list active subnets", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	existingSubnets := make([]subnets.ExistingSubnet, len(existing))
	for i, s := range existing {
		existingSubnets[i] = subnets.ExistingSubnet{ID: s.ID, CIDR: s.Cidr}
	}

	prefix, err := subnets.ValidateSubnet(cidr, existingSubnets, nil)
	if err != nil {
		c.renderNewFormWithError(w, r, cidr, label, err.Error())
		return
	}

	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}
	if _, err := c.store.CreateSubnet(r.Context(), db.CreateSubnetParams{
		Cidr:  prefix.String(),
		Label: labelPtr,
	}); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to create subnet", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/subnets", http.StatusSeeOther)
}

// update handles the updating of an existing subnet. It validates the input, checks for conflicts with other subnets,
// and updates the subnet in the store. On success, it redirects to the subnets list page; on failure, it renders the form with an error.
func (c *SubnetsController) update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid subnet id", http.StatusBadRequest)
		return
	}

	cidr := r.FormValue("cidr")
	label := r.FormValue("label")
	active := r.FormValue("active") == "1"

	existing, err := c.store.ListActiveSubnets(r.Context())
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to list active subnets", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	existingSubnets := make([]subnets.ExistingSubnet, len(existing))
	for i, s := range existing {
		existingSubnets[i] = subnets.ExistingSubnet{ID: s.ID, CIDR: s.Cidr}
	}

	prefix, err := subnets.ValidateSubnet(cidr, existingSubnets, &id)
	if err != nil {
		c.renderEditFormWithError(w, r, id, cidr, label, active, err.Error())
		return
	}

	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}
	activeInt := int64(0)
	if active {
		activeInt = 1
	}
	if err := c.store.UpdateSubnet(r.Context(), db.UpdateSubnetParams{
		ID:     id,
		Cidr:   prefix.String(),
		Label:  labelPtr,
		Active: activeInt,
	}); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to update subnet", "id", id, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/admin/subnets", http.StatusSeeOther)
}

// renderNewFormWithError renders the new subnet form with an error message. It retrieves the authenticated principal from the context
// and passes it along with the form data to the view for rendering.
func (c *SubnetsController) renderNewFormWithError(w http.ResponseWriter, r *http.Request, cidr, label, errMsg string) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	admin.SubnetFormPage(principal, admin.SubnetFormData{
		CIDR:   cidr,
		Label:  label,
		Active: true,
		ErrMsg: errMsg,
	}).Render(r.Context(), w)
}

// renderEditForm renders the edit subnet form with the provided data. It retrieves the authenticated principal from the context
// and passes it along with the form data to the view for rendering.
func (c *SubnetsController) renderEditFormWithError(w http.ResponseWriter, r *http.Request, id int64, cidr, label string, active bool, errMsg string) {
	principal, _ := middleware.GetAuthFromContext(r.Context())

	allocCount, err := c.store.CountActiveAllocationsForSubnet(r.Context(), id)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to count allocations", "id", id, "error", err)
	}

	c.renderEditForm(w, r, principal, admin.SubnetFormData{
		ID:                    id,
		IsEdit:                true,
		CIDR:                  cidr,
		Label:                 label,
		Active:                active,
		ActiveAllocationCount: allocCount,
		ErrMsg:                errMsg,
	})
}

// renderEditForm renders the edit subnet form with the provided data. It retrieves the authenticated principal from the context
// and passes it along with the form data to the view for rendering.
func (c *SubnetsController) renderEditForm(w http.ResponseWriter, r *http.Request, principal middleware.Principal, data admin.SubnetFormData) {
	admin.SubnetFormPage(principal, data).Render(r.Context(), w)
}
