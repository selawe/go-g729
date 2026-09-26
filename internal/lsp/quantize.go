package lsp

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// InitLSPMA initializes the MA prediction memory with the standard ITU-T reset values.
func InitLSPMA(freqPrev *[params.MA_NP][params.M]float32) {
	for k := 0; k < params.MA_NP; k++ {
		copy(freqPrev[k][:], tables.FreqPrevReset[:])
	}
}

// QuantizeLSP quantizes line spectral pairs lsp[0...9] using 2-stage switched MA
// vector quantization as defined in ITU-T G.729 / G.729A.
//
// Arguments:
//   - lsp: input line spectral pairs in cosine domain (1.0 > lsp > -1.0)
//   - freqPrev: 4-frame MA predictor history (updated in-place)
//
// Returns:
//   - l0: switched predictor index (0 or 1, 1 bit)
//   - l1: 1st stage codebook index (0...127, 7 bits)
//   - l2: 2nd stage lower codebook index (0...31, 5 bits)
//   - l3: 2nd stage higher codebook index (0...31, 5 bits)
//   - lspQ: quantized LSP vector in cosine domain
func QuantizeLSP(lsp [params.M]float32, freqPrev *[params.MA_NP][params.M]float32) (l0, l1, l2, l3 int, lspQ [params.M]float32) {
	lsf := LSP2LSF(lsp)
	wegt := ComputeLSFWeights(lsf)

	var (
		cand    [2]int
		tindex1 [2]int
		tindex2 [2]int
		tdist   [2]float32
		candBuf [2][params.M]float32
	)

	// Search both MA predictor modes (0 and 1)
	for mode := 0; mode < 2; mode++ {
		var rbuf [params.M]float32
		for j := 0; j < params.M; j++ {
			var pred float32
			for k := 0; k < params.MA_NP; k++ {
				pred += freqPrev[k][j] * tables.MAPredictor[mode][k][j]
			}
			rbuf[j] = (lsf[j] - pred) * tables.InvMAPredictorSum[mode][j]
		}

		// Stage 1 search: 128 entries of 10 LSFs (unweighted Euclidean distance)
		bestL1 := 0
		minDist1 := float32(math.MaxFloat32)
		for i := 0; i < params.NC0; i++ {
			var dist float32
			for j := 0; j < params.M; j++ {
				diff := rbuf[j] - tables.LSP_L1[i][j]
				dist += diff * diff
			}
			if dist < minDist1 {
				minDist1 = dist
				bestL1 = i
			}
		}
		cand[mode] = bestL1

		// Stage 2 lower search: 32 entries of 5 LSFs (weighted)
		var buf [params.M]float32
		for j := 0; j < 5; j++ {
			buf[j] = rbuf[j] - tables.LSP_L1[bestL1][j]
		}
		bestL2 := 0
		minDist2 := float32(math.MaxFloat32)
		for k1 := 0; k1 < params.NC1; k1++ {
			var dist float32
			for j := 0; j < 5; j++ {
				diff := buf[j] - tables.LSP_L2[k1][j]
				dist += wegt[j] * diff * diff
			}
			if dist < minDist2 {
				minDist2 = dist
				bestL2 = k1
			}
		}
		tindex1[mode] = bestL2

		for j := 0; j < 5; j++ {
			buf[j] = tables.LSP_L1[bestL1][j] + tables.LSP_L2[bestL2][j]
		}

		// Stage 2 higher search: 32 entries of 5 LSFs (weighted).
		// Note: the upper search residual is computed directly from rbuf, not from buf[0..4],
		// so buf[0..4] does not need intermediate expansion before this search.
		var upResidual [5]float32
		for j := 0; j < 5; j++ {
			upResidual[j] = rbuf[j+5] - tables.LSP_L1[bestL1][j+5]
		}
		bestL3 := 0
		minDist3 := float32(math.MaxFloat32)
		for k2 := 0; k2 < params.NC1; k2++ {
			var dist float32
			for j := 0; j < 5; j++ {
				diff := upResidual[j] - tables.LSP_L3[k2][j]
				dist += wegt[j+5] * diff * diff
			}
			if dist < minDist3 {
				minDist3 = dist
				bestL3 = k2
			}
		}
		tindex2[mode] = bestL3

		for j := 5; j < params.M; j++ {
			buf[j] = tables.LSP_L1[bestL1][j] + tables.LSP_L3[bestL3][j-5]
		}
		// Apply gap enforcement identically to the decoder (ITU-T G.729 reference: lsp_expand_1_2 twice).
		// Expand12 covers all 10 adjacent pairs including the lower/upper boundary at index (4,5).
		// This ensures encoder MA memory (freqPrev[0]) == decoder MA memory after each frame.
		Expand12(buf[:10], Gap1)
		Expand12(buf[:10], Gap2)

		candBuf[mode] = buf

		// Total distortion computation
		var totalDist float32
		for j := 0; j < params.M; j++ {
			diff := (buf[j] - rbuf[j]) * tables.MAPredictorSum[mode][j]
			totalDist += wegt[j] * diff * diff
		}
		tdist[mode] = totalDist
	}

	// Select mode with lowest total distortion
	bestMode := 0
	if tdist[1] < tdist[0] {
		bestMode = 1
	}

	l0 = bestMode
	l1 = cand[bestMode]
	l2 = tindex1[bestMode]
	l3 = tindex2[bestMode]

	// Reconstruct quantized LSF
	chosenBuf := candBuf[bestMode]
	var lsfQ [params.M]float32
	for j := 0; j < params.M; j++ {
		lsfQ[j] = chosenBuf[j] * tables.MAPredictorSum[bestMode][j]
		for k := 0; k < params.MA_NP; k++ {
			lsfQ[j] += freqPrev[k][j] * tables.MAPredictor[bestMode][k][j]
		}
	}

	// Update MA memory
	for k := params.MA_NP - 1; k > 0; k-- {
		copy(freqPrev[k][:], freqPrev[k-1][:])
	}
	copy(freqPrev[0][:], chosenBuf[:])

	// Check stability
	StabilizeLSF(&lsfQ, Gap3)
	lspQ = LSF2LSP(lsfQ)

	return l0, l1, l2, l3, lspQ
}

// ComputeLSFWeights computes the perceptual weighting factors for LSF distance calculation.
func ComputeLSFWeights(lsf [params.M]float32) (wegt [params.M]float32) {
	const (
		pi04    = float32(math.Pi * 0.04)
		pi92    = float32(math.Pi * 0.92)
		const12 = float32(1.2)
	)

	tmp := lsf[1] - pi04 - 1.0
	if tmp > 0.0 {
		wegt[0] = 1.0
	} else {
		wegt[0] = tmp*tmp*10.0 + 1.0
	}

	for i := 1; i < params.M-1; i++ {
		tmp = lsf[i+1] - lsf[i-1] - 1.0
		if tmp > 0.0 {
			wegt[i] = 1.0
		} else {
			wegt[i] = tmp*tmp*10.0 + 1.0
		}
	}

	tmp = pi92 - lsf[params.M-2] - 1.0
	if tmp > 0.0 {
		wegt[params.M-1] = 1.0
	} else {
		wegt[params.M-1] = tmp*tmp*10.0 + 1.0
	}

	wegt[4] *= const12
	wegt[5] *= const12
	return wegt
}
