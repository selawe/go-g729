package codebook

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// CorrXY2 computes the correlation factors <y2, y2>, -2<xn, y2>, and 2<y1, y2>
// required for gain quantization.
//
// Arguments:
//   - xn: target vector (40 samples)
//   - y1: filtered adaptive codebook vector (40 samples)
//   - y2: filtered algebraic codebook vector (40 samples)
//   - gCoeff: [5]float32 array where gCoeff[0] and gCoeff[1] are precomputed by pitch search,
//     and gCoeff[2..4] are populated by this function.
func CorrXY2(xn, y1, y2 []float32, gCoeff *[5]float32) {
	var y2y2 float32 = 0.01
	for i := 0; i < params.L_SUBFR; i++ {
		y2y2 += y2[i] * y2[i]
	}
	gCoeff[2] = y2y2

	var xny2 float32 = 0.01
	for i := 0; i < params.L_SUBFR; i++ {
		xny2 += xn[i] * y2[i]
	}
	gCoeff[3] = -2.0 * xny2

	var y1y2 float32 = 0.01
	for i := 0; i < params.L_SUBFR; i++ {
		y1y2 += y1[i] * y2[i]
	}
	gCoeff[4] = 2.0 * y1y2
}

// GainPredict computes the predicted codebook gain gcode0 using a 4th-order Moving Average (MA)
// predictor on the innovation energy in dB with mean removed.
//
// Arguments:
//   - pastQuaEn: past 4 quantized log-energies (in dB)
//   - code: innovation codevector (40 samples)
//
// Returns:
//   - gcode0: predicted codebook gain
func GainPredict(pastQuaEn *[params.MA_NP]float32, code []float32) float32 {
	var enerCode float32 = 0.01
	for i := 0; i < params.L_SUBFR; i++ {
		enerCode += code[i] * code[i]
	}
	enerCode = 10.0 * float32(math.Log10(float64(enerCode/float32(params.L_SUBFR))))

	predCode := params.MEAN_ENER - enerCode
	for i := 0; i < params.MA_NP; i++ {
		predCode += tables.MAPredGainCoeffs[i] * pastQuaEn[i]
	}

	gcode0 := float32(math.Pow(10.0, float64(predCode/20.0)))
	return gcode0
}

// GainUpdate updates the history of past quantized energies with the new quantized codebook gain correction factor.
func GainUpdate(pastQuaEn *[params.MA_NP]float32, gCodeFactor float32) {
	if gCodeFactor < 1e-10 {
		gCodeFactor = 1e-10
	}
	for i := params.MA_NP - 1; i > 0; i-- {
		pastQuaEn[i] = pastQuaEn[i-1]
	}
	pastQuaEn[0] = 20.0 * float32(math.Log10(float64(gCodeFactor)))
}

// GainUpdateErasure updates the history of past quantized energies when a frame erasure occurs.
func GainUpdateErasure(pastQuaEn *[params.MA_NP]float32) {
	var avPredEn float32
	for i := 0; i < params.MA_NP; i++ {
		avPredEn += pastQuaEn[i]
	}
	avPredEn = avPredEn*0.25 - 4.0
	if avPredEn < -14.0 {
		avPredEn = -14.0
	}
	for i := params.MA_NP - 1; i > 0; i-- {
		pastQuaEn[i] = pastQuaEn[i-1]
	}
	pastQuaEn[0] = avPredEn
}

// GbkPresel performs pre-selection of candidate regions in gain codebook 1 (GA, 3-bit)
// and codebook 2 (GB, 4-bit) based on unquantized ideal gains bestGain and predicted gain gcode0.
func GbkPresel(bestGain [2]float32, gcode0 float32) (cand1, cand2 int) {
	invCoef := params.INV_COEF
	x := (bestGain[1] - (tables.GainCoef[0][0]*bestGain[0]+tables.GainCoef[1][1])*gcode0) * invCoef
	y := (tables.GainCoef[1][0]*(-tables.GainCoef[0][1]+bestGain[0]*tables.GainCoef[0][0])*gcode0 - tables.GainCoef[0][0]*bestGain[1]) * invCoef

	if gcode0 > 0.0 {
		cand1 = 0
		for cand1 < (params.NCODE1 - params.NCAN1) {
			if y > tables.GainThr1[cand1]*gcode0 {
				cand1++
			} else {
				break
			}
		}

		cand2 = 0
		for cand2 < (params.NCODE2 - params.NCAN2) {
			if x > tables.GainThr2[cand2]*gcode0 {
				cand2++
			} else {
				break
			}
		}
	} else {
		cand1 = 0
		for cand1 < (params.NCODE1 - params.NCAN1) {
			if y < tables.GainThr1[cand1]*gcode0 {
				cand1++
			} else {
				break
			}
		}

		cand2 = 0
		for cand2 < (params.NCODE2 - params.NCAN2) {
			if x < tables.GainThr2[cand2]*gcode0 {
				cand2++
			} else {
				break
			}
		}
	}

	return cand1, cand2
}

