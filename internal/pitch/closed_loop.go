package pitch

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
)

// CorHX computes the backward correlation of impulse response h with target xn:
//
//	dn[i] = sum_{j=i}^{39} xn[j] * h[j-i]
//
// for i = 0...39.
func CorHX(dn, h, xn []float32) {
	l := len(xn)
	for i := 0; i < l; i++ {
		var sum float32
		for j := i; j < l; j++ {
			sum += xn[j] * h[j-i]
		}
		dn[i] = sum
	}
}

// ClosedLoopPitch determines the closed-loop pitch delay (integer lag t0 and fraction frac)
// for subframe length params.L_SUBFR (40).
//
// Arguments:
//   - excBuf: excitation buffer with history
//   - offset: start index of current subframe in excBuf
//   - xn: target vector (length 40)
//   - h: impulse response of weighted synthesis filter (length 40)
//   - t0Min, t0Max: search range
//   - subframe: 0 for 1st subframe, 1 for 2nd subframe
//
// Returns:
//   - t0: integer pitch delay
//   - pitFrac: fractional pitch delay (-1, 0, or 1 in units of 1/3)
//   - Upon return, excBuf[offset:offset+40] contains the selected adaptive excitation vector.
func ClosedLoopPitch(excBuf []float32, offset int, xn, h []float32, t0Min, t0Max int, subframe int) (t0 int, pitFrac int) {
	var dn [params.L_SUBFR]float32
	CorHX(dn[:], h, xn)

	// 1. Search integer delay with maximum correlation
	max := float32(-math.MaxFloat32)
	t0 = t0Min

	for t := t0Min; t <= t0Max; t++ {
		pastIdx := offset - t
		var corr float32
		for j := 0; j < params.L_SUBFR; j++ {
			corr += dn[j] * excBuf[pastIdx+j]
		}
		if corr > max {
			max = corr
			t0 = t
		}
	}

	// 2. Fractional pitch search
	InterpExcitation(excBuf, offset, t0, 0, params.L_SUBFR)
	var maxCorr float32
	for j := 0; j < params.L_SUBFR; j++ {
		maxCorr += dn[j] * excBuf[offset+j]
	}
	pitFrac = 0

	// In 1st subframe, delays > 84 use integer resolution only
	if subframe == 0 && t0 > 84 {
		return t0, 0
	}

	var excTmp [params.L_SUBFR]float32
	copy(excTmp[:], excBuf[offset:offset+params.L_SUBFR])

	// Test fraction -1/3
	InterpExcitation(excBuf, offset, t0, -1, params.L_SUBFR)
	var corrNeg float32
	for j := 0; j < params.L_SUBFR; j++ {
		corrNeg += dn[j] * excBuf[offset+j]
	}
	if corrNeg > maxCorr {
		maxCorr = corrNeg
		pitFrac = -1
		copy(excTmp[:], excBuf[offset:offset+params.L_SUBFR])
	}

	// Test fraction +1/3
	InterpExcitation(excBuf, offset, t0, 1, params.L_SUBFR)
	var corrPos float32
	for j := 0; j < params.L_SUBFR; j++ {
		corrPos += dn[j] * excBuf[offset+j]
	}
	if corrPos > maxCorr {
		pitFrac = 1
	} else {
		copy(excBuf[offset:offset+params.L_SUBFR], excTmp[:])
	}

	return t0, pitFrac
}

// PitchGain computes the adaptive codebook gain gp and correlation terms gCoeff.
//
// Arguments:
//   - xn: target vector (length 40)
//   - y1: filtered adaptive codebook excitation vector (length 40)
//
// Returns:
//   - gain: pitch gain clamped to [0.0, 1.2]
//   - gCoeff: [0] = <y1, y1>, [1] = -2*<xn, y1> + 0.01 (used for gain VQ)
func PitchGain(xn, y1 []float32) (gain float32, gCoeff [2]float32) {
	var yy float32 = 0.01
	var xy float32
	l := len(xn)

	for i := 0; i < l; i++ {
		yy += y1[i] * y1[i]
		xy += xn[i] * y1[i]
	}

	gCoeff[0] = yy
	gCoeff[1] = -2.0*xy + 0.01

	gain = xy / yy
	if gain < params.GP_MIN {
		gain = params.GP_MIN
	}
	if gain > params.GP_MAX {
		gain = params.GP_MAX
	}

	return gain, gCoeff
}
