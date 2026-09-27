package subnets

import (
	"errors"
	"net/netip"
)

// ErrSubnetInactive indicates the subnet has been soft-retired
// (active = 0). Distinct from ErrSubnetExhausted -- same "no
// allocation happened" symptom, different cause, different admin fix.
var ErrSubnetInactive = errors.New("subnet is not active")

// ErrSubnetExhausted indicates every address in the subnet's usable
// host range is either reserved or already allocated.
var ErrSubnetExhausted = errors.New("subnet has no free addresses")

// NextFreeIP returns the first available address in prefix's usable
// host range (excluding network and broadcast), skipping any address
// present in reserved or allocated. Iteration is in natural address
// order, so a subnet always fills from the bottom up.
//
// /31 and /32 (or /127, /128) always return ErrSubnetExhausted --
// consistent with ComputeUtilization's capacity floor at 0 for these,
// not a special case bolted on separately.
func NextFreeIP(prefix netip.Prefix, reserved, allocated []netip.Addr) (netip.Addr, error) {
	base := prefix.Masked()
	hostBits := base.Addr().BitLen() - base.Bits()

	if hostBits < 2 {
		return netip.Addr{}, ErrSubnetExhausted
	}

	skip := make(map[netip.Addr]struct{}, len(reserved)+len(allocated))
	for _, a := range reserved {
		skip[a] = struct{}{}
	}
	for _, a := range allocated {
		skip[a] = struct{}{}
	}

	network := base.Addr()
	broadcast := lastAddr(base)

	for addr := network.Next(); addr != broadcast; addr = addr.Next() {
		if _, excluded := skip[addr]; !excluded {
			return addr, nil
		}
	}
	return netip.Addr{}, ErrSubnetExhausted
}

// lastAddr computes the broadcast (highest) address of prefix by
// setting every host bit to 1. Works for both IPv4 and IPv6 since it
// operates on the raw address bytes rather than assuming a width.
func lastAddr(prefix netip.Prefix) netip.Addr {
	addrBytes := prefix.Addr().AsSlice()
	hostBits := len(addrBytes)*8 - prefix.Bits()

	out := make([]byte, len(addrBytes))
	copy(out, addrBytes)

	i := len(out) - 1
	for hostBits > 0 {
		if hostBits >= 8 {
			out[i] = 0xFF
			hostBits -= 8
		} else {
			out[i] |= byte(0xFF >> (8 - hostBits))
			hostBits = 0
		}
		i--
	}

	addr, _ := netip.AddrFromSlice(out)
	return addr
}