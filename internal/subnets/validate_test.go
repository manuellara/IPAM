package subnets

import "testing"

func int64Ptr(i int64) *int64 { return &i }

func TestValidateNoOverlap(t *testing.T) {
	existing := []ExistingSubnet{
		{ID: 1, CIDR: "10.20.4.0/24"},
	}

	tests := []struct {
		name      string
		candidate string
		excludeID *int64
		wantErr   bool
	}{
		{"adjacent disjoint block", "10.20.5.0/24", nil, false},
		{"contained smaller block, first half", "10.20.4.0/25", nil, true},
		{"contained smaller block, second half", "10.20.4.128/25", nil, true},
		{"identical block", "10.20.4.0/24", nil, true},
		{"editing subnet 1 to itself (excluded)", "10.20.4.0/24", int64Ptr(1), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate, err := ParseCIDR(tt.candidate)
			if err != nil {
				t.Fatalf("unexpected parse error: %v", err)
			}
			err = ValidateNoOverlap(candidate, existing, tt.excludeID)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateNoOverlap() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateNoOverlap_LargerExistingBlock(t *testing.T) {
	// The "admin fat-fingers a subnet already covered by a bigger
	// existing block" case from the example table.
	existing := []ExistingSubnet{
		{ID: 1, CIDR: "10.20.0.0/16"},
	}
	candidate, _ := ParseCIDR("10.20.4.0/24")
	if err := ValidateNoOverlap(candidate, existing, nil); err == nil {
		t.Error("expected overlap error, got nil")
	}
}

func TestParseCIDR_NormalizesHostBits(t *testing.T) {
	p, err := ParseCIDR("10.20.4.5/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.String() != "10.20.4.0/24" {
		t.Errorf("expected normalized 10.20.4.0/24, got %s", p.String())
	}
}