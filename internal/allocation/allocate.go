package allocation

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/manuellara/ipam/internal/db"
	"github.com/manuellara/ipam/internal/subnets"
)

// Allocate finds and reserves the next free IP in subnetID, inserting
// the ip_allocations row. q must already be scoped to an in-progress
// write transaction (via db.WithTx) -- this function is a single step
// meant to compose with a naming-sequence increment and an audit log
// write in the same transaction, not to run standalone.
func Allocate(ctx context.Context, q *db.Queries, subnetID int64, requestID, serverID *int64) (db.IpAllocation, error) {
	subnet, err := q.GetSubnet(ctx, subnetID)
	if err != nil {
		return db.IpAllocation{}, fmt.Errorf("load subnet: %w", err)
	}
	if subnet.Active == 0 {
		return db.IpAllocation{}, subnets.ErrSubnetInactive
	}

	prefix, err := subnets.ParseCIDR(subnet.Cidr)
	if err != nil {
		return db.IpAllocation{}, fmt.Errorf("parse subnet cidr: %w", err)
	}

	reserved, err := parseAddrs(q.ListReservedIPsForSubnet(ctx, subnetID))
	if err != nil {
		return db.IpAllocation{}, fmt.Errorf("load reserved ips: %w", err)
	}

	allocated, err := parseAddrs(q.ListActiveAllocatedIPsForSubnet(ctx, subnetID))
	if err != nil {
		return db.IpAllocation{}, fmt.Errorf("load active allocations: %w", err)
	}

	next, err := subnets.NextFreeIP(prefix, reserved, allocated)
	if err != nil {
		return db.IpAllocation{}, err // subnets.ErrSubnetExhausted
	}

	row, err := q.CreateIPAllocation(ctx, db.CreateIPAllocationParams{
		SubnetID:  subnetID,
		IpAddress: next.String(),
		RequestID: requestID,
		ServerID:  serverID,
	})
	if err != nil {
		return db.IpAllocation{}, fmt.Errorf("insert ip_allocation: %w", err)
	}
	return row, nil
}

// parseAddrs converts a list of IP address strings into netip.Addr values.
// It returns an error if any of the strings are not valid IP addresses.
func parseAddrs(ipStrings []string, err error) ([]netip.Addr, error) {
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(ipStrings))
	for _, s := range ipStrings {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("stored ip %q is invalid: %w", s, err)
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}