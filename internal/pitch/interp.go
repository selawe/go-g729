package pitch

import (
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// InterpExcitation computes the adaptive codebook excitation for a subframe
// using fractional pitch sinc interpolation with 1/3 resolution.
//
// Arguments:
//   - excBuf: excitation buffer containing history before offset and current subframe.
//   - offset: start index of the current subframe in excBuf (e.g. 154 for subframe 1, 194 for subframe 2).
//   - t0: integer pitch delay (lag).
//   - frac: fractional pitch delay: -1, 0, or 1 (representing -1/3, 0, or +1/3).
//   - lSubfr: length of the subframe (typically params.L_SUBFR = 40).
//
// The result is written directly into excBuf[offset : offset + lSubfr].
func InterpExcitation(excBuf []float32, offset int, t0 int, frac int, lSubfr int) {
	x0Idx := offset - t0

	fracMod := -frac
	if fracMod < 0 {
		fracMod += params.UP_SAMP
		x0Idx--
	}

	c1 := fracMod
	c2 := params.UP_SAMP - fracMod

	for j := 0; j < lSubfr; j++ {
		x1Idx := x0Idx + j
		x2Idx := x0Idx + j + 1

		var s float32
		for i, k := 0, 0; i < params.L_INTER10; i, k = i+1, k+params.UP_SAMP {
			s += excBuf[x1Idx-i]*tables.SincTable[c1+k] + excBuf[x2Idx+i]*tables.SincTable[c2+k]
		}
		excBuf[offset+j] = s
	}
}
