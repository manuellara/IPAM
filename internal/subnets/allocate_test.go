package subnets

import (
	"errors"
	"net/netip"
	"testing"
)

func TestNextFreeIP(t *testing.T) {
	tests := []struct {
		name      string
		prefix    netip.Prefix
		reserved  []netip.Addr
		allocated []netip.Addr
		want      netip.Addr
		wantErr   error
	}{
		{
			name:   "first usable IPv4 address",
			prefix: netip.MustParsePrefix("192.0.2.0/29"),
			want:   netip.MustParseAddr("192.0.2.1"),
		},
		{
			name:      "skips reserved and allocated addresses",
			prefix:    netip.MustParsePrefix("192.0.2.0/29"),
			reserved:  []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			allocated: []netip.Addr{netip.MustParseAddr("192.0.2.2")},
			want:      netip.MustParseAddr("192.0.2.3"),
		},
		{
			name:      "exhausted IPv4 prefix",
			prefix:    netip.MustParsePrefix("192.0.2.0/30"),
			reserved:  []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			allocated: []netip.Addr{netip.MustParseAddr("192.0.2.2")},
			wantErr:   ErrSubnetExhausted,
		},
		{
			name:    "IPv4 prefix with fewer than two host bits",
			prefix:  netip.MustParsePrefix("192.0.2.0/31"),
			wantErr: ErrSubnetExhausted,
		},
		{
			name:   "first usable IPv6 address",
			prefix: netip.MustParsePrefix("2001:db8::/126"),
			want:   netip.MustParseAddr("2001:db8::1"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NextFreeIP(tt.prefix, tt.reserved, tt.allocated)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NextFreeIP() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NextFreeIP() = %v, want %v", got, tt.want)
			}
		})
	}
}
