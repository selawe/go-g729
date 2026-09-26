package filter

import (
	"math"

	"github.com/selawe/go-g729/internal/dsp"
	"github.com/selawe/go-g729/internal/params"
)

const (
	// G.729A perceptual weighting factor
	Gamma1A = float32(0.75)

	// Full G.729 perceptual weighting constants (from LD8K.H and PWF.C)
	Gamma1_0  = float32(0.98)
	Gamma1_1  = float32(0.94)
	Gamma2_0_H = float32(0.70)
	Gamma2_0_L = float32(0.40)
	Gamma2_1  = float32(0.60)
	AlphaPwf  = float32(-6.0)
	BetaPwf   = float32(1.0)
	ThreshL1  = float32(-1.74)
	ThreshH1  = float32(0.65)
	ThreshL2  = float32(-1.52)
	ThreshH2  = float32(0.43)
)

// WeightAz computes spectral expansion (bandwidth expansion) on LP coefficients:
//
//	ap[i] = a[i] * gamma^i, for i = 0..m
//
// Arguments:
//   - a: input LP coefficients (length >= m+1, with a[0] = 1.0)
//   - gamma: bandwidth expansion factor (e.g. 0.75)
//   - ap: output weighted LP coefficients (length >= m+1)
func WeightAz(a []float32, gamma float32, ap []float32) {
	const m = params.M
	ap[0] = a[0]
	fac := gamma
	for i := 1; i <= m; i++ {
		ap[i] = a[i] * fac
		fac *= gamma
	}
}

// PerceptualWeightCoeffs derives the numerator and denominator polynomials of the
// perceptual weighting filter W(z) = A(z/gamma1) / A(z/gamma2):
//
//	ap1[i] = a[i] * gamma1^i
//	ap2[i] = a[i] * gamma2^i
func PerceptualWeightCoeffs(a []float32, gamma1, gamma2 float32, ap1, ap2 []float32) {
	WeightAz(a, gamma1, ap1)
	WeightAz(a, gamma2, ap2)
}

// ApplyPerceptualFilterA computes the weighted speech signal in G.729 Annex A:
//
//	W(z) = A(z) / A(z/gamma1)
//
// Because the numerator is A(z), the input speech is first converted to the LP residual:
//
//	res(n) = speech(n) + sum_{i=1}^M a[i] * speech(n-i)
//
// and then synthesized through 1 / A(z/gamma1) with filter state memW:
//
//	wsp(n) = res(n) - sum_{i=1}^M ap[i] * wsp(n-i)
//
// Arguments:
//   - wsp: output weighted speech buffer (at least len(speech) samples)
//   - speech: input speech buffer
//   - a: LP filter coefficients A(z) (length M+1)
//   - ap: bandwidth-expanded LP coefficients A(z/gamma1) (length M+1)
//   - memW: filter memory (10 samples, updated in place)
func ApplyPerceptualFilterA(wsp, speech, a, ap []float32, memW []float32) {
	// 1. Inverse filter: res = speech * A(z)
	dsp.Residue(wsp, speech, a, nil, false)

	// 2. Synthesis filter: wsp = res * (1 / A(z/gamma1))
	dsp.SynthesisFilter(wsp, wsp, ap, memW, true)
}

// ApplyPerceptualFilterFull computes the weighted speech signal in Full G.729:
//
//	W(z) = A(z/gamma1) / A(z/gamma2)
//
// Arguments:
//   - wsp: output weighted speech buffer (at least len(speech) samples)
//   - speech: input speech buffer
//   - ap1: numerator coefficients A(z/gamma1) (length M+1)
//   - ap2: denominator coefficients A(z/gamma2) (length M+1)
//   - memW: filter memory (10 samples, updated in place)
func ApplyPerceptualFilterFull(wsp, speech, ap1, ap2 []float32, memW []float32) {
	// 1. Inverse filter: res = speech * A(z/gamma1)
	dsp.Residue(wsp, speech, ap1, nil, false)

	// 2. Synthesis filter: wsp = res * (1 / A(z/gamma2))
	dsp.SynthesisFilter(wsp, wsp, ap2, memW, true)
}

// PercVar computes adaptive bandwidth expansion factors gamma1 and gamma2 for Full G.729
// based on reflection coefficients (spectral tilt) and minimum LSF distance.
//
// Arguments:
//   - lsfInt: interpolated LSFs for 1st subframe (10 samples)
//   - lsfNew: unquantized LSFs for 2nd subframe (10 samples)
//   - rc: reflection coefficients from Levinson recursion (at least 2 samples)
//   - larOld: past log-area ratios (2 samples, updated in place)
//   - smooth: smoothing state flag (0 or 1, updated in place)
//
// Returns:
//   - gamma1: weighting factor gamma1 for [subframe 0, subframe 1]
//   - gamma2: weighting factor gamma2 for [subframe 0, subframe 1]
func PercVar(
	lsfInt, lsfNew, rc []float32,
	larOld *[2]float32,
	smooth *int,
) (gamma1, gamma2 [2]float32) {
	var lar [4]float32

	// Convert first 2 reflection coefficients to log-area ratios (LAR)
	for i := 0; i < 2; i++ {
		r := rc[i]
		if r > 0.9999 {
			r = 0.9999
		} else if r < -0.9999 {
			r = -0.9999
		}
		lar[2+i] = float32(math.Log10(float64((1.0 + r) / (1.0 - r))))
	}

	// Interpolate LAR for the 1st subframe
	for i := 0; i < 2; i++ {
		lar[i] = 0.5 * (lar[2+i] + larOld[i])
		larOld[i] = lar[2+i]
	}

	// Loop for 1st and 2nd subframes
	for k := 0; k < 2; k++ {
		critlar0 := lar[2*k]
		critlar1 := lar[2*k+1]

		if *smooth != 0 {
			if critlar0 < ThreshL1 && critlar1 > ThreshH1 {
				*smooth = 0
			}
		} else {
			if critlar0 > ThreshL2 || critlar1 < ThreshH2 {
				*smooth = 1
			}
		}

		if *smooth == 0 {
			gamma1[k] = Gamma1_0

			var lsf []float32
			if k == 0 {
				lsf = lsfInt
			} else {
				lsf = lsfNew
			}

			dMin := lsf[1] - lsf[0]
			for i := 1; i < params.M-1; i++ {
				temp := lsf[i+1] - lsf[i]
				if temp < dMin {
					dMin = temp
				}
			}

			g2 := AlphaPwf*dMin + BetaPwf
			if g2 > Gamma2_0_H {
				g2 = Gamma2_0_H
			}
			if g2 < Gamma2_0_L {
				g2 = Gamma2_0_L
			}
			gamma2[k] = g2
		} else {
			gamma1[k] = Gamma1_1
			gamma2[k] = Gamma2_1
		}
	}

	return gamma1, gamma2
}