// QuantizeGain jointly quantizes adaptive codebook gain (pitch gain) and fixed codebook gain
// using a 2-stage conjugate-structure codebook: GA (3-bit) and GB (4-bit).
//
// Arguments:
//   - code: innovation codevector (40 samples)
//   - gCoeff: [5]float32 array containing correlation products:
//     gCoeff[0] = <y1, y1>, gCoeff[1] = -2<xn, y1>,
//     gCoeff[2] = <y2, y2>, gCoeff[3] = -2<xn, y2>, gCoeff[4] = 2<y1, y2>
//   - pastQuaEn: history of past 4 quantized energies (modified in place)
//   - tameFlag: 1 if pitch taming is active, 0 otherwise
//
// Returns:
//   - ga: 3-bit gain index (0..7)
//   - gb: 4-bit gain index (0..15)
//   - gainPit: quantized pitch gain
//   - gainCode: quantized fixed codebook gain
func QuantizeGain(code []float32, gCoeff *[5]float32, pastQuaEn *[params.MA_NP]float32, tameFlag int) (ga, gb int, gainPit, gainCode float32) {
	// 1. Predict codebook gain gcode0 from past energy history.
	gcode0 := GainPredict(pastQuaEn, code)

	// 2. Compute unquantized ideal gains by solving the 2x2 linear least-squares system:
	denom := 4.0*gCoeff[0]*gCoeff[2] - gCoeff[4]*gCoeff[4]
	var tmp float32
	if math.Abs(float64(denom)) > 1e-10 {
		tmp = -1.0 / denom
	}
	var bestGain [2]float32
	bestGain[0] = (2.0*gCoeff[2]*gCoeff[1] - gCoeff[3]*gCoeff[4]) * tmp
	bestGain[1] = (2.0*gCoeff[0]*gCoeff[3] - gCoeff[1]*gCoeff[4]) * tmp

	if tameFlag == 1 && bestGain[0] > params.GPCLIP2 {
		bestGain[0] = params.GPCLIP2
	}

	// 3. Pre-select candidate regions for GA (4 candidates) and GB (8 candidates).
	cand1, cand2 := GbkPresel(bestGain, gcode0)

	// 4. Search the 32 candidate gain pairs to minimize weighted error metric.
	distMin := float32(math.MaxFloat32)
	best1 := cand1
	best2 := cand2

	if tameFlag == 1 {
		for i := 0; i < params.NCAN1; i++ {
			k1 := cand1 + i
			for j := 0; j < params.NCAN2; j++ {
				k2 := cand2 + j
				gPitch := tables.GACodebook[k1][0] + tables.GBCodebook[k2][0]
				if gPitch < params.GP0999 {
					gCode := gcode0 * (tables.GACodebook[k1][1] + tables.GBCodebook[k2][1])
					dist := gPitch*gPitch*gCoeff[0] +
						gPitch*gCoeff[1] +
						gCode*gCode*gCoeff[2] +
						gCode*gCoeff[3] +
						gPitch*gCode*gCoeff[4]
					if dist < distMin {
						distMin = dist
						best1 = k1
						best2 = k2
					}
				}
			}
		}
	} else {
		for i := 0; i < params.NCAN1; i++ {
			k1 := cand1 + i
			for j := 0; j < params.NCAN2; j++ {
				k2 := cand2 + j
				gPitch := tables.GACodebook[k1][0] + tables.GBCodebook[k2][0]
				gCode := gcode0 * (tables.GACodebook[k1][1] + tables.GBCodebook[k2][1])
				dist := gPitch*gPitch*gCoeff[0] +
					gPitch*gCoeff[1] +
					gCode*gCode*gCoeff[2] +
					gCode*gCoeff[3] +
					gPitch*gCode*gCoeff[4]
				if dist < distMin {
					distMin = dist
					best1 = k1
					best2 = k2
				}
			}
		}
	}

	// 5. Reconstruct quantized gains.
	gainPit = tables.GACodebook[best1][0] + tables.GBCodebook[best2][0]
	gCodeFactor := tables.GACodebook[best1][1] + tables.GBCodebook[best2][1]
	gainCode = gCodeFactor * gcode0

	// 6. Update past quantized energy memory.
	GainUpdate(pastQuaEn, gCodeFactor)

	// 7. Map codebook entries to bitstream indices GA and GB.
	ga = int(tables.MapGA[best1])
	gb = int(tables.MapGB[best2])

	return ga, gb, gainPit, gainCode
}

