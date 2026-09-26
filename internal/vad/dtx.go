package vad

import (
	"math"

	"github.com/selawe/go-g729/internal/lsp"
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// SIDParams holds the coded parameters for a Silence Insertion Descriptor (SID) frame.
type SIDParams struct {
	Transmitted bool    // True if SID frame must be transmitted, false if untransmitted
	Mode        int     // Switched MA predictor mode (1 bit: 0 or 1)
	L1          int     // 1st stage codebook index (5 bits: 0..31)
	L2          int     // 2nd stage codebook index (4 bits: 0..15)
	GainIndex   int     // SID energy gain index (5 bits: 0..31)
	Packed      [2]byte // 16-bit octet transmission bitstream
}

// DTXEncoderState encapsulates the encoder state and memory for Discontinuous Transmission (DTX).
type DTXEncoderState struct {
	LspSidQ    [params.M]float32
	PastCoeff  [params.MP1]float32
	RCoeff     [params.MP1]float32
	Acf        [params.NB_CURACF * params.MP1]float32 // 2 * 11 = 22
	SumAcf     [params.NB_SUMACF * params.MP1]float32 // 3 * 11 = 33
	Ener       [params.NB_GAIN]float32                // 2
	FrCur      int
	CurGain    float32
	NbEner     int
	SidGain    float32
	FlagChang  int
	PrevEnergy float32
	CountFr0   int
	OldA       [params.MP1]float32
	OldRc      [2]float32
}

// NewDTXEncoderState creates and initializes a DTX encoder state.
func NewDTXEncoderState() *DTXEncoderState {
	s := &DTXEncoderState{}
	s.Reset()
	return s
}

// Reset re-initializes DTX encoder variables per ITU-T init_cod_cng().
func (s *DTXEncoderState) Reset() {
	for i := range s.LspSidQ {
		s.LspSidQ[i] = 0.0
	}
	for i := range s.PastCoeff {
		s.PastCoeff[i] = 0.0
	}
	s.PastCoeff[0] = 1.0
	for i := range s.RCoeff {
		s.RCoeff[i] = 0.0
	}
	for i := range s.Acf {
		s.Acf[i] = 0.0
	}
	for i := range s.SumAcf {
		s.SumAcf[i] = 0.0
	}
	for i := range s.Ener {
		s.Ener[i] = 0.0
	}
	s.FrCur = 0
	s.CurGain = 0.0
	s.NbEner = 0
	s.SidGain = tables.TabSidGain[0]
	s.FlagChang = 0
	s.PrevEnergy = 0.0
	s.CountFr0 = 0

	s.OldA[0] = 1.0
	for i := 1; i < len(s.OldA); i++ {
		s.OldA[i] = 0.0
	}
	s.OldRc[0] = 0.0
	s.OldRc[1] = 0.0
}

// UpdateCNG updates the autocorrelation arrays used for DTX / CNG decision.
// Should be called every frame after autocorrelation analysis.
func (s *DTXEncoderState) UpdateCNG(r []float32, vad int) {
	// Shift Acf by 1 frame (MP1 = 11 floats)
	for i := len(s.Acf) - 1; i >= params.MP1; i-- {
		s.Acf[i] = s.Acf[i-params.MP1]
	}

	// Save current autocorrelation
	for i := 0; i < params.MP1; i++ {
		s.Acf[i] = r[i]
	}

	s.FrCur++
	if s.FrCur == params.NB_CURACF {
		s.FrCur = 0
		if vad != 0 {
			s.updateSumAcf()
		}
	}
}

// CodCNG computes the DTX decision, encodes SID parameters if needed, synthesizes comfort
// noise excitation, and updates the LPC predictor filters and excitation buffers.
func (s *DTXEncoderState) CodCNG(
	excBuf []float32,
	excOffset int,
	pastVad int,
	lspOldQ *[params.M]float32,
	aq *[2 * params.MP1]float32,
	freqPrev *[params.MA_NP][params.M]float32,
	seed *int16,
	excErr *[4]float32,
) SIDParams {
	var curAcf [params.MP1]float32
	var curCoeff [params.MP1]float32
	var bid [params.M]float32
	var lspNew [params.M]float32

	// 1. Shift energy history
	for i := params.NB_GAIN - 1; i >= 1; i-- {
		s.Ener[i] = s.Ener[i-1]
	}

	// 2. Compute current accumulated autocorrelations
	s.calcSumAcf(s.Acf[:], curAcf[:], params.NB_CURACF)

	// 3. Compute LPC coefficients and residual energy
	if curAcf[0] == 0.0 {
		s.Ener[0] = 0.0
	} else {
		s.Ener[0] = LevinsonSID(curAcf[:], params.M, &curCoeff, &bid, &s.OldA, &s.OldRc)
	}

	var res SIDParams
	var energyQ float32
	var curIGain int

	// 4. Transmission decision
	if pastVad != 0 {
		// First frame of silence -> Always transmit SID frame
		res.Transmitted = true
		s.CountFr0 = 0
		s.NbEner = 1
		energyQ, curIGain = QuaSidgain(s.Ener[:], s.NbEner)
	} else {
		s.NbEner++
		if s.NbEner > params.NB_GAIN {
			s.NbEner = params.NB_GAIN
		}
		energyQ, curIGain = QuaSidgain(s.Ener[:], s.NbEner)

		// Check spectral stationarity vs reference filter
		if s.cmpFilt(s.RCoeff[:], curAcf[:], s.Ener[0], params.THRESH1) != 0 {
			s.FlagChang = 1
		}

		// Check energy difference vs last frame
		if float32(math.Abs(float64(s.PrevEnergy-energyQ))) > 2.0 {
			s.FlagChang = 1
		}

		s.CountFr0++
		if s.CountFr0 < params.FR_SID_MIN {
			res.Transmitted = false
		} else {
			if s.FlagChang != 0 {
				res.Transmitted = true
			} else {
				res.Transmitted = false
			}
			s.CountFr0 = params.FR_SID_MIN
		}
	}

	// 5. If SID frame is to be transmitted, encode parameters
	if res.Transmitted {
		s.CountFr0 = 0
		s.FlagChang = 0

		// Compute past average filter
		s.calcPastFilt(&s.PastCoeff)
		s.calcRCoeff(s.PastCoeff[:], s.RCoeff[:])

		var lpcCoeff *[params.MP1]float32
		if s.cmpFilt(s.RCoeff[:], curAcf[:], s.Ener[0], params.THRESH2) == 0 {
			// Stationary: transmit past average filter
			lpcCoeff = &s.PastCoeff
		} else {
			// Non-stationary: transmit current filter
			lpcCoeff = &curCoeff
			s.calcRCoeff(curCoeff[:], s.RCoeff[:])
		}

		// Convert A(z) to LSP
		lspNew, _ = lsp.LPC2LSP(lpcCoeff, lspOldQ)

		// Quantize noise LSFs
		mode, l1, l2 := LsfqNoise(lspNew[:], &s.LspSidQ, freqPrev)
		res.Mode = mode
		res.L1 = l1
		res.L2 = l2
		res.GainIndex = curIGain

		s.PrevEnergy = energyQ
		s.SidGain = tables.TabSidGain[curIGain]
	}

	// 6. Update gain & generate comfort noise excitation
	if pastVad != 0 {
		s.CurGain = s.SidGain
	} else {
		s.CurGain = s.CurGain*params.A_GAIN0 + s.SidGain*params.A_GAIN1
	}

	CalcExcRand(s.CurGain, excBuf, excOffset, seed, true, excErr)

	// 7. Interpolate quantized LPC coefficients
	a1, a2 := lsp.InterpolateLSP(*lspOldQ, s.LspSidQ)
	copy(aq[:params.MP1], a1[:])
	copy(aq[params.MP1:], a2[:])
	for i := 0; i < params.M; i++ {
		lspOldQ[i] = s.LspSidQ[i]
	}

	// 8. Update sumAcf if frCur == 0
	if s.FrCur == 0 {
		s.updateSumAcf()
	}

	// Pack SID bitstream if transmitted
	if res.Transmitted {
		res.Packed = PackSID(res)
	}

	return res
}

// PackSID serializes SID parameters into standard 2-byte octet-mode format.
// Bit layout (MSB to LSB):
//   - bit 15: Mode (1 bit)
//   - bits 14..10: L1 (5 bits)
//   - bits 9..6: L2 (4 bits)
//   - bits 5..1: GainIndex (5 bits)
//   - bit 0: 0 (1 bit padding)
func PackSID(p SIDParams) [2]byte {
	v := (uint16(p.Mode&1) << 15) |
		(uint16(p.L1&0x1F) << 10) |
		(uint16(p.L2&0x0F) << 6) |
		(uint16(p.GainIndex&0x1F) << 1)

	return [2]byte{
		byte(v >> 8),
		byte(v & 0xFF),
	}
}

// UnpackSID deserializes a 2-byte bitstream into SID parameters.
func UnpackSID(data [2]byte) SIDParams {
	v := (uint16(data[0]) << 8) | uint16(data[1])
	return SIDParams{
		Transmitted: true,
		Mode:        int((v >> 15) & 1),
		L1:          int((v >> 10) & 0x1F),
		L2:          int((v >> 6) & 0x0F),
		GainIndex:   int((v >> 1) & 0x1F),
		Packed:      data,
	}
}

// QuaSidgain quantizes the average excitation energy into an index and decoded energy in dB.
func QuaSidgain(ener []float32, nbEner int) (enerQ float32, idx int) {
	var avrEner float32
	if nbEner == 0 {
		avrEner = ener[0] * tables.Fact[0]
	} else {
		for i := 0; i < nbEner; i++ {
			avrEner += ener[i]
		}
		avrEner *= tables.Fact[nbEner]
	}
	return QuantEnergy(avrEner)
}

// QuantEnergy performs scalar quantization of SID frame energy in dB.
func QuantEnergy(ener float32) (enerQ float32, idx int) {
	if ener <= params.MIN_ENER {
		return -12.0, 0
	}

	enerDB := float32(10.0 * math.Log10(float64(ener)))
	if enerDB <= -8.0 {
		return -12.0, 0
	}
	if enerDB >= 65.0 {
		return 66.0, 31
	}

	if enerDB <= 14.0 {
		index := int((enerDB + 10.0) * 0.25)
		if index < 1 {
			index = 1
		}
		enerQ = 4.0*float32(index) - 8.0
		return enerQ, index
	}

	index := int((enerDB - 3.0) * 0.5)
	if index < 6 {
		index = 6
	}
	enerQ = 2.0*float32(index) + 4.0
	return enerQ, index
}

// LsfqNoise quantizes noise line spectral frequencies for SID frames.
func LsfqNoise(
	lspIn []float32,
	lspQ *[params.M]float32,
	freqPrev *[params.MA_NP][params.M]float32,
) (mode, l1, l2 int) {
	var lsf [params.M]float32
	for i := 0; i < params.M; i++ {
		v := float64(lspIn[i])
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		lsf[i] = float32(math.Acos(v))
	}

	// Enforce spacing ~100 Hz
	if lsf[0] < lsp.LLimit {
		lsf[0] = lsp.LLimit
	}
	for i := 0; i < params.M-1; i++ {
		if lsf[i+1]-lsf[i] < 2.0*lsp.Gap3 {
			lsf[i+1] = lsf[i] + 2.0*lsp.Gap3
		}
	}
	if lsf[params.M-1] > lsp.MLimit {
		lsf[params.M-1] = lsp.MLimit
	}
	if lsf[params.M-1] < lsf[params.M-2] {
		lsf[params.M-2] = lsf[params.M-1] - lsp.Gap3
	}

	// Compute perceptual weights
	weight := lsp.ComputeLSFWeights(lsf)

	// Extract prediction error vector for both MA modes
	var errlsf [2 * params.M]float32
	for m := 0; m < 2; m++ {
		for j := 0; j < params.M; j++ {
			ele := lsf[j]
			for k := 0; k < params.MA_NP; k++ {
				ele -= freqPrev[k][j] * tables.NoiseMAPredictor[m][k][j]
			}
			errlsf[m*params.M+j] = ele * tables.NoiseFgSumInv[m][j]
		}
	}

	var tmpbuf [params.M]float32
	mode, l1, l2 = qntE(errlsf[:], weight[:], &tmpbuf)

	// Enforce minimum distance of 0.0012
	lsp.Expand12(tmpbuf[:], 0.0012)

	// Compose quantized LSF
	var lsfQ [params.M]float32
	for j := 0; j < params.M; j++ {
		lsfQ[j] = tmpbuf[j] * tables.NoiseFgSum[mode][j]
		for k := 0; k < params.MA_NP; k++ {
			lsfQ[j] += freqPrev[k][j] * tables.NoiseMAPredictor[mode][k][j]
		}
	}

	// Update MA memory
	for k := params.MA_NP - 1; k > 0; k-- {
		copy(freqPrev[k][:], freqPrev[k-1][:])
	}
	copy(freqPrev[0][:], tmpbuf[:])

	// Check stability
	lsp.StabilizeLSF(&lsfQ, lsp.Gap3)

	// Convert to cosine domain
	for i := 0; i < params.M; i++ {
		lspQ[i] = float32(math.Cos(float64(lsfQ[i])))
	}

	return mode, l1, l2
}

// SidLsfqDecode decodes quantized noise LSFs from SID parameter indices.
func SidLsfqDecode(
	mode, l1, l2 int,
	lspQ *[params.M]float32,
	freqPrev *[params.MA_NP][params.M]float32,
) {
	var tmpbuf [params.M]float32

	cb1Idx := tables.PtrTab1[l1]
	cb2LowerIdx := tables.PtrTab2[0][l2]
	cb2UpperIdx := tables.PtrTab2[1][l2]

	for i := 0; i < params.M/2; i++ {
		tmpbuf[i] = tables.LSP_L1[cb1Idx][i] + tables.LSP_L2[cb2LowerIdx][i]
	}
	for i := params.M / 2; i < params.M; i++ {
		tmpbuf[i] = tables.LSP_L1[cb1Idx][i] + tables.LSP_L3[cb2UpperIdx][i-params.M/2]
	}

	// Minimum distance 0.0012
	lsp.Expand12(tmpbuf[:], 0.0012)

	// Compose quantized LSF
	var lsfQ [params.M]float32
	for j := 0; j < params.M; j++ {
		lsfQ[j] = tmpbuf[j] * tables.NoiseFgSum[mode][j]
		for k := 0; k < params.MA_NP; k++ {
			lsfQ[j] += freqPrev[k][j] * tables.NoiseMAPredictor[mode][k][j]
		}
	}

	// Update memory
	for k := params.MA_NP - 1; k > 0; k-- {
		copy(freqPrev[k][:], freqPrev[k-1][:])
	}
	copy(freqPrev[0][:], tmpbuf[:])

	// Stabilize
	lsp.StabilizeLSF(&lsfQ, lsp.Gap3)

	for i := 0; i < params.M; i++ {
		lspQ[i] = float32(math.Cos(float64(lsfQ[i])))
	}
}

// LevinsonSID runs Levinson-Durbin recursion for SID frame analysis with fallback to last stable filter.
// Returns the prediction error (residual energy).
func LevinsonSID(
	r []float32,
	m int,
	a *[params.MP1]float32,
	rc *[params.M]float32,
	oldA *[params.MP1]float32,
	oldRc *[2]float32,
) float32 {
	r1 := r[1]
	if r1 > r[0] {
		r1 = r[0]
	}
	rc0 := -r1 / r[0]
	a[0] = 1.0
	a[1] = rc0
	rc[0] = rc0
	err := r[0] + r1*rc0

	for i := 2; i <= m; i++ {
		var s float32
		for j := 0; j < i; j++ {
			s += r[i-j] * a[j]
		}
		if err != 0.0 {
			rc[i-1] = -s / err
		} else {
			rc[i-1] = 1.0
		}

		// Test for unstable filter: if unstable keep old A(z)
		if float32(math.Abs(float64(rc[i-1]))) > 0.999451 {
			for j := 0; j <= m; j++ {
				a[j] = oldA[j]
			}
			rc[0] = oldRc[0]
			rc[1] = oldRc[1]
			return 0.001
		}

		half := i / 2
		for j := 1; j <= half; j++ {
			l := i - j
			at := a[j] + rc[i-1]*a[l]
			a[l] += rc[i-1] * a[j]
			a[j] = at
		}
		a[i] = rc[i-1]
		err += rc[i-1] * s
		if err <= 0.0 {
			err = 0.001
		}
	}

	for j := 0; j <= m; j++ {
		oldA[j] = a[j]
	}
	oldRc[0] = rc[0]
	oldRc[1] = rc[1]

	return err
}

// Local helper methods

func (s *DTXEncoderState) calcPastFilt(coeff *[params.MP1]float32) {
	var sSumAcf [params.MP1]float32
	var bid [params.M]float32

	s.calcSumAcf(s.SumAcf[:], sSumAcf[:], params.NB_SUMACF)
	if sSumAcf[0] == 0.0 {
		coeff[0] = 1.0
		for i := 1; i <= params.M; i++ {
			coeff[i] = 0.0
		}
		return
	}

	LevinsonSID(sSumAcf[:], params.M, coeff, &bid, &s.OldA, &s.OldRc)
}

func (s *DTXEncoderState) calcRCoeff(coeff []float32, rCoeff []float32) {
	var temp float32
	for j := 0; j <= params.M; j++ {
		temp += coeff[j] * coeff[j]
	}
	rCoeff[0] = temp

	for i := 1; i <= params.M; i++ {
		temp = 0.0
		for j := 0; j <= params.M-i; j++ {
			temp += coeff[j] * coeff[j+i]
		}
		rCoeff[i] = 2.0 * temp
	}
}

func (s *DTXEncoderState) cmpFilt(rCoeff []float32, acf []float32, alpha float32, thresh float32) int {
	var temp1 float32
	for i := 0; i <= params.M; i++ {
		temp1 += rCoeff[i] * acf[i]
	}
	temp2 := alpha * thresh
	if temp1 > temp2 {
		return 1
	}
	return 0
}

func (s *DTXEncoderState) calcSumAcf(acf []float32, sum []float32, nb int) {
	for j := 0; j < params.MP1; j++ {
		sum[j] = 0.0
	}
	for i := 0; i < nb; i++ {
		base := i * params.MP1
		for j := 0; j < params.MP1; j++ {
			sum[j] += acf[base+j]
		}
	}
}

func (s *DTXEncoderState) updateSumAcf() {
	// Shift sumAcf
	for i := len(s.SumAcf) - 1; i >= params.MP1; i-- {
		s.SumAcf[i] = s.SumAcf[i-params.MP1]
	}
	// Compute new sumAcf
	s.calcSumAcf(s.Acf[:], s.SumAcf[:params.MP1], params.NB_CURACF)
}

func qntE(errlsf []float32, weight []float32, qlsf *[params.M]float32) (mode, c0, c1 int) {
	var dData0 [4 * params.M]float32
	var dData1 [params.M]float32
	var bestIndx0 [4]int
	var bestIndx1 [1]int
	var ptrBack0 [4]int
	var ptrBack1 [1]int

	newMLSearch1(errlsf, 2, dData0[:], 4, bestIndx0[:], ptrBack0[:], tables.PtrTab1[:], 32)
	newMLSearch2(dData0[:], weight, 4, dData1[:], 1, bestIndx1[:], ptrBack0[:], ptrBack1[:], tables.PtrTab2, 16)

	c1 = bestIndx1[0]
	ptr := ptrBack1[0]
	c0 = bestIndx0[ptr]
	mode = ptrBack0[ptr]

	cb1Idx := tables.PtrTab1[c0]
	cb2LowerIdx := tables.PtrTab2[0][c1]
	cb2UpperIdx := tables.PtrTab2[1][c1]

	for i := 0; i < params.M/2; i++ {
		qlsf[i] = tables.LSP_L1[cb1Idx][i] + tables.LSP_L2[cb2LowerIdx][i]
	}
	for i := params.M / 2; i < params.M; i++ {
		qlsf[i] = tables.LSP_L1[cb1Idx][i] + tables.LSP_L3[cb2UpperIdx][i-params.M/2]
	}

	return mode, c0, c1
}

func newMLSearch1(
	dData []float32,
	j int,
	newDData []float32,
	k int,
	bestIndx []int,
	ptrBack []int,
	ptrTab []int,
	mq int,
) {
	var sum [2 * 32]float32
	var minVal [4]float32
	var minIndxP [4]int
	var minIndxM [4]int

	for q := 0; q < k; q++ {
		minVal[q] = math.MaxFloat32
	}

	for p := 0; p < j; p++ {
		for m := 0; m < mq; m++ {
			var s float32
			cbIdx := ptrTab[m]
			pBase := p * params.M
			for l := 0; l < params.M; l++ {
				diff := dData[pBase+l] - tables.LSP_L1[cbIdx][l]
				s += diff * diff
			}
			sum[p*mq+m] = s * tables.NoiseMp[p]
		}
	}

	for q := 0; q < k; q++ {
		for p := 0; p < j; p++ {
			for m := 0; m < mq; m++ {
				idx := p*mq + m
				if sum[idx] < minVal[q] {
					minVal[q] = sum[idx]
					minIndxP[q] = p
					minIndxM[q] = m
				}
			}
		}
		sum[minIndxP[q]*mq+minIndxM[q]] = math.MaxFloat32
	}

	for q := 0; q < k; q++ {
		cbIdx := ptrTab[minIndxM[q]]
		pBase := minIndxP[q] * params.M
		qBase := q * params.M
		for l := 0; l < params.M; l++ {
			newDData[qBase+l] = dData[pBase+l] - tables.LSP_L1[cbIdx][l]
		}
		ptrBack[q] = minIndxP[q]
		bestIndx[q] = minIndxM[q]
	}
}

func newMLSearch2(
	dData []float32,
	weight []float32,
	j int,
	newDData []float32,
	k int,
	bestIndx []int,
	ptrPrd []int,
	ptrBack []int,
	ptrTab [2][16]int,
	mq int,
) {
	var sum [4 * 16]float32
	var minVal [4]float32
	var minIndxP [4]int
	var minIndxM [4]int

	for q := 0; q < k; q++ {
		minVal[q] = math.MaxFloat32
	}

	for p := 0; p < j; p++ {
		prdMode := ptrPrd[p]
		pBase := p * params.M
		for m := 0; m < mq; m++ {
			var s float32
			cbLowerIdx := ptrTab[0][m]
			cbUpperIdx := ptrTab[1][m]

			for l := 0; l < params.M/2; l++ {
				fgSum := tables.NoiseFgSum[prdMode][l]
				diff := dData[pBase+l] - tables.LSP_L2[cbLowerIdx][l]
				s += weight[l] * (fgSum * fgSum) * (diff * diff)
			}
			for l := params.M / 2; l < params.M; l++ {
				fgSum := tables.NoiseFgSum[prdMode][l]
				diff := dData[pBase+l] - tables.LSP_L3[cbUpperIdx][l-params.M/2]
				s += weight[l] * (fgSum * fgSum) * (diff * diff)
			}
			sum[p*mq+m] = s
		}
	}

	for q := 0; q < k; q++ {
		for p := 0; p < j; p++ {
			for m := 0; m < mq; m++ {
				idx := p*mq + m
				if sum[idx] < minVal[q] {
					minVal[q] = sum[idx]
					minIndxP[q] = p
					minIndxM[q] = m
				}
			}
		}
		sum[minIndxP[q]*mq+minIndxM[q]] = math.MaxFloat32
	}

	for q := 0; q < k; q++ {
		cbLowerIdx := ptrTab[0][minIndxM[q]]
		cbUpperIdx := ptrTab[1][minIndxM[q]]
		pBase := minIndxP[q] * params.M
		qBase := q * params.M

		for l := 0; l < params.M/2; l++ {
			newDData[qBase+l] = dData[pBase+l] - tables.LSP_L2[cbLowerIdx][l]
		}
		for l := params.M / 2; l < params.M; l++ {
			newDData[qBase+l] = dData[pBase+l] - tables.LSP_L3[cbUpperIdx][l-params.M/2]
		}
		ptrBack[q] = minIndxP[q]
		bestIndx[q] = minIndxM[q]
	}
}
