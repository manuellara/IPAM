package subnets

import (
	"errors"
	"net/netip"
)

// MaxReserveRangeSize caps a single reserve-range submission so a
// fat-fingered CIDR can't silently insert thousands of rows. The work
// item asks for "a small range" -- this is the enforced definition of
// small.
const MaxReserveRangeSize = 256

var (
	ErrReserveRangeInverted  = errors.New("range end must be >= start")
	ErrReserveRangeTooLarge  = errors.New("range exceeds maximum size (256 addresses)")
	ErrReserveOutsideSubnet  = errors.New("address is outside the subnet")
	ErrReserveFamilyMismatch = errors.New("start and end must be the same address family")
)

// ExpandReserveRange validates start/end against prefix and returns the
// individual addresses to insert as subnet_reserved_ips rows. end may
// equal start (single-IP reservation).
func ExpandReserveRange(prefix netip.Prefix, start, end netip.Addr) ([]netip.Addr, error) {
	if start.Is4() != end.Is4() {
		return nil, ErrReserveFamilyMismatch
	}
	if !prefix.Contains(start) || !prefix.Contains(end) {
		return nil, ErrReserveOutsideSubnet
	}
	if start.Compare(end) > 0 {
		return nil, ErrReserveRangeInverted
	}

	addrs := []netip.Addr{start}
	cur := start
	for cur != end {
		cur = cur.Next()
		addrs = append(addrs, cur)
		if len(addrs) > MaxReserveRangeSize {
			return nil, ErrReserveRangeTooLarge
		}
	}
	return addrs, nil
}