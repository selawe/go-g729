package dsp

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
)

// Levinson computes the LPC prediction coefficients a and reflection coefficients rc
// from autocorrelation vector r using the Levinson-Durbin recursion.
//
// Arguments:
//   - r: autocorrelation coefficients r[0...m], length >= m+1.
//   - m: prediction order, typically params.M (10).
//
// Returns:
//   - a: LP prediction filter polynomial a[0...10], where a[0] = 1.0.
//   - rc: reflection coefficients rc[0...9] (used in Annex B VAD for spectral tilt).
//   - err: nil on success, ErrInvalidInput, ErrSingularMatrix, or ErrUnstableFilter.
//
// Arithmetic is computed in float64 internally for optimal numerical stability,
// then cast to float32 upon return.
func Levinson(r []float32, m int) (a [params.M + 1]float32, rc [params.M]float32, err error) {
	a[0] = 1.0
	if m <= 0 || m > params.M {
		return a, rc, ErrInvalidInput
	}
	if len(r) < m+1 {
		return a, rc, ErrInvalidInput
	}

	r0 := float64(r[0])
	if r0 <= 0 || math.IsNaN(r0) || math.IsInf(r0, 0) {
		return a, rc, ErrSingularMatrix
	}

	var (
		a64  [params.M + 1]float64
		rc64 [params.M]float64
	)
	a64[0] = 1.0

	// Order 1
	r1 := float64(r[1])
	rc0 := -r1 / r0
	if math.Abs(rc0) >= 1.0 {
		rc[0] = float32(rc0)
		a[1] = float32(rc0)
		return a, rc, ErrUnstableFilter
	}

	rc64[0] = rc0
	a64[1] = rc0
	err64 := r0 + r1*rc0
	if err64 <= 0.0 {
		err64 = 0.001
	}

	// Higher orders 2 ... m
	for i := 2; i <= m; i++ {
		var s float64
		for j := 0; j < i; j++ {
			s += float64(r[i-j]) * a64[j]
		}

		rci := -s / err64
		if math.Abs(rci) >= 1.0 {
			// Populate coefficients computed so far
			for k := 0; k <= params.M; k++ {
				a[k] = float32(a64[k])
			}
			for k := 0; k < params.M; k++ {
				rc[k] = float32(rc64[k])
			}
			return a, rc, ErrUnstableFilter
		}
		rc64[i-1] = rci

		half := i / 2
		for j := 1; j <= half; j++ {
			l := i - j
			at := a64[j] + rci*a64[l]
			a64[l] = a64[l] + rci*a64[j]
			a64[j] = at
		}
		a64[i] = rci

		err64 += rci * s
		if err64 <= 0.0 {
			err64 = 0.001
		}
	}

	for k := 0; k <= m; k++ {
		a[k] = float32(a64[k])
	}
	for k := 0; k < m; k++ {
		rc[k] = float32(rc64[k])
	}

	return a, rc, nil
}
