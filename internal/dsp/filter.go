package dsp

import (
	"fmt"

	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// HPFState maintains the 4 state variables for a 2nd-order IIR high-pass filter.
// X0, X1: past inputs x[n-1], x[n-2]
// Y1, Y2: past outputs y[n-1], y[n-2]
type HPFState struct {
	X0 float32
	X1 float32
	Y1 float32
	Y2 float32
}

// Reset clears the filter memory states.
func (s *HPFState) Reset() {
	*s = HPFState{}
}

// HighPassFilter applies a 2nd-order 140 Hz IIR high-pass filter in-place to signal.
// This is used for speech pre-processing in the encoder.
// Difference equation:
//
//	y[i] = b[0]*x[i] + b[1]*x[i-1] + b[2]*x[i-2] + a[1]*y[i-1] + a[2]*y[i-2]
func HighPassFilter(signal []float32, state *HPFState) {
	applyBiquad(signal, state, tables.HPF140_B, tables.HPF140_A)
}

// PostProcessFilter applies a 2nd-order 100 Hz IIR high-pass filter in-place to signal.
// This is used for output speech post-processing in the decoder.
func PostProcessFilter(signal []float32, state *HPFState) {
	applyBiquad(signal, state, tables.HPF100_B, tables.HPF100_A)
}

func applyBiquad(signal []float32, state *HPFState, b, a [3]float32) {
	var x0, x1, y1, y2 float32
	if state != nil {
		x0, x1 = state.X0, state.X1
		y1, y2 = state.Y1, state.Y2
	}

	b0, b1, b2 := b[0], b[1], b[2]
	a1, a2 := a[1], a[2]

	for i, s := range signal {
		x2 := x1
		x1 = x0
		x0 = s

		y0 := y1*a1 + y2*a2 + x0*b0 + x1*b1 + x2*b2

		signal[i] = y0
		y2 = y1
		y1 = y0
	}

	if state != nil {
		state.X0, state.X1 = x0, x1
		state.Y1, state.Y2 = y1, y2
	}
}

// SynthesisFilter filters input excitation x through 1/A(z) to produce speech out.
// a contains the LP coefficients: either 11 elements (with a[0] = 1.0) or 10 elements (a[1..10]).
// mem holds the filter history (length >= 10).
// If update is true, the last 10 samples of out are copied into mem[:10].
// Difference equation:
//
//	y[i] = x[i] - sum_{j=1}^M a[j]*y[i-j]
func SynthesisFilter(out, x, a, mem []float32, update bool) {
	l := len(x)
	if len(out) < l {
		panic("dsp: out slice length is less than input x length")
	}

	var aCoeffs []float32
	if len(a) >= params.M+1 {
		aCoeffs = a[1 : params.M+1]
	} else if len(a) >= params.M {
		aCoeffs = a[:params.M]
	} else {
		panic("dsp: a slice length must be at least M (10)")
	}
	_ = aCoeffs[params.M-1]

	var buf [params.L_FRAME + params.M]float32
	if l+params.M > len(buf) {
		panic(fmt.Sprintf("dsp: SynthesisFilter input length %d exceeds L_FRAME (%d)", l, params.L_FRAME))
	}
	yy := buf[:l+params.M]

	if mem != nil && len(mem) >= params.M {
		copy(yy[:params.M], mem[:params.M])
	} else {
		for i := 0; i < params.M; i++ {
			yy[i] = 0
		}
	}

	a0, a1, a2, a3, a4, a5, a6, a7, a8, a9 := aCoeffs[0], aCoeffs[1], aCoeffs[2], aCoeffs[3], aCoeffs[4], aCoeffs[5], aCoeffs[6], aCoeffs[7], aCoeffs[8], aCoeffs[9]
	for i := 0; i < l; i++ {
		idx := params.M + i
		p := yy[i:idx]
		_ = p[9]
		s := x[i] - a0*p[9] - a1*p[8] - a2*p[7] - a3*p[6] - a4*p[5] -
			a5*p[4] - a6*p[3] - a7*p[2] - a8*p[1] - a9*p[0]
		yy[idx] = s
		out[i] = s
	}

	if update && mem != nil && len(mem) >= params.M {
		copy(mem[:params.M], yy[l:l+params.M])
	}
}

// Residue filters input speech x through A(z) to produce LP residual out.
// a contains the LP coefficients: either 11 elements (with a[0] = 1.0) or 10 elements (a[1..10]).
// mem holds the filter history (length >= 10).
// If update is true, the last 10 samples of x are copied into mem[:10].
// Difference equation:
//
//	y[i] = x[i] + sum_{j=1}^M a[j]*x[i-j]
func Residue(out, x, a, mem []float32, update bool) {
	l := len(x)
	if len(out) < l {
		panic("dsp: out slice length is less than input x length")
	}

	var aCoeffs []float32
	if len(a) >= params.M+1 {
		aCoeffs = a[1 : params.M+1]
	} else if len(a) >= params.M {
		aCoeffs = a[:params.M]
	} else {
		panic("dsp: a slice length must be at least M (10)")
	}
	_ = aCoeffs[params.M-1]

	var buf [params.L_FRAME + params.M]float32
	if l+params.M > len(buf) {
		panic(fmt.Sprintf("dsp: Residue input length %d exceeds L_FRAME (%d)", l, params.L_FRAME))
	}
	xx := buf[:l+params.M]

	if mem != nil && len(mem) >= params.M {
		copy(xx[:params.M], mem[:params.M])
	} else {
		for i := 0; i < params.M; i++ {
			xx[i] = 0
		}
	}
	copy(xx[params.M:], x[:l])

	a0, a1, a2, a3, a4, a5, a6, a7, a8, a9 := aCoeffs[0], aCoeffs[1], aCoeffs[2], aCoeffs[3], aCoeffs[4], aCoeffs[5], aCoeffs[6], aCoeffs[7], aCoeffs[8], aCoeffs[9]
	for i := 0; i < l; i++ {
		idx := params.M + i
		p := xx[i:idx]
		_ = p[9]
		s := x[i] + a0*p[9] + a1*p[8] + a2*p[7] + a3*p[6] + a4*p[5] +
			a5*p[4] + a6*p[3] + a7*p[2] + a8*p[1] + a9*p[0]
		out[i] = s
	}

	if update && mem != nil && len(mem) >= params.M {
		copy(mem[:params.M], x[l-params.M:l])
	}
}

// Convolution computes causal linear convolution of input vector x and impulse response h:
//
//	out[n] = sum_{i=0}^n x[i]*h[n-i]
//
// for n = 0 ... len(out)-1.
func Convolution(out, h, x []float32) {
	l := len(out)
	lx := len(x)
	lh := len(h)

	for n := 0; n < l; n++ {
		var s float32
		maxI := n
		if maxI >= lx {
			maxI = lx - 1
		}
		for i := 0; i <= maxI; i++ {
			hIdx := n - i
			if hIdx < lh {
				s += x[i] * h[hIdx]
			}
		}
		out[n] = s
	}
}
