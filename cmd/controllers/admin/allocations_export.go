package admin

import (
	"encoding/csv"
	"net/http"
	"strconv"

	"github.com/alexedwards/scs/v2"
	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/middleware"
)

type AllocationsExportController struct {
	store          *db.Queries
	sessionManager *scs.SessionManager
}

// NewAllocationsExportController creates a new instance of AllocationsExportController with the given dependencies.
func NewAllocationsExportController(store *db.Queries, sessionManager *scs.SessionManager) *AllocationsExportController {
	return &AllocationsExportController{
		store:          store,
		sessionManager: sessionManager}
}

// RegisterAllocationsExportRoutes registers the HTTP routes for exporting IP allocations.
func (c *AllocationsExportController) RegisterAllocationsExportRoutes(mux *http.ServeMux, mw middleware.Middleware) {
	mux.Handle("GET /admin/allocations/export.csv", middleware.WithRole(mw, middleware.RoleAdmin, c.export))
}

// export handles the HTTP request to export IP allocations as a CSV file.
func (c *AllocationsExportController) export(w http.ResponseWriter, r *http.Request) {
	logger := middleware.GetLoggerFromContext(r.Context())

	type row struct {
		SubnetCIDR  string
		IPAddress   string
		Hostname    *string
		Source      *string
		Requester   *string
		AllocatedAt string
	}

	var rows []row

	if subnetIDStr := r.URL.Query().Get("subnet_id"); subnetIDStr != "" {
		subnetID, err := strconv.ParseInt(subnetIDStr, 10, 64)
		if err != nil {
			logger.Warn("invalid subnet id for allocations export", "subnet_id", subnetIDStr, "error", err)
			http.Error(w, "invalid subnet_id", http.StatusBadRequest)
			return
		}
		dbRows, err := c.store.ListActiveAllocationsForSubnetExport(r.Context(), subnetID)
		if err != nil {
			logger.Error("failed to load subnet allocations for export", "subnet_id", subnetID, "error", err)
			http.Error(w, "failed to load allocations", http.StatusInternalServerError)
			return
		}
		for _, dr := range dbRows {
			rows = append(rows, row{
				SubnetCIDR:  dr.SubnetCidr,
				IPAddress:   dr.IpAddress,
				Hostname:    dr.Hostname,
				Source:      dr.Source,
				Requester:   dr.Requester,
				AllocatedAt: dr.AllocatedAt,
			})
		}
	} else {
		dbRows, err := c.store.ListActiveAllocationsForExport(r.Context())
		if err != nil {
			logger.Error("failed to load allocations for export", "error", err)
			http.Error(w, "failed to load allocations", http.StatusInternalServerError)
			return
		}
		for _, dr := range dbRows {
			rows = append(rows, row{
				SubnetCIDR:  dr.SubnetCidr,
				IPAddress:   dr.IpAddress,
				Hostname:    dr.Hostname,
				Source:      dr.Source,
				Requester:   dr.Requester,
				AllocatedAt: dr.AllocatedAt,
			})
		}
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="ip_allocations.csv"`)

	cw := csv.NewWriter(w)

	if err := cw.Write([]string{"Subnet", "IP Address", "Hostname", "Source", "Requester", "Allocated At", "Status"}); err != nil {
		logger.Error("failed to write allocations export header", "error", err)
		return
	}
	for _, row := range rows {
		hostname, source, requester := "", "", ""
		if row.Hostname != nil {
			hostname = *row.Hostname
		}
		if row.Source != nil {
			source = *row.Source
		}
		if row.Requester != nil {
			requester = *row.Requester
		}
		if err := cw.Write([]string{
			row.SubnetCIDR,
			row.IPAddress,
			hostname,
			source,
			requester,
			row.AllocatedAt,
			"Active", // constant for now -- only active rows are ever exported (see design decision: no include_released toggle in v1)
		}); err != nil {
			logger.Error("failed to write allocations export row", "ip_address", row.IPAddress, "error", err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		logger.Error("failed to flush allocations export", "error", err)
	}
}
