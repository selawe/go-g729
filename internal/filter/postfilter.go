package filter

import (
	"math"

	"github.com/selawe/go-g729/internal/dsp"
	"github.com/selawe/go-g729/internal/params"
)

const (
	Gamma2Pst = float32(0.55) // Formant postfilter factor (numerator)
	Gamma1Pst = float32(0.70) // Formant postfilter factor (denominator)
	Gammap    = float32(0.50) // Harmonic postfilter factor
	InvGammap = float32(1.0 / 1.5)
	Gammap2   = float32(0.5 / 1.5)
	MuTilt    = float32(0.8)  // Tilt compensation factor
	AgcFac    = float32(0.9)  // Factor for automatic gain control
	AgcFac1   = float32(1.0 - 0.9)
	LH        = 22            // Size of truncated impulse response of A(z/gamma2_pst)/A(z/gamma1_pst)
)

// PostFilterState holds the internal history and filter states for adaptive postfiltering.
// Instances should be created via NewPostFilterState().
type PostFilterState struct {
	// res2Buf contains PIT_MAX (143) samples of past residual + L_SUBFR (40) current samples = 183 samples.
	res2Buf [params.PIT_MAX + params.L_SUBFR]float32

	// memRes holds the last M samples of the synthesis filter output for the A(z/gamma2_pst)
	// residue filter history. The C reference achieves this via buffer indexing (syn[-M..-1]);
	// here we carry it explicitly across subframe/frame boundaries.
	memRes [params.M]float32

	// memSynPst holds the 10-sample filter memory for short-term synthesis filter 1/A(z/gamma1_pst).
	memSynPst [params.M]float32

	// memPre holds the 1-sample filter memory for tilt compensation filter (1 - mu*z^-1).
	memPre float32

	// pastGain holds the smoothed AGC gain across subframes (initialized to 1.0).
	pastGain float32
}

// NewPostFilterState creates and initializes a new PostFilterState.
func NewPostFilterState() *PostFilterState {
	s := &PostFilterState{}
	s.Reset()
	return s
}

// Reset clears all memories and initializes pastGain to 1.0.
func (s *PostFilterState) Reset() {
	for i := range s.res2Buf {
		s.res2Buf[i] = 0
	}
	for i := range s.memRes {
		s.memRes[i] = 0
	}
	for i := range s.memSynPst {
		s.memSynPst[i] = 0
	}
	s.memPre = 0
	s.pastGain = 1.0
}

// PostFilter performs adaptive postfiltering on one frame of synthesized speech (80 samples)
// in-place according to ITU-T G.729 / G.729A.
//
// The postfiltering stages are:
//  1. Inverse filtering through A(z/gamma2_pst) to obtain residual res2.
//  2. Pitch postfiltering on res2 (long-term harmonic enhancement).
//  3. Spectral tilt compensation (first-order pre-emphasis filter).
//  4. Short-term formant synthesis filtering through 1/A(z/gamma1_pst).
//  5. Adaptive gain control (AGC) to match output energy to input energy.
//
// Arguments:
//   - syn: synthesized speech buffer (80 samples, modified in-place with postfiltered speech)
//   - az: interpolated LP filter coefficients for both subframes (2 * (M+1) = 22 floats)
//   - pitchLags: decoded integer pitch lags for subframe 1 and subframe 2
//   - state: persistent postfilter state
func PostFilter(syn []float32, az []float32, pitchLags [2]int, state *PostFilterState) {
	PostFilterB(syn, az, pitchLags, 1, state)
}

// PostFilterB performs adaptive postfiltering on one frame of synthesized speech (80 samples)
// in-place according to ITU-T G.729 / G.729A / Annex B.
// When vad == 0 (non-active speech / comfort noise), the pitch postfilter is bypassed.
func PostFilterB(syn []float32, az []float32, pitchLags [2]int, vad int, state *PostFilterState) {
	var synPst [params.L_FRAME]float32
	var res2Pst [params.L_SUBFR]float32
	var ap3, ap4 [params.M + 1]float32
	var h [LH]float32
	var zeroMem [params.M]float32

	for iSubfr := 0; iSubfr < params.L_FRAME; iSubfr += params.L_SUBFR {
		subfrIdx := iSubfr / params.L_SUBFR
		azSubfr := az[subfrIdx*(params.M+1) : (subfrIdx+1)*(params.M+1)]

		// 1. Pitch search range around decoded pitch lag [t0 - 3, t0 + 3]
		t0Min := pitchLags[subfrIdx] - 3
		t0Max := t0Min + 6
		if t0Max > params.PIT_MAX {
			t0Max = params.PIT_MAX
			t0Min = t0Max - 6
		}
		if t0Min < params.PIT_MIN {
			t0Min = params.PIT_MIN
			t0Max = t0Min + 6
		}

		// 2. Compute bandwidth-expanded coefficients ap3 = A(z/gamma2_pst) and ap4 = A(z/gamma1_pst)
		WeightAz(azSubfr, Gamma2Pst, ap3[:])
		WeightAz(azSubfr, Gamma1Pst, ap4[:])

		// 3. Inverse filter syn[] through A(z/gamma2_pst) to get current res2.
		// memRes carries the last M samples of syn across subframe/frame boundaries,
		// matching the C reference which accesses syn[-M..-1] via pointer arithmetic.
		res2 := state.res2Buf[params.PIT_MAX : params.PIT_MAX+params.L_SUBFR]
		dsp.Residue(res2, syn[iSubfr:iSubfr+params.L_SUBFR], ap3[:], state.memRes[:], true)

		// 4. Pitch postfiltering on res2
		if vad == 1 {
			pitPstFilt(&state.res2Buf, t0Min, t0Max, res2Pst[:])
		} else {
			copy(res2Pst[:], res2[:])
		}

		// 5. Tilt compensation: impulse response h of A(z/gamma2_pst) / A(z/gamma1_pst)
		copy(h[:params.M+1], ap3[:params.M+1])
		for i := params.M + 1; i < LH; i++ {
			h[i] = 0
		}
		for i := range zeroMem {
			zeroMem[i] = 0
		}
		dsp.SynthesisFilter(h[:], h[:], ap4[:], zeroMem[:], false)

		var temp1, temp2 float32
		for i := 0; i < LH; i++ {
			temp1 += h[i] * h[i]
		}
		for i := 0; i < LH-1; i++ {
			temp2 += h[i] * h[i+1]
		}
		var mu float32
		if temp2 > 0 && temp1 > 0 {
			mu = temp2 * MuTilt / temp1
		}

		preemphasis(res2Pst[:], mu, &state.memPre)

		// 6. Short-term synthesis filtering through 1/A(z/gamma1_pst)
		dsp.SynthesisFilter(synPst[iSubfr:iSubfr+params.L_SUBFR], res2Pst[:], ap4[:], state.memSynPst[:], true)

		// 7. Adaptive gain control (AGC)
		agc(syn[iSubfr:iSubfr+params.L_SUBFR], synPst[iSubfr:iSubfr+params.L_SUBFR], &state.pastGain)

		// 8. Update res2 history: shift left by L_SUBFR
		copy(state.res2Buf[0:params.PIT_MAX], state.res2Buf[params.L_SUBFR:params.L_SUBFR+params.PIT_MAX])
	}

	// Overwrite input syn with postfiltered speech
	copy(syn, synPst[:])
}

