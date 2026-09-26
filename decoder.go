package g729

import (
	"fmt"
	"math"

	"github.com/selawe/go-g729/internal/bits"
	"github.com/selawe/go-g729/internal/codebook"
	"github.com/selawe/go-g729/internal/dsp"
	"github.com/selawe/go-g729/internal/filter"
	"github.com/selawe/go-g729/internal/lsp"
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/pitch"
	"github.com/selawe/go-g729/internal/tables"
	"github.com/selawe/go-g729/internal/vad"
)

// decoder implements the Decoder interface for ITU-T G.729 / G.729A speech synthesis.
type decoder struct {
	// Excitation history buffer (154 past samples + 80 current frame samples = 234 floats).
	oldExc [params.EXC_BUF_LEN]float32

	// Line spectral pairs (cosine domain).
	lspOld [params.M]float32

	// Line spectral frequencies (radians domain) and MA predictor mode for erasure concealment.
	prevLSF [params.M]float32
	prevMA  int

	// 4-frame LSP MA predictor history.
	freqPrev [params.MA_NP][params.M]float32

	// Synthesis filter memory (10 samples).
	memSyn [params.M]float32

	// Pitch sharpening of previous subframe.
	sharp float32

	// Decoded integer pitch lag of previous frame/subframe.
	oldT0 int

	// Decoded codebook and pitch gains.
	gainPit  float32
	gainCode float32

	// History of past 4 quantized energies (in dB) for codebook gain prediction.
	pastQuaEn [params.MA_NP]float32

	// Adaptive postfilter state.
	pstState filter.PostFilterState

	// Output 100 Hz high-pass filter state.
	hpfState dsp.HPFState

	// Annex B Comfort Noise Generator state.
	cngState vad.CNGDecoderState

	// Frame erasure seed for random pulse generation.
	seedFER int16

	// Previous frame type indicator (1: active speech, 0: SID or untransmitted).
	pastFTyp int

	// Excitation energy saved from active speech for SID recovery.
	sidSav float32

	// Consecutive lost frames counter for progressive muting.
	badFrames int
}

// NewDecoder creates and initializes a new G.729 / G.729A speech decoder.
func NewDecoder() Decoder {
	d := &decoder{}
	d.Reset()
	return d
}

// Reset clears all internal state, delay lines, and history buffers to their initial reset state.
func (d *decoder) Reset() {
	for i := range d.oldExc {
		d.oldExc[i] = 0
	}
	copy(d.lspOld[:], tables.LSPOldReset[:])
	copy(d.prevLSF[:], tables.FreqPrevReset[:])
	d.prevMA = 0

	for k := 0; k < params.MA_NP; k++ {
		copy(d.freqPrev[k][:], tables.FreqPrevReset[:])
	}
	for i := range d.memSyn {
		d.memSyn[i] = 0
	}

	d.sharp = params.SHARPMIN
	d.oldT0 = 60
	d.gainPit = 0.0
	d.gainCode = 0.0

	for i := 0; i < params.MA_NP; i++ {
		d.pastQuaEn[i] = -14.0
	}

	d.pstState.Reset()
	d.hpfState.Reset()
	d.cngState.Reset()

	d.seedFER = 21845
	d.pastFTyp = 1
	d.sidSav = 0.0
	d.badFrames = 0
}

