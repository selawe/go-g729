package lsp

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// LPC2LSP converts LP prediction coefficients a[0...10] (with a[0]=1.0)
// to line spectral pairs lsp[0...9] in the cosine domain (values between 1.0 and -1.0).
//
// The roots of sum polynomial F1(z) and difference polynomial F2(z) are found
// by evaluating Chebyshev polynomials over Grid50 with bisection.
// If all 10 roots are successfully found, ok is true.
// If fewer than 10 roots are found, ok is false and lsp falls back to prevLSP.
func LPC2LSP(a *[params.M + 1]float32, prevLSP *[params.M]float32) (lsp [params.M]float32, ok bool) {
	const nc = params.M / 2 // 5
	var f1, f2 [nc + 1]float32

	f1[0] = 1.0
	f2[0] = 1.0
	for i, j := 1, params.M; i <= nc; i, j = i+1, j-1 {
		f1[i] = a[i] + a[j] - f1[i-1]
		f2[i] = a[i] - a[j] + f2[i-1]
	}

	nf := 0
	ip := 0
	coef := f1[:]

	xlow := tables.Grid50[0]
	ylow := chebyshev(xlow, coef, nc)

	j := 0
	for nf < params.M && j < len(tables.Grid50)-1 {
		j++
		xhigh := xlow
		yhigh := ylow
		xlow = tables.Grid50[j]
		ylow = chebyshev(xlow, coef, nc)

		if ylow*yhigh <= 0.0 {
			j--

			// 4 bisection steps to refine interval
			for i := 0; i < 4; i++ {
				xmid := 0.5 * (xlow + xhigh)
				ymid := chebyshev(xmid, coef, nc)
				if ylow*ymid <= 0.0 {
					yhigh = ymid
					xhigh = xmid
				} else {
					ylow = ymid
					xlow = xmid
				}
			}

			// Linear interpolation to evaluate root
			denom := yhigh - ylow
			var xint float32
			if denom != 0.0 {
				xint = xlow - ylow*(xhigh-xlow)/denom
			} else {
				xint = 0.5 * (xlow + xhigh)
			}

			lsp[nf] = xint
			nf++

			// Switch polynomial
			ip = 1 - ip
			if ip == 1 {
				coef = f2[:]
			} else {
				coef = f1[:]
			}

			xlow = xint
			ylow = chebyshev(xlow, coef, nc)
		}
	}

	if nf < params.M {
		if prevLSP != nil {
			copy(lsp[:], prevLSP[:])
		} else {
			for i := 0; i < params.M; i++ {
				lsp[i] = float32(math.Cos(math.Pi * float64(i+1) / float64(params.M+1)))
			}
		}
		return lsp, false
	}

	return lsp, true
}

func chebyshev(x float32, f []float32, n int) float32 {
	x2 := 2.0 * x
	b2 := float32(1.0)
	b1 := x2 + f[1]
	for i := 2; i < n; i++ {
		b0 := x2*b1 - b2 + f[i]
		b2 = b1
		b1 = b0
	}
	return x*b1 - b2 + 0.5*f[n]
}
