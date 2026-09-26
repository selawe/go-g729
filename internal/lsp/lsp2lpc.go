package lsp

import (
	"github.com/selawe/go-g729/internal/params"
)

// LSP2LPC converts line spectral pairs lsp[0...9] (cosine domain)
// to LP predictor coefficients a[0...10], where a[0] = 1.0.
func LSP2LPC(lsp [params.M]float32) (a [params.M + 1]float32) {
	const nc = params.M / 2 // 5
	var f1, f2 [nc + 1]float32

	// Even LSP indices (0, 2, 4, 6, 8) form F1
	getLSPPoly(&lsp, 0, f1[:])
	// Odd LSP indices (1, 3, 5, 7, 9) form F2
	getLSPPoly(&lsp, 1, f2[:])

	// Convolve F1 with (1 + z^-1) and F2 with (1 - z^-1)
	for i := nc; i > 0; i-- {
		f1[i] += f1[i-1]
		f2[i] -= f2[i-1]
	}

	a[0] = 1.0
	for i, j := 1, params.M; i <= nc; i, j = i+1, j-1 {
		a[i] = 0.5 * (f1[i] + f2[i])
		a[j] = 0.5 * (f1[i] - f2[i])
	}

	return a
}

// getLSPPoly computes the polynomial F1(z) or F2(z) from LSP coefficients.
// start is 0 for F1 (even indices) and 1 for F2 (odd indices).
func getLSPPoly(lsp *[params.M]float32, start int, f []float32) {
	const nc = params.M / 2 // 5

	f[0] = 1.0
	b := -2.0 * lsp[start]
	f[1] = b
	for i := 2; i <= nc; i++ {
		b = -2.0 * lsp[start+2*i-2]
		f[i] = b*f[i-1] + 2.0*f[i-2]
		for j := i - 1; j > 1; j-- {
			f[j] += b*f[j-1] + f[j-2]
		}
		f[1] += b
	}
}
