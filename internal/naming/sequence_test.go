package naming

import (
	"errors"
	"testing"
)

func TestFormatSequence(t *testing.T) {
	cases := []struct {
		seq     int
		want    string
		wantErr error
	}{
		{seq: 0, want: "000"},
		{seq: 7, want: "007"},
		{seq: 999, want: "999"},
		{seq: 1000, want: "A00"}, // the 999 -> A00 boundary
		{seq: 1099, want: "A99"},
		{seq: 1100, want: "B00"},
		{seq: 3599, want: "Z99"},                   // last valid value
		{seq: 3600, wantErr: ErrSequenceExhausted}, // hard-block
	}

	for _, tc := range cases {
		got, err := FormatSequence(tc.seq)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("FormatSequence(%d): want error %v, got %v", tc.seq, tc.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("FormatSequence(%d): unexpected error %v", tc.seq, err)
			continue
		}
		if got != tc.want {
			t.Errorf("FormatSequence(%d) = %q, want %q", tc.seq, got, tc.want)
		}
	}
}
