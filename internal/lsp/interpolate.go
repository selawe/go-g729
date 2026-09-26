package lsp

import (
	"github.com/selawe/go-g729/internal/params"
)

// InterpolateLSP interpolates LSP coefficients for subframe 1 and subframe 2,
// then converts both to LP filter prediction coefficients A(z).
//
// Subframe 1: lsp_sf1 = 0.5*prevLSP + 0.5*currLSP
// Subframe 2: lsp_sf2 = currLSP
func InterpolateLSP(prevLSP, currLSP [params.M]float32) (aSubfr1, aSubfr2 [params.M + 1]float32) {
	var lspSF1 [params.M]float32
	for i := 0; i < params.M; i++ {
		lspSF1[i] = 0.5*prevLSP[i] + 0.5*currLSP[i]
	}

	aSubfr1 = LSP2LPC(lspSF1)
	aSubfr2 = LSP2LPC(currLSP)
	return aSubfr1, aSubfr2
}

// InterpolateLSPDirect returns the interpolated LSP vectors directly in the cosine domain.
func InterpolateLSPDirect(prevLSP, currLSP [params.M]float32) (lspSF1, lspSF2 [params.M]float32) {
	for i := 0; i < params.M; i++ {
		lspSF1[i] = 0.5*prevLSP[i] + 0.5*currLSP[i]
	}
	lspSF2 = currLSP
	return lspSF1, lspSF2
}
