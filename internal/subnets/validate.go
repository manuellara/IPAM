package subnets

import (
	"fmt"
	"math"
	"net/netip"
)

// ExistingSubnet is the minimal shape needed to check a candidate CIDR
// against subnets already in the database. Callers must only pass ACTIVE
// (active=1) subnets here -- inactive subnets are deliberately excluded
// from overlap validation (see subnets.active semantics).
type ExistingSubnet struct {
	ID   int64
	CIDR string
}

// SubnetUtilization holds computed capacity/usage numbers for a subnet,
// derived from its CIDR prefix length plus counts pulled from the DB.
type SubnetUtilization struct {
	Capacity int
	Used     int
	Reserved int
	Free     int
}

// ParseCIDR parses and normalizes a CIDR string, masking off any host
// bits -- "10.20.4.5/24" becomes "10.20.4.0/24". This matches common
// IPAM tooling behavior (auto-correct a technically-valid but
// non-network-aligned input) rather than rejecting it outright.
func ParseCIDR(cidr string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	return p.Masked(), nil
}

// ValidateNoOverlap checks that candidate doesn't overlap any subnet in
// existing. excludeID, if non-nil, skips that subnet's own row from the
// comparison -- required when validating an edit to an existing
// subnet's CIDR, so a subnet never overlaps itself.
//
// CIDR blocks never partially overlap: any two are either identical, one
// fully contains the other, or fully disjoint. netip.Prefix.Overlaps is
// a complete check -- there's no separate partial-overlap case.
func ValidateNoOverlap(candidate netip.Prefix, existing []ExistingSubnet, excludeID *int64) error {
	for _, s := range existing {
		if excludeID != nil && s.ID == *excludeID {
			continue
		}
		existingPrefix, err := ParseCIDR(s.CIDR)
		if err != nil {
			// A malformed row already in the DB shouldn't silently pass
			// validation as "no conflict" -- surface it loudly instead.
			return fmt.Errorf("existing subnet id=%d has invalid stored CIDR %q: %w", s.ID, s.CIDR, err)
		}
		if candidate.Overlaps(existingPrefix) {
			return fmt.Errorf("overlaps existing subnet %s (id=%d)", existingPrefix, s.ID)
		}
	}
	return nil
}

// ValidateSubnet parses candidateCIDR, normalizes it, and checks it for
// overlap against existing (active) subnets, excluding excludeID's own
// row if set. Returns the normalized prefix to store on success -- store
// prefix.String(), not the raw input, so the DB always holds the
// canonical network address.
func ValidateSubnet(candidateCIDR string, existing []ExistingSubnet, excludeID *int64) (netip.Prefix, error) {
	candidate, err := ParseCIDR(candidateCIDR)
	if err != nil {
		return netip.Prefix{}, err
	}
	if err := ValidateNoOverlap(candidate, existing, excludeID); err != nil {
		return netip.Prefix{}, err
	}
	return candidate, nil
}

// ComputeUtilization derives usable capacity from a subnet's prefix
// length (total addresses minus network + broadcast), then free space
// from the DB-supplied reserved and used counts.
//
// /31 naturally computes to 0 capacity (2 total - 2 = 0), no special
// case needed. /32 would go negative (1 total - 2 = -1) and is clamped
// to 0 -- neither case is expected in real use (this tool provisions
// servers/VMs/VIPs, not point-to-point router links).
func ComputeUtilization(prefix netip.Prefix, reservedCount, usedCount int) SubnetUtilization {
	bits := prefix.Addr().BitLen() // 32 for IPv4, 128 for IPv6
	hostBits := bits - prefix.Bits()

	var totalAddresses int64
	if hostBits >= 63 {
		totalAddresses = math.MaxInt64
	} else {
		totalAddresses = int64(1) << uint(hostBits)
	}

	capacity := totalAddresses - 2 - int64(reservedCount)
	if capacity < 0 {
		capacity = 0
	}

	free := capacity - int64(usedCount)
	if free < 0 {
		free = 0
	}

	return SubnetUtilization{
		Capacity: int(capacity),
		Used:     usedCount,
		Reserved: reservedCount,
		Free:     int(free),
	}
}