// pitPstFilt finds the pitch delay around the transmitted pitch and performs harmonic postfiltering:
//
//	H_p(z) = (1 + g * z^-T) / (1 + g), where g = min(pit_gain * gammap, 1.0)
func pitPstFilt(res2Buf *[params.PIT_MAX + params.L_SUBFR]float32, t0Min, t0Max int, signalPst []float32) {
	signal := res2Buf[params.PIT_MAX : params.PIT_MAX+params.L_SUBFR]

	// Find delay in [t0Min, t0Max] that maximizes cross-correlation
	corMax := float32(-1e30)
	bestT0 := t0Min

	for i := t0Min; i <= t0Max; i++ {
		var corr float32
		delayed := res2Buf[params.PIT_MAX-i : params.PIT_MAX-i+params.L_SUBFR]
		for j := 0; j < params.L_SUBFR; j++ {
			corr += signal[j] * delayed[j]
		}
		if corr > corMax {
			corMax = corr
			bestT0 = i
		}
	}

	// Compute energy of delayed signal
	var ener float32 = 0.5
	delayed := res2Buf[params.PIT_MAX-bestT0 : params.PIT_MAX-bestT0+params.L_SUBFR]
	for i := 0; i < params.L_SUBFR; i++ {
		ener += delayed[i] * delayed[i]
	}

	// Compute energy of current subframe signal
	var ener0 float32 = 0.5
	for i := 0; i < params.L_SUBFR; i++ {
		ener0 += signal[i] * signal[i]
	}

	if corMax < 0 {
		corMax = 0
	}

	// Check if prediction gain >= 3 dB:
	// gain_pred (dB) = -10 * log10(1 - cor_max^2 / (ener * ener0)) >= 3 dB <=> cor_max^2 >= 0.5 * ener * ener0
	if corMax*corMax < ener*ener0*0.5 {
		// Switch off pitch postfilter
		copy(signalPst, signal)
		return
	}

	var g0, gain float32
	if corMax > ener {
		g0 = InvGammap
		gain = Gammap2
	} else {
		corScaled := corMax * Gammap
		temp := 1.0 / (corScaled + ener)
		gain = temp * corScaled
		g0 = 1.0 - gain
	}

	for i := 0; i < params.L_SUBFR; i++ {
		signalPst[i] = g0*signal[i] + gain*delayed[i]
	}
}

// preemphasis performs first-order spectral tilt filtering in-place:
//
//	s(n) = s(n) - g * s(n-1)
func preemphasis(signal []float32, g float32, memPre *float32) {
	l := len(signal)
	temp := signal[l-1]

	for i := l - 1; i > 0; i-- {
		signal[i] -= g * signal[i-1]
	}
	signal[0] -= g * (*memPre)

	*memPre = temp
}

// agc scales the postfiltered output on a subframe basis to match input speech energy:
//
//	gain(n) = AGC_FAC * gain(n-1) + (1 - AGC_FAC) * sqrt(E_in / E_out)
func agc(sigIn, sigOut []float32, pastGain *float32) {
	var gainOut float32
	for i := 0; i < len(sigOut); i++ {
		gainOut += sigOut[i] * sigOut[i]
	}
	if gainOut == 0.0 {
		*pastGain = 0.0
		return
	}

	var gainIn float32
	for i := 0; i < len(sigIn); i++ {
		gainIn += sigIn[i] * sigIn[i]
	}

	var g0 float32
	if gainIn != 0.0 {
		g0 = float32(math.Sqrt(float64(gainIn/gainOut))) * AgcFac1
	}

	gain := *pastGain
	for i := 0; i < len(sigOut); i++ {
		gain = AgcFac*gain + g0
		sigOut[i] *= gain
	}
	*pastGain = gain
}
