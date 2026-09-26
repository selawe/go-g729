package codebook

import (
	"github.com/selawe/go-g729/internal/params"
)

// Track position sets for G.729 / G.729A 4-pulse algebraic codebook:
// Track 0: {0, 5, 10, 15, 20, 25, 30, 35} (8 positions, 3 bits)
// Track 1: {1, 6, 11, 16, 21, 26, 31, 36} (8 positions, 3 bits)
// Track 2: {2, 7, 12, 17, 22, 27, 32, 37} (8 positions, 3 bits)
// Track 3: {3, 8, 13, 18, 23, 28, 33, 38, 4, 9, 14, 19, 24, 29, 34, 39} (16 positions, 4 bits)
var (
	Track0 = [8]int{0, 5, 10, 15, 20, 25, 30, 35}
	Track1 = [8]int{1, 6, 11, 16, 21, 26, 31, 36}
	Track2 = [8]int{2, 7, 12, 17, 22, 27, 32, 37}
	Track3 = [16]int{
		3, 8, 13, 18, 23, 28, 33, 38,
		4, 9, 14, 19, 24, 29, 34, 39,
	}
)

// BuildCodeVector constructs a 40-sample algebraic codevector containing exactly
// 4 pulses from the 13-bit position index and 4-bit sign index.
//
// Shared by both G.729A and G.729 Full (identical bitstream format).
func BuildCodeVector(index int, sign int) (cod [params.L_SUBFR]float32) {
	p0, p1, p2, p3 := ExtractPulsePositions(index)

	if (sign & 1) != 0 {
		cod[p0] = 1.0
	} else {
		cod[p0] = -1.0
	}

	if (sign & 2) != 0 {
		cod[p1] = 1.0
	} else {
		cod[p1] = -1.0
	}

	if (sign & 4) != 0 {
		cod[p2] = 1.0
	} else {
		cod[p2] = -1.0
	}

	if (sign & 8) != 0 {
		cod[p3] = 1.0
	} else {
		cod[p3] = -1.0
	}

	return cod
}

// ExtractPulsePositions extracts the 4 pulse positions from the 13-bit position index.
func ExtractPulsePositions(index int) (p0, p1, p2, p3 int) {
	i0 := index & 7
	p0 = i0 * 5

	index >>= 3
	i1 := index & 7
	p1 = i1*5 + 1

	index >>= 3
	i2 := index & 7
	p2 = i2*5 + 2

	index >>= 3
	j := index & 1
	index >>= 1
	i3 := index & 7
	p3 = i3*5 + 3 + j

	return p0, p1, p2, p3
}

// PackPulseIndex packs 4 pulse positions and signs into the 13-bit position index
// and 4-bit sign index.
func PackPulseIndex(p0, p1, p2, p3 int, s0, s1, s2, s3 int) (index int, sign int) {
	i0 := p0 / 5
	i1 := (p1 - 1) / 5
	i2 := (p2 - 2) / 5

	rem3 := p3 % 5
	j := 0
	if rem3 == 4 {
		j = 1
	}
	i3 := (p3 - 3 - j) / 5

	index = (i0 & 7) | ((i1 & 7) << 3) | ((i2 & 7) << 6) | ((j & 1) << 9) | ((i3 & 7) << 10)

	sign = 0
	if s0 > 0 {
		sign |= 1
	}
	if s1 > 0 {
		sign |= 2
	}
	if s2 > 0 {
		sign |= 4
	}
	if s3 > 0 {
		sign |= 8
	}

	return index, sign
}
