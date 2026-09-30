package vad

import (
	"math"

	"github.com/selawe/go-g729/internal/codebook"
	"github.com/selawe/go-g729/internal/lsp"
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/pitch"
	"github.com/selawe/go-g729/internal/tables"
)

// RandomG729C implements the ITU-T G.729 Annex B linear congruential PRNG:
//
//	seed = seed * 31821 + 13849 (mod 65536)
//
// The int16 cast provides the implicit mod 65536 required by the spec.
// This generator is NOT cryptographically secure; it is intended solely
// for comfort-noise and CNG excitation generation within the codec.
func RandomG729C(seed *int16) int16 {
	*seed = int16(int32(*seed)*31821 + 13849)
	return *seed
}

// Gauss generates standard normally distributed N(0, 1) pseudo-random numbers
// by summing 12 uniform random variables using the Central Limit Theorem.
func Gauss(seed *int16) float32 {
	var lAcc int32
	for i := 0; i < 12; i++ {
		lAcc += int32(RandomG729C(seed))
	}
	lAcc >>= 7
	temp := int16(lAcc)
	return float32(temp) * 0.001953125 // temp / 512.0
}

// CalcExcRand generates pseudo-random comfort noise excitation for inactive / SID frames.
func CalcExcRand(
	curGain float32,
	excBuf []float32,
	excOffset int,
	seed *int16,
	isEncoder bool,
	excErr *[4]float32,
) {
	if curGain == 0.0 {
		for i := 0; i < params.L_FRAME; i++ {
			excBuf[excOffset+i] = 0.0
		}
		if isEncoder && excErr != nil {
			for iSubfr := 0; iSubfr < params.L_FRAME; iSubfr += params.L_SUBFR {
				codebook.UpdateExcErr(0.0, params.L_SUBFR+1, excErr)
			}
		}
		return
	}

	for iSubfr := 0; iSubfr < params.L_FRAME; iSubfr += params.L_SUBFR {
		subfrOffset := excOffset + iSubfr

		// Generate random pitch lag, fraction, pulse positions, and signs
		temp1 := RandomG729C(seed)
		frac := int(temp1&0x0003) - 1
		if frac == 2 {
			frac = 0
		}
		temp1 >>= 2
		t0 := int(temp1&0x003F) + 40
		temp1 >>= 6

		temp2 := int16(temp1 & 0x0007)
		pos0 := 5 * int(temp2)
		temp1 >>= 3
		temp2 = int16(temp1 & 0x0001)
		sign0 := 2.0*float32(temp2) - 1.0

		temp1 >>= 1
		temp2 = int16(temp1 & 0x0007)
		pos1 := 5*int(temp2) + 1
		temp1 >>= 3
		temp2 = int16(temp1 & 0x0001)
		sign1 := 2.0*float32(temp2) - 1.0

		temp1 = RandomG729C(seed)
		temp2 = int16(temp1 & 0x0007)
		pos2 := 5*int(temp2) + 2
		temp1 >>= 3
		temp2 = int16(temp1 & 0x0001)
		sign2 := 2.0*float32(temp2) - 1.0

		temp1 >>= 1
		temp2 = int16(temp1 & 0x000F)
		pos3 := int(temp2&0x0001) + 3
		temp2 >>= 1
		temp2 &= 0x0007
		pos3 += 5 * int(temp2)
		temp1 >>= 4
		temp2 = int16(temp1 & 0x0001)
		sign3 := 2.0*float32(temp2) - 1.0

		gpInt := int16(RandomG729C(seed) & 0x1FFF)
		gp := float32(gpInt) / 16384.0

		// Generate Gaussian excitation
		var excg [params.L_SUBFR]float32
		var ener float32
		for i := 0; i < params.L_SUBFR; i++ {
			g := Gauss(seed)
			excg[i] = g
			ener += g * g
		}

		fact := params.NORM_GAUSS * curGain / float32(math.Sqrt(float64(ener)))
		for i := 0; i < params.L_SUBFR; i++ {
			excg[i] *= fact
		}

		// Generate adaptive excitation via fractional sinc interpolation
		pitch.InterpExcitation(excBuf, subfrOffset, t0, frac, params.L_SUBFR)

		// Combine adaptive + Gaussian excitation into excBuf
		ener = 0.0
		for i := 0; i < params.L_SUBFR; i++ {
			v := excBuf[subfrOffset+i]*gp + excg[i]
			excBuf[subfrOffset+i] = v
			ener += v * v
		}

		// Fixed code gain solution EQ(X) = 4 X^2 + 2 b X + c
		interExc := excBuf[subfrOffset+pos0]*sign0 +
			excBuf[subfrOffset+pos1]*sign1 +
			excBuf[subfrOffset+pos2]*sign2 +
			excBuf[subfrOffset+pos3]*sign3

		k := curGain * curGain * float32(params.L_SUBFR)
		delta := interExc*interExc - 4.0*(ener-k)

		if delta < 0.0 {
			copy(excBuf[subfrOffset:subfrOffset+params.L_SUBFR], excg[:])
			interExc = excBuf[subfrOffset+pos0]*sign0 +
				excBuf[subfrOffset+pos1]*sign1 +
				excBuf[subfrOffset+pos2]*sign2 +
				excBuf[subfrOffset+pos3]*sign3
			delta = interExc*interExc + params.K0*k
			gp = 0.0
		}

		sqrtDelta := float32(math.Sqrt(float64(delta)))
		x1 := (sqrtDelta - interExc) * 0.25
		x2 := -(sqrtDelta + interExc) * 0.25
		g := x1
		if math.Abs(float64(x2)) < math.Abs(float64(x1)) {
			g = x2
		}
		if g >= 0.0 {
			if g > params.G_MAX {
				g = params.G_MAX
			}
		} else {
			if g < -params.G_MAX {
				g = -params.G_MAX
			}
		}

		// Add ACELP pulses
		excBuf[subfrOffset+pos0] += g * sign0
		excBuf[subfrOffset+pos1] += g * sign1
		excBuf[subfrOffset+pos2] += g * sign2
		excBuf[subfrOffset+pos3] += g * sign3

		if isEncoder && excErr != nil {
			codebook.UpdateExcErr(gp, t0, excErr)
		}
	}
}

