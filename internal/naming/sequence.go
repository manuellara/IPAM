package naming

import (
	"errors"
	"fmt"
)

// MaxSequence is the highest valid naming_sequences.last_seq value (Z99).
// Exceeding it must hard-fail the approval transaction -- never wrap
// around, since that would silently produce a duplicate hostname.
const MaxSequence = 3599

var ErrSequenceExhausted = errors.New("sequence exhausted: prefix has reached Z99, manual intervention required")

// FormatSequence renders seq (a naming_sequences.last_seq value, already
// incremented by the caller) as its 3-character code: "000"-"999" for
// 0-999, then "A00"-"Z99" for 1000-3599 (the letter increments every 100
// values). Returns ErrSequenceExhausted for seq > MaxSequence -- the
// approval transaction (IPAM-22) must fail cleanly on this error, not
// retry or wrap around. Only called for naming_mode='generated' schemes;
// manual-mode schemes (F5 Virtual Server) never touch naming_sequences.
func FormatSequence(seq int) (string, error) {
	if seq < 0 {
		return "", fmt.Errorf("sequence must be non-negative, got %d", seq)
	}
	if seq > MaxSequence {
		return "", ErrSequenceExhausted
	}
	if seq < 1000 {
		return fmt.Sprintf("%03d", seq), nil
	}
	offset := seq - 1000
	letter := 'A' + rune(offset/100)
	num := offset % 100
	return fmt.Sprintf("%c%02d", letter, num), nil
}
