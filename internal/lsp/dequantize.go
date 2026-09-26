package lsp

import (
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// DequantizeLSP reconstructs line spectral pairs lspQ from quantized codebook indices
// and updates the MA predictor memory freqPrev.
func DequantizeLSP(l0, l1, l2, l3 int, freqPrev *[params.MA_NP][params.M]float32) (lspQ [params.M]float32) {
	return DequantizeLSPExt(l0, l1, l2, l3, false, nil, nil, freqPrev)
}

// DequantizeLSPExt reconstructs line spectral pairs lspQ from quantized codebook indices
// (or reuses previous LSF on frame erasure) and updates the MA predictor memory freqPrev.
func DequantizeLSPExt(
	l0, l1, l2, l3 int,
	erase bool,
	prevLSF *[params.M]float32,
	prevMA *int,
	freqPrev *[params.MA_NP][params.M]float32,
) (lspQ [params.M]float32) {
	if erase {
		ma := 0
		if prevMA != nil {
			ma = *prevMA
			if ma < 0 || ma > 1 {
				ma = 0
			}
		}

		var lsfQ [params.M]float32
		if prevLSF != nil {
			copy(lsfQ[:], prevLSF[:])
		} else {
			copy(lsfQ[:], tables.FreqPrevReset[:])
		}

		// lsp_prev_extract: calculate target vector from composed LSP
		var buf [params.M]float32
		for j := 0; j < params.M; j++ {
			diff := lsfQ[j]
			if freqPrev != nil {
				for k := 0; k < params.MA_NP; k++ {
					diff -= freqPrev[k][j] * tables.MAPredictor[ma][k][j]
				}
			}
			buf[j] = diff * tables.InvMAPredictorSum[ma][j]
		}

		// lsp_prev_update: push to freqPrev
		if freqPrev != nil {
			for k := params.MA_NP - 1; k > 0; k-- {
				copy(freqPrev[k][:], freqPrev[k-1][:])
			}
			copy(freqPrev[0][:], buf[:])
		}

		return LSF2LSP(lsfQ)
	}

	// Guard code indices against corrupt bitstream frames
	if l0 < 0 || l0 > 1 {
		l0 = 0
	}
	if l1 < 0 || l1 >= params.NC0 {
		l1 = 0
	}
	if l2 < 0 || l2 >= params.NC1 {
		l2 = 0
	}
	if l3 < 0 || l3 >= params.NC1 {
		l3 = 0
	}

	var buf [params.M]float32
	for j := 0; j < 5; j++ {
		buf[j] = tables.LSP_L1[l1][j] + tables.LSP_L2[l2][j]
	}
	for j := 5; j < params.M; j++ {
		buf[j] = tables.LSP_L1[l1][j] + tables.LSP_L3[l3][j-5]
	}

	Expand12(buf[:10], Gap1)
	Expand12(buf[:10], Gap2)

	var lsfQ [params.M]float32
	for j := 0; j < params.M; j++ {
		lsfQ[j] = buf[j] * tables.MAPredictorSum[l0][j]
		if freqPrev != nil {
			for k := 0; k < params.MA_NP; k++ {
				lsfQ[j] += freqPrev[k][j] * tables.MAPredictor[l0][k][j]
			}
		}
	}

	if freqPrev != nil {
		for k := params.MA_NP - 1; k > 0; k-- {
			copy(freqPrev[k][:], freqPrev[k-1][:])
		}
		copy(freqPrev[0][:], buf[:])
	}

	StabilizeLSF(&lsfQ, Gap3)
	if prevLSF != nil {
		copy(prevLSF[:], lsfQ[:])
	}
	if prevMA != nil {
		*prevMA = l0
	}
	return LSF2LSP(lsfQ)
}