// CNGDecoderState maintains Comfort Noise Generation state at the decoder.
type CNGDecoderState struct {
	CurGain float32
	LspSid  [params.M]float32
	SidGain float32
	Seed    int16
}

// NewCNGDecoderState creates and initializes a CNG decoder state struct.
func NewCNGDecoderState() *CNGDecoderState {
	s := &CNGDecoderState{}
	s.Reset()
	return s
}

// Reset re-initializes CNG decoder state according to ITU-T init_dec_cng().
func (s *CNGDecoderState) Reset() {
	for i := 0; i < params.M; i++ {
		s.LspSid[i] = float32(math.Cos(float64(tables.FreqPrevReset[i])))
	}
	s.SidGain = tables.TabSidGain[0]
	s.CurGain = 0.0
	s.Seed = params.INIT_SEED
}

// DecCNG performs comfort noise decoding and excitation generation at the decoder for SID and untransmitted frames.
func (s *CNGDecoderState) DecCNG(
	pastIsSpeech bool,
	sidSav float32,
	sidParams SIDParams,
	excBuf []float32,
	excOffset int,
	lspOld *[params.M]float32,
	aT *[2 * params.MP1]float32,
	freqPrev *[params.MA_NP][params.M]float32,
) {
	if sidParams.Transmitted {
		s.SidGain = tables.TabSidGain[sidParams.GainIndex]
		SidLsfqDecode(sidParams.Mode, sidParams.L1, sidParams.L2, &s.LspSid, freqPrev)
	} else {
		// Untransmitted frame
		// In case first SID frame was lost / erased: recover SID gain
		if pastIsSpeech {
			_, ind := QuaSidgain([]float32{sidSav}, 0)
			s.SidGain = tables.TabSidGain[ind]
		}
	}

	if pastIsSpeech {
		s.CurGain = s.SidGain
	} else {
		s.CurGain = s.CurGain*params.A_GAIN0 + s.SidGain*params.A_GAIN1
	}

	CalcExcRand(s.CurGain, excBuf, excOffset, &s.Seed, false, nil)

	// Interpolate LSP vectors to produce LP coefficients for both subframes
	a1, a2 := lsp.InterpolateLSP(*lspOld, s.LspSid)
	copy(aT[:params.MP1], a1[:])
	copy(aT[params.MP1:], a2[:])
	copy(lspOld[:], s.LspSid[:])
}
