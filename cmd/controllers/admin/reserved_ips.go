package admin

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/alexedwards/scs/v2"
	views "github.com/manuellara/ipam/cmd/views/admin"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
	"github.com/manuellara/ipam/internal/subnets"
)

type ReservedIPController struct {
	store          *db.Queries
	sqlDB          *sql.DB
	sessionManager *scs.SessionManager
}

// NewReservedIPController creates a new instance of ReservedIPController with the given dependencies.
func NewReservedIPController(store *db.Queries, sqlDB *sql.DB, sessionManager *scs.SessionManager) *ReservedIPController {
	return &ReservedIPController{
		store:          store,
		sqlDB:          sqlDB,
		sessionManager: sessionManager,
	}
}

// RegisterReservedIPRoutes registers the HTTP routes for managing reserved IPs.
func (c *ReservedIPController) RegisterReservedIPRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /admin/subnets/{id}/reserved-ips", middleware.WithRole(mw, middleware.RoleAdmin, c.list))
	mux.Handle("POST /admin/subnets/{id}/reserved-ips", middleware.WithRole(mw, middleware.RoleAdmin, c.create))
	mux.Handle("POST /admin/subnets/{id}/reserved-ips/{reservedID}/delete", middleware.WithRole(mw, middleware.RoleAdmin, c.delete))
}

// list handles the HTTP GET request to list reserved IPs for a specific subnet.
func (c *ReservedIPController) list(w http.ResponseWriter, r *http.Request) {
	subnetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Warn("invalid subnet id for reserved IP list", "id", r.PathValue("id"), "error", err)
		http.NotFound(w, r)
		return
	}
	c.render(w, r, subnetID, "")
}

func (c *ReservedIPController) render(w http.ResponseWriter, r *http.Request, subnetID int64, formErr string) {
	ctx := r.Context()

	subnet, err := c.store.GetSubnet(ctx, subnetID)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Error("failed to load subnet for reserved IPs", "subnet_id", subnetID, "error", err)
		http.NotFound(w, r)
		return
	}

	rows, err := c.store.ListSubnetReservedIPsForAdmin(ctx, subnetID)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Error("failed to list reserved IPs", "subnet_id", subnetID, "error", err)
		http.Error(w, "failed to load reserved IPs", http.StatusInternalServerError)
		return
	}

	viewRows := make([]views.ReservedIPRow, len(rows))
	for i, row := range rows {
		reason := ""
		if row.Reason != nil {
			reason = *row.Reason
		}
		viewRows[i] = views.ReservedIPRow{
			ID:        row.ID,
			IPAddress: row.IpAddress,
			Reason:    reason,
			CreatedAt: row.CreatedAt,
		}
	}

	principal, _ := middleware.GetAuthFromContext(ctx)
	views.ReservedIPListPage(principal, views.ReservedIPListData{
		SubnetID:   subnetID,
		SubnetCIDR: subnet.Cidr,
		Rows:       viewRows,
		FormError:  formErr,
	}).Render(ctx, w)
}

// create handles the HTTP POST request to create new reserved IPs for a specific subnet.
func (c *ReservedIPController) create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	subnetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Warn("invalid subnet id for reserved IP create", "id", r.PathValue("id"), "error", err)
		http.NotFound(w, r)
		return
	}

	subnet, err := c.store.GetSubnet(ctx, subnetID)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Error("failed to load subnet for reserved IP create", "subnet_id", subnetID, "error", err)
		http.NotFound(w, r)
		return
	}
	prefix, err := netip.ParsePrefix(subnet.Cidr)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Error("invalid stored subnet CIDR for reserved IP create", "subnet_id", subnetID, "cidr", subnet.Cidr, "error", err)
		http.Error(w, "subnet has an invalid CIDR on record", http.StatusInternalServerError)
		return
	}

	startStr := r.FormValue("start_ip")
	endStr := r.FormValue("end_ip")
	if endStr == "" {
		endStr = startStr
	}
	reason := r.FormValue("reason")

	start, err := netip.ParseAddr(startStr)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Warn("invalid reserved IP start address", "subnet_id", subnetID, "start_ip", startStr, "error", err)
		c.render(w, r, subnetID, "start IP is not a valid address")
		return
	}
	end, err := netip.ParseAddr(endStr)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Warn("invalid reserved IP end address", "subnet_id", subnetID, "end_ip", endStr, "error", err)
		c.render(w, r, subnetID, "end IP is not a valid address")
		return
	}

	addrs, err := subnets.ExpandReserveRange(prefix, start, end)
	if err != nil {
		middleware.GetLoggerFromContext(ctx).Warn("invalid reserved IP range", "subnet_id", subnetID, "start_ip", startStr, "end_ip", endStr, "error", err)
		c.render(w, r, subnetID, err.Error())
		return
	}

	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}

	txErr := db.WithTx(ctx, c.sqlDB, func(q *db.Queries) error {
		for _, addr := range addrs {
			if _, err := q.CreateSubnetReservedIP(ctx, db.CreateSubnetReservedIPParams{
				SubnetID:  subnetID,
				IpAddress: addr.String(),
				Reason:    reasonPtr,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	if txErr != nil {
		if isUniqueConstraintErr(txErr) {
			middleware.GetLoggerFromContext(ctx).Warn("reserved IP range already contains reserved addresses", "subnet_id", subnetID, "start_ip", startStr, "end_ip", endStr, "error", txErr)
			c.render(w, r, subnetID, "one or more addresses in that range are already reserved -- no rows were added")
			return
		}
		middleware.GetLoggerFromContext(ctx).Error("failed to create reserved IP range", "subnet_id", subnetID, "start_ip", startStr, "end_ip", endStr, "error", txErr)
		c.render(w, r, subnetID, "failed to save reserved range")
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/subnets/%d/reserved-ips", subnetID), http.StatusSeeOther)
}

// delete handles the HTTP POST request to delete a reserved IP for a specific subnet.
func (c *ReservedIPController) delete(w http.ResponseWriter, r *http.Request) {
	subnetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Warn("invalid subnet id for reserved IP delete", "id", r.PathValue("id"), "error", err)
		http.NotFound(w, r)
		return
	}
	reservedID, err := strconv.ParseInt(r.PathValue("reservedID"), 10, 64)
	if err != nil {
		middleware.GetLoggerFromContext(r.Context()).Warn("invalid reserved IP id for delete", "subnet_id", subnetID, "id", r.PathValue("reservedID"), "error", err)
		http.NotFound(w, r)
		return
	}

	if err := c.store.DeleteSubnetReservedIP(r.Context(), db.DeleteSubnetReservedIPParams{
		ID:       reservedID,
		SubnetID: subnetID,
	}); err != nil {
		middleware.GetLoggerFromContext(r.Context()).Error("failed to delete reserved IP", "subnet_id", subnetID, "reserved_ip_id", reservedID, "error", err)
		http.Error(w, "failed to delete reserved IP", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/subnets/%d/reserved-ips", subnetID), http.StatusSeeOther)
}