// Decode decompresses a G.729 bitstream frame in src into 80 16-bit linear PCM samples in dst.
//
// src length determines the frame processing mode:
//   - 10 bytes: Normal active speech frame (80 bits)
//   - 2 bytes:  Annex B SID frame (16 bits)
//   - 0 bytes (or nil): Frame erasure / packet loss concealment (or untransmitted frame if in DTX)
func (d *decoder) Decode(dst []int16, src []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("g729: decode panic: %v", r)
		}
	}()

	if len(dst) < params.L_FRAME {
		return ErrInvalidOutputLen
	}

	excOffset := params.L_PAST_EXC
	var synth [params.L_FRAME]float32
	var pitchLags [2]int

	switch len(src) {
	case 10:
		// --------------------------------------------------------------------
		// 1. Normal Active Speech Frame (10 bytes = 80 bits)
		// --------------------------------------------------------------------
		d.badFrames = 0
		var paramSet bits.ParamSet
		bits.Unpack(&paramSet, src[:10])

		// Parity check on pitch delay P1 (odd parity across 6 MSBs of P1)
		parityErr := 0
		if pitch.ParityBit(paramSet.P1) != paramSet.P0 {
			parityErr = 1
		}

		// Decode LSPs
		lspNew := lsp.DequantizeLSPExt(
			int(paramSet.L0),
			int(paramSet.L1),
			int(paramSet.L2),
			int(paramSet.L3),
			false,
			&d.prevLSF,
			&d.prevMA,
			&d.freqPrev,
		)

		// Interpolate LPC for both subframes
		a1, a2 := lsp.InterpolateLSP(d.lspOld, lspNew)
		d.lspOld = lspNew

		var t0Min, t0Max int

		for subfrIdx := 0; subfrIdx < params.N_SUBFR; subfrIdx++ {
			iSubfr := subfrIdx * params.L_SUBFR
			subfrExcOffset := excOffset + iSubfr

			var aSubfr [params.MP1]float32
			if subfrIdx == 0 {
				copy(aSubfr[:], a1[:])
			} else {
				copy(aSubfr[:], a2[:])
			}

			// Decode pitch delay
			var t0, t0Frac int
			if subfrIdx == 0 {
				if parityErr != 0 {
					t0 = d.oldT0
					t0Frac = 0
					d.oldT0++
					if d.oldT0 > params.PIT_MAX {
						d.oldT0 = params.PIT_MAX
					}
				} else {
					t0, t0Frac = pitch.DecodePitch(int(paramSet.P1), 0, &t0Min, &t0Max)
					d.oldT0 = t0
				}

				// Always calculate search range [t0Min, t0Max] for subframe 2 from subframe 1 lag
				t0Min = t0 - 5
				if t0Min < params.PIT_MIN {
					t0Min = params.PIT_MIN
				}
				t0Max = t0Min + 9
				if t0Max > params.PIT_MAX {
					t0Max = params.PIT_MAX
					t0Min = t0Max - 9
				}
			} else {
				t0, t0Frac = pitch.DecodePitch(int(paramSet.P2), 1, &t0Min, &t0Max)
				d.oldT0 = t0
			}

			if t0 < params.PIT_MIN {
				t0 = params.PIT_MIN
			}
			if t0 > params.PIT_MAX {
				t0 = params.PIT_MAX
			}
			pitchLags[subfrIdx] = t0

			// Adaptive codebook excitation
			pitch.InterpExcitation(d.oldExc[:], subfrExcOffset, t0, t0Frac, params.L_SUBFR)

			// Innovative codebook
			var cPos, cSign int
			if subfrIdx == 0 {
				cPos = int(paramSet.C1)
				cSign = int(paramSet.S1)
			} else {
				cPos = int(paramSet.C2)
				cSign = int(paramSet.S2)
			}
			codeVec := codebook.BuildCodeVector(cPos, cSign)

			// Pitch sharpening
			for i := t0; i < params.L_SUBFR; i++ {
				codeVec[i] += d.sharp * codeVec[i-t0]
			}

			// Gain decoding
			var ga, gb int
			if subfrIdx == 0 {
				ga = int(paramSet.GA1)
				gb = int(paramSet.GB1)
			} else {
				ga = int(paramSet.GA2)
				gb = int(paramSet.GB2)
			}
			gainPit, gainCode := codebook.DequantizeGain(ga, gb, codeVec[:], &d.pastQuaEn, 0, &d.gainPit, &d.gainCode)

			// Update pitch sharpening
			d.sharp = gainPit
			if d.sharp > params.SHARPMAX {
				d.sharp = params.SHARPMAX
			}
			if d.sharp < params.SHARPMIN {
				d.sharp = params.SHARPMIN
			}

			// Total excitation: exc = g_p * v + g_c * c
			for i := 0; i < params.L_SUBFR; i++ {
				d.oldExc[subfrExcOffset+i] = gainPit*d.oldExc[subfrExcOffset+i] + gainCode*codeVec[i]
			}

			// Synthesis filter 1/A(z)
			dsp.SynthesisFilter(synth[iSubfr:iSubfr+params.L_SUBFR], d.oldExc[subfrExcOffset:subfrExcOffset+params.L_SUBFR], aSubfr[:], d.memSyn[:], true)
		}

		// Save excitation energy for SID recovery in DTX transition
		var ener float32
		for i := 0; i < params.L_FRAME; i++ {
			ener += d.oldExc[excOffset+i] * d.oldExc[excOffset+i]
		}
		d.sidSav = ener

		// Update excitation history buffer (shift left by 80 samples)
		copy(d.oldExc[0:params.L_PAST_EXC], d.oldExc[params.L_FRAME:params.EXC_BUF_LEN])

		// Adaptive postfiltering
		var aT [2 * params.MP1]float32
		copy(aT[:params.MP1], a1[:])
		copy(aT[params.MP1:], a2[:])
		filter.PostFilterB(synth[:], aT[:], pitchLags, 1, &d.pstState)

		// 100 Hz output high-pass filter
		dsp.PostProcessFilter(synth[:], &d.hpfState)

		d.pastFTyp = 1

	case 2:
		// --------------------------------------------------------------------
		// 2. Annex B SID Frame (2 bytes = 16 bits)
		// --------------------------------------------------------------------
		d.badFrames = 0
		var sidBytes [2]byte
		copy(sidBytes[:], src[:2])
		sidParams := vad.UnpackSID(sidBytes)
		sidParams.Transmitted = true

		pastIsSpeech := (d.pastFTyp == 1)
		var aT [2 * params.MP1]float32
		d.cngState.DecCNG(pastIsSpeech, d.sidSav, sidParams, d.oldExc[:], excOffset, &d.lspOld, &aT, &d.freqPrev)

		// Synthesis filter for both subframes
		for subfrIdx := 0; subfrIdx < params.N_SUBFR; subfrIdx++ {
			iSubfr := subfrIdx * params.L_SUBFR
			aSubfr := aT[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]
			dsp.SynthesisFilter(synth[iSubfr:iSubfr+params.L_SUBFR], d.oldExc[excOffset+iSubfr:excOffset+iSubfr+params.L_SUBFR], aSubfr, d.memSyn[:], true)
		}

		d.sharp = params.SHARPMIN
		pitchLags = [2]int{d.oldT0, d.oldT0}

		// Update excitation history buffer
		copy(d.oldExc[0:params.L_PAST_EXC], d.oldExc[params.L_FRAME:params.EXC_BUF_LEN])

		// Postfiltering (bypass pitch postfilter for comfort noise)
		filter.PostFilterB(synth[:], aT[:], pitchLags, 0, &d.pstState)

		// 100 Hz output high-pass filter
		dsp.PostProcessFilter(synth[:], &d.hpfState)

		d.pastFTyp = 0

	case 0:
		// --------------------------------------------------------------------
		// 3. Untransmitted DTX Frame or Packet Loss Concealment (PLC)
		// --------------------------------------------------------------------
		if d.pastFTyp == 0 {
			// Untransmitted comfort noise frame in DTX
			sidParams := vad.SIDParams{Transmitted: false}
			var aT [2 * params.MP1]float32
			d.cngState.DecCNG(false, d.sidSav, sidParams, d.oldExc[:], excOffset, &d.lspOld, &aT, &d.freqPrev)

			for subfrIdx := 0; subfrIdx < params.N_SUBFR; subfrIdx++ {
				iSubfr := subfrIdx * params.L_SUBFR
				aSubfr := aT[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]
				dsp.SynthesisFilter(synth[iSubfr:iSubfr+params.L_SUBFR], d.oldExc[excOffset+iSubfr:excOffset+iSubfr+params.L_SUBFR], aSubfr, d.memSyn[:], true)
			}

			d.sharp = params.SHARPMIN
			pitchLags = [2]int{d.oldT0, d.oldT0}

			copy(d.oldExc[0:params.L_PAST_EXC], d.oldExc[params.L_FRAME:params.EXC_BUF_LEN])
			filter.PostFilterB(synth[:], aT[:], pitchLags, 0, &d.pstState)
			dsp.PostProcessFilter(synth[:], &d.hpfState)

			d.pastFTyp = 0
		} else {
			// Packet Loss Concealment (PLC) for active speech
			d.badFrames++

			// Extrapolate LSPs from previous good frame
			lspNew := lsp.DequantizeLSPExt(0, 0, 0, 0, true, &d.prevLSF, &d.prevMA, &d.freqPrev)
			a1, a2 := lsp.InterpolateLSP(d.lspOld, lspNew)
			d.lspOld = lspNew

			for subfrIdx := 0; subfrIdx < params.N_SUBFR; subfrIdx++ {
				iSubfr := subfrIdx * params.L_SUBFR
				subfrExcOffset := excOffset + iSubfr

				var aSubfr [params.MP1]float32
				if subfrIdx == 0 {
					copy(aSubfr[:], a1[:])
				} else {
					copy(aSubfr[:], a2[:])
				}

				// Extrapolate pitch delay with gradual drift
				t0 := d.oldT0
				t0Frac := 0
				d.oldT0++
				if d.oldT0 > params.PIT_MAX {
					d.oldT0 = params.PIT_MAX
				}
				pitchLags[subfrIdx] = t0

				// Adaptive excitation
				pitch.InterpExcitation(d.oldExc[:], subfrExcOffset, t0, t0Frac, params.L_SUBFR)

				// Random pulse generation for innovative codebook
				rndPos := int(vad.RandomG729C(&d.seedFER) & 0x1FFF)
				rndSign := int(vad.RandomG729C(&d.seedFER) & 0x000F)
				codeVec := codebook.BuildCodeVector(rndPos, rndSign)

				// Pitch sharpening
				for i := t0; i < params.L_SUBFR; i++ {
					codeVec[i] += d.sharp * codeVec[i-t0]
				}

				// Gain attenuation (0.9 for pitch gain, 0.98 for codebook gain)
				gainPit, gainCode := codebook.DequantizeGain(0, 0, codeVec[:], &d.pastQuaEn, 1, &d.gainPit, &d.gainCode)

				// Progressive muting if consecutive frame loss exceeds 6 frames (60 ms)
				if d.badFrames > 6 {
					muteFac := float32(math.Pow(0.9, float64(d.badFrames-6)))
					gainPit *= muteFac
					gainCode *= muteFac
				}

				// Update pitch sharpening
				d.sharp = gainPit
				if d.sharp > params.SHARPMAX {
					d.sharp = params.SHARPMAX
				}
				if d.sharp < params.SHARPMIN {
					d.sharp = params.SHARPMIN
				}

				// Total excitation
				for i := 0; i < params.L_SUBFR; i++ {
					d.oldExc[subfrExcOffset+i] = gainPit*d.oldExc[subfrExcOffset+i] + gainCode*codeVec[i]
				}

				// Synthesis filter 1/A(z)
				dsp.SynthesisFilter(synth[iSubfr:iSubfr+params.L_SUBFR], d.oldExc[subfrExcOffset:subfrExcOffset+params.L_SUBFR], aSubfr[:], d.memSyn[:], true)
			}

			copy(d.oldExc[0:params.L_PAST_EXC], d.oldExc[params.L_FRAME:params.EXC_BUF_LEN])

			var aT [2 * params.MP1]float32
			copy(aT[:params.MP1], a1[:])
			copy(aT[params.MP1:], a2[:])
			filter.PostFilterB(synth[:], aT[:], pitchLags, 1, &d.pstState)

			dsp.PostProcessFilter(synth[:], &d.hpfState)

			d.pastFTyp = 1
		}

	default:
		return ErrInvalidFrameLen
	}

	// --------------------------------------------------------------------
	// 4. Output Saturation & Conversion to 16-bit Linear PCM
	// --------------------------------------------------------------------
	for i := 0; i < params.L_FRAME; i++ {
		val := synth[i]
		if val > 32767.0 {
			dst[i] = 32767
		} else if val < -32768.0 {
			dst[i] = -32768
		} else {
			if val >= 0 {
				dst[i] = int16(val + 0.5)
			} else {
				dst[i] = int16(val - 0.5)
			}
		}
	}

	return nil
}