// DequantizeGain decodes the pitch gain and fixed codebook gain from bitstream indices GA and GB,
// or performs gain attenuation and memory updates during frame erasures (bfi != 0).
//
// Arguments:
//   - ga: 3-bit gain index (0..7)
//   - gb: 4-bit gain index (0..15)
//   - code: innovation codevector (40 samples)
//   - pastQuaEn: history of past 4 quantized energies (modified in place)
//   - bfi: bad frame indicator (0 for good frame, 1 for frame erasure)
//   - prevGainPit: pointer to previous pitch gain (read and updated on erasure)
//   - prevGainCode: pointer to previous code gain (read and updated on erasure)
//
// Returns:
//   - gainPit: decoded adaptive codebook gain
//   - gainCode: decoded fixed codebook gain
func DequantizeGain(
	ga, gb int,
	code []float32,
	pastQuaEn *[params.MA_NP]float32,
	bfi int,
	prevGainPit *float32,
	prevGainCode *float32,
) (gainPit, gainCode float32) {
	// Frame erasure handling
	if bfi != 0 {
		if prevGainPit != nil {
			*prevGainPit *= 0.9
			if *prevGainPit > 0.9 {
				*prevGainPit = 0.9
			}
			gainPit = *prevGainPit
		}
		if prevGainCode != nil {
			*prevGainCode *= 0.98
			gainCode = *prevGainCode
		}
		GainUpdateErasure(pastQuaEn)
		return gainPit, gainCode
	}

	// Normal decode
	index1 := int(tables.InvMapGA[ga&7])
	index2 := int(tables.InvMapGB[gb&15])

	gainPit = tables.GACodebook[index1][0] + tables.GBCodebook[index2][0]

	gcode0 := GainPredict(pastQuaEn, code)
	gCodeFactor := tables.GACodebook[index1][1] + tables.GBCodebook[index2][1]
	gainCode = gCodeFactor * gcode0

	GainUpdate(pastQuaEn, gCodeFactor)

	if prevGainPit != nil {
		*prevGainPit = gainPit
	}
	if prevGainCode != nil {
		*prevGainCode = gainCode
	}

	return gainPit, gainCode
}

// ----------------------------------------------------------------------------
// Taming Functions (Error Propagation Control)
// ----------------------------------------------------------------------------

// InitExcErr initializes the 4-element error accumulation memory for pitch taming.
func InitExcErr(excErr *[4]float32) {
	for i := 0; i < 4; i++ {
		excErr[i] = 1.0
	}
}

// TestErr computes accumulated potential error in adaptive codebook contribution
// to decide whether pitch taming is necessary.
// Returns 1 if taming is needed, 0 otherwise.
func TestErr(t0, t0Frac int, excErr *[4]float32) int {
	t1 := t0
	if t0Frac > 0 {
		t1 = t0 + 1
	}

	i := t1 - params.L_SUBFR - params.L_INTER10
	if i < 0 {
		i = 0
	}
	zone1 := int(float32(i) * 0.025)

	i = t1 + params.L_INTER10 - 2
	zone2 := int(float32(i) * 0.025)

	maxLoc := float32(-1.0)
	for k := zone2; k >= zone1; k-- {
		if k >= 0 && k < 4 && excErr[k] > maxLoc {
			maxLoc = excErr[k]
		}
	}

	if maxLoc > params.THRESH_ERR {
		return 1
	}
	return 0
}

// UpdateExcErr updates the error accumulation memory after pitch gain is quantized.
func UpdateExcErr(gainPit float32, t0 int, excErr *[4]float32) {
	worst := float32(-1.0)
	n := t0 - params.L_SUBFR
	if n < 0 {
		temp := float32(1.0) + gainPit*excErr[0]
		if temp > worst {
			worst = temp
		}
		temp = float32(1.0) + gainPit*temp
		if temp > worst {
			worst = temp
		}
	} else {
		zone1 := int(float32(n) * 0.025)
		i := t0 - 1
		zone2 := int(float32(i) * 0.025)
		for k := zone1; k <= zone2; k++ {
			if k >= 0 && k < 4 {
				temp := float32(1.0) + gainPit*excErr[k]
				if temp > worst {
					worst = temp
				}
			}
		}
	}

	for i := 3; i >= 1; i-- {
		excErr[i] = excErr[i-1]
	}
	excErr[0] = worst
}
