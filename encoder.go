package g729

import (
	"errors"
	"math"
	"runtime/debug"

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

type Encoder struct {
	cfg Config

	frameCount int

	// Input High-Pass Filter delay line (2nd-order IIR)
	hpfState dsp.HPFState

	// Speech buffer: 240 samples = 120 past + 80 frame + 40 lookahead
	oldSpeech [params.L_TOTAL]float32

	// Weighted speech buffer: 143 past + 80 frame
	oldWsp [params.PIT_MAX + params.L_FRAME]float32

	// Excitation buffer: 154 past + 80 frame
	oldExc [params.EXC_BUF_LEN]float32

	// LSP memories
	oldA    [params.MP1]float32
	lspOld  [params.M]float32
	lspOldQ [params.M]float32
	lspMA   [params.MA_NP][params.M]float32

	// Interpolated LP coefficients for current frame (both subframes)
	aqT [2 * params.MP1]float32
	apT [2 * params.MP1]float32

	// Weighting filter memories
	memW    [params.M]float32
	memW0   [params.M]float32
	memZero [params.M]float32
	memErr  [params.M]float32
	memSyn  [params.M]float32

	// Pitch sharpening factor
	sharp float32

	// Pitch taming state
	excErr [4]float32

	// Gain prediction memory (past 4 quantized energies in dB)
	gainPred [params.MA_NP]float32

	// Full G.729 harmonic weighting & time budget state
	fullSearchExtra int
	larOld          [2]float32
	smooth          int

	// Annex B VAD / DTX state
	vadState vad.VADState
	dtxState vad.DTXEncoderState
	pastVad  int
	ppastVad int
	seed     int16

	// Encoded parameter set for current frame
	paramSet bits.ParamSet

	// Cumulative runtime telemetry stats
	stats EncoderStats
}

// NewEncoder creates and initializes a G.729 speech encoder according to the given Config.
func NewEncoder(cfg Config) *Encoder {
	e := &Encoder{
		cfg: cfg,
	}
	e.Reset()
	return e
}

// Reset clears all encoder state, filter delay lines, and history buffers to their initial state.
func (e *Encoder) Reset() {
	if e == nil {
		return
	}
	e.frameCount = 0
	e.stats = EncoderStats{}
	e.hpfState.Reset()

	for i := range e.oldSpeech {
		e.oldSpeech[i] = 0.0
	}
	for i := range e.oldWsp {
		e.oldWsp[i] = 0.0
	}
	for i := range e.oldExc {
		e.oldExc[i] = 0.0
	}

	copy(e.lspOld[:], tables.LSPOldReset[:])
	copy(e.lspOldQ[:], tables.LSPOldReset[:])
	lsp.InitLSPMA(&e.lspMA)

	e.oldA[0] = 1.0
	for i := 1; i < len(e.oldA); i++ {
		e.oldA[i] = 0.0
	}

	for i := range e.aqT {
		e.aqT[i] = 0.0
		e.apT[i] = 0.0
	}

	for i := range e.memW {
		e.memW[i] = 0.0
		e.memW0[i] = 0.0
		e.memZero[i] = 0.0
		e.memErr[i] = 0.0
		e.memSyn[i] = 0.0
	}

	e.sharp = params.SHARPMIN
	codebook.InitExcErr(&e.excErr)

	for i := range e.gainPred {
		e.gainPred[i] = -14.0
	}

	e.fullSearchExtra = 0
	e.larOld[0] = 0.0
	e.larOld[1] = 0.0
	e.smooth = 1

	e.vadState.Reset()
	e.dtxState.Reset()
	e.pastVad = 1
	e.ppastVad = 1
	e.seed = params.INIT_SEED
}

// Encode processes 80 samples (10 ms) of 16-bit linear PCM audio in src, and writes the encoded
// bitstream into dst.
func (e *Encoder) Encode(dst []byte, src []int16) (n int, frameType FrameType, err error) {
	if e == nil {
		return 0, FrameUntransmitted, ErrNilEncoder
	}
	if !e.cfg.DisablePanicRecovery {
		defer func() {
			if r := recover(); r != nil {
				err = buildPanicError(r, debug.Stack(), e.cfg.IncludePanicStack)
				n = 0
				frameType = FrameUntransmitted
			}
		}()
	}

	// 1. Validate buffers
	if len(src) != params.L_FRAME {
		return 0, FrameUntransmitted, ErrInvalidInputLen
	}
	if len(dst) < params.BYTES_PER_FRAME {
		return 0, FrameUntransmitted, ErrInvalidOutputLen
	}

	// 2. High-pass filter new speech samples into speech buffer
	newSpeech := e.oldSpeech[params.L_TOTAL-params.L_FRAME : params.L_TOTAL]
	for i := 0; i < params.L_FRAME; i++ {
		newSpeech[i] = float32(src[i])
	}

	// clippedCount counts samples that entered the SoftClip compression zone (|x| >= 28000).
	// This threshold matches the SoftClip knee so the metric reflects what was actually
	// processed, regardless of whether EnableClipRepair is on or off.
	clippedCount := 0
	if e.cfg.EnableClipRepair {
		clippedCount = dsp.SoftClip(newSpeech, 28000.0, 32760.0)
	} else {
		const softClipThreshold = float32(28000)
		for _, s := range newSpeech {
			if s >= softClipThreshold || s <= -softClipThreshold {
				clippedCount++
			}
		}
	}

	dsp.HighPassFilter(newSpeech, &e.hpfState)

	// Pointers within oldSpeech:
	pWindow := e.oldSpeech[:]                                                                                    // [0...240]
	speech := e.oldSpeech[params.L_TOTAL-params.L_FRAME-params.L_LOOKAHEAD : params.L_TOTAL-params.L_LOOKAHEAD] // [120...200]

	// 3. Autocorrelation analysis
	var r [params.NP + 1]float32
	var rUnwindowed [params.MP1]float32

	if e.cfg.EnableVAD {
		dsp.AutocorrRaw(r[:params.NP+1], pWindow, nil, params.NP)
		copy(rUnwindowed[:], r[:params.MP1])
		dsp.LagWindow(r[:params.NP+1], params.NP)
	} else {
		dsp.Autocorr(r[:params.MP1], pWindow, nil, params.M)
	}

	// 4. Levinson-Durbin
	a, rc, levFallback, levErr := dsp.Levinson(r[:params.MP1], params.M)
	if levErr != nil {
		a = e.oldA
	} else {
		e.oldA = a
	}

	// 5. Convert A(z) to LSP
	lspNew, ok := lsp.LPC2LSP(&a, &e.lspOld)
	if !ok {
		lspNew = e.lspOld
	}

	// 6. Annex B VAD / DTX
	e.frameCount++
	var energyDB float32
	var marker int = vad.Voice

	if e.cfg.EnableVAD {
		lsfNew := lsp.LSP2LSF(lspNew)
		var vadEnergy float32
		marker, vadEnergy = e.vadState.Process(rc[1], lsfNew[:], r[:params.NP+1], pWindow, e.frameCount, e.pastVad, e.ppastVad)
		energyDB = vadEnergy
		e.dtxState.UpdateCNG(rUnwindowed[:params.MP1], marker)

		if marker == vad.Noise {
			sid := e.dtxState.CodCNG(
				e.oldExc[:],
				params.L_PAST_EXC,
				e.pastVad,
				&e.lspOldQ,
				&e.aqT,
				&e.lspMA,
				&e.seed,
				&e.excErr,
			)

			e.ppastVad = e.pastVad
			e.pastVad = 0

			// Shift speech, wsp, exc buffers for next frame
			copy(e.oldSpeech[:params.L_TOTAL-params.L_FRAME], e.oldSpeech[params.L_FRAME:])
			copy(e.oldWsp[:params.PIT_MAX], e.oldWsp[params.L_FRAME:params.L_FRAME+params.PIT_MAX])
			copy(e.oldExc[:params.L_PAST_EXC], e.oldExc[params.L_FRAME:params.L_FRAME+params.L_PAST_EXC])

			ft := FrameUntransmitted
			if sid.Transmitted {
				ft = FrameSID
			}
			if e.cfg.OnDiagnostic != nil {
				e.invokeDiagnostic(DiagnosticStats{
					FrameIndex:   e.frameCount,
					FrameType:    ft,
					EnergyDB:     energyDB,
					ZeroCrossing: zeroCrossingRate(speech),
					VADMarker:    marker,
					ClippedCount: clippedCount,
					LPCFallback:  levFallback,
				})
			}

			e.stats.TotalFrames++
			e.stats.ClippedSamples += int64(clippedCount)
			if sid.Transmitted {
				e.stats.SIDFrames++
				e.stats.BytesEmitted += params.BYTES_PER_SID
				copy(dst[:params.BYTES_PER_SID], sid.Packed[:])
				return params.BYTES_PER_SID, FrameSID, nil
			}
			e.stats.Untransmitted++
			return 0, FrameUntransmitted, nil
		}

		e.ppastVad = e.pastVad
		e.pastVad = 1
	} else if e.cfg.OnDiagnostic != nil {
		energyDB = float32(10.0 * math.Log10(float64(r[0]/240.0 + 1e-38)))
	}

	// 7. Active Speech Frame: Quantize LSP
	l0, l1, l2, l3, lspNewQ := lsp.QuantizeLSP(lspNew, &e.lspMA)
	e.paramSet.L0 = uint8(l0)
	e.paramSet.L1 = uint8(l1)
	e.paramSet.L2 = uint8(l2)
	e.paramSet.L3 = uint8(l3)

	// 8. Interpolate quantized LSPs for subframe 1 and 2
	aSubfr1, aSubfr2 := lsp.InterpolateLSP(e.lspOldQ, lspNewQ)
	copy(e.aqT[:params.MP1], aSubfr1[:])
	copy(e.aqT[params.MP1:], aSubfr2[:])
	e.lspOld = lspNew
	e.lspOldQ = lspNewQ

	// 9. LP residual & Weighted speech computation
	speechOffset := params.L_TOTAL - params.L_FRAME - params.L_LOOKAHEAD // 120

	// Pre-compute LP residual exc for both subframes into e.oldExc with exact past speech history
	dsp.Residue(
		e.oldExc[params.L_PAST_EXC:params.L_PAST_EXC+params.L_SUBFR],
		speech[:params.L_SUBFR],
		e.aqT[:params.MP1],
		e.oldSpeech[speechOffset-params.M:speechOffset],
		false,
	)
	dsp.Residue(
		e.oldExc[params.L_PAST_EXC+params.L_SUBFR:params.L_PAST_EXC+params.L_FRAME],
		speech[params.L_SUBFR:params.L_FRAME],
		e.aqT[params.MP1:],
		speech[params.L_SUBFR-params.M:params.L_SUBFR],
		false,
	)

	var ap1Full, ap2Full [2 * params.MP1]float32

	if e.cfg.Variant == VariantG729A {
		filter.WeightAz(e.aqT[:params.MP1], filter.Gamma1A, e.apT[:params.MP1])
		filter.WeightAz(e.aqT[params.MP1:], filter.Gamma1A, e.apT[params.MP1:])

		// Filter wsp directly from exc using Ap1(z) = Ap(z) * (1 - 0.7*z^-1)
		var ap1 [params.MP1]float32

		// Subframe 0 wsp
		ap1[0] = 1.0
		for i := 1; i <= params.M; i++ {
			ap1[i] = e.apT[i] - 0.7*e.apT[i-1]
		}
		dsp.SynthesisFilter(
			e.oldWsp[params.PIT_MAX:params.PIT_MAX+params.L_SUBFR],
			e.oldExc[params.L_PAST_EXC:params.L_PAST_EXC+params.L_SUBFR],
			ap1[:],
			e.memW[:],
			true,
		)

		// Subframe 1 wsp
		ap1[0] = 1.0
		for i := 1; i <= params.M; i++ {
			ap1[i] = e.apT[params.MP1+i] - 0.7*e.apT[params.MP1+i-1]
		}
		dsp.SynthesisFilter(
			e.oldWsp[params.PIT_MAX+params.L_SUBFR:params.PIT_MAX+params.L_FRAME],
			e.oldExc[params.L_PAST_EXC+params.L_SUBFR:params.L_PAST_EXC+params.L_FRAME],
			ap1[:],
			e.memW[:],
			true,
		)
	} else {
		// Full G.729: compute gamma1, gamma2 dynamically using PercVar
		var lspSubfr1 [params.M]float32
		for i := 0; i < params.M; i++ {
			lspSubfr1[i] = 0.5*e.lspOld[i] + 0.5*lspNew[i]
		}
		lsfInt := lsp.LSP2LSF(lspSubfr1)
		lsfNewCurr := lsp.LSP2LSF(lspNew)
		gamma1, gamma2 := filter.PercVar(lsfInt[:], lsfNewCurr[:], rc[:], &e.larOld, &e.smooth)

		filter.WeightAz(e.aqT[:params.MP1], gamma1[0], e.apT[:params.MP1])
		filter.WeightAz(e.aqT[params.MP1:], gamma1[1], e.apT[params.MP1:])
		filter.WeightAz(e.aqT[:params.MP1], gamma2[0], ap2Full[:params.MP1])
		filter.WeightAz(e.aqT[params.MP1:], gamma2[1], ap2Full[params.MP1:])

		filter.WeightAz(aSubfr1[:], gamma1[0], ap1Full[:params.MP1])
		filter.WeightAz(aSubfr2[:], gamma1[1], ap1Full[params.MP1:])

		// Subframe 0 wsp
		dsp.Residue(
			e.oldWsp[params.PIT_MAX:params.PIT_MAX+params.L_SUBFR],
			speech[:params.L_SUBFR],
			ap1Full[:params.MP1],
			e.oldSpeech[speechOffset-params.M:speechOffset],
			false,
		)
		dsp.SynthesisFilter(
			e.oldWsp[params.PIT_MAX:params.PIT_MAX+params.L_SUBFR],
			e.oldWsp[params.PIT_MAX:params.PIT_MAX+params.L_SUBFR],
			ap2Full[:params.MP1],
			e.memW[:],
			true,
		)

		// Subframe 1 wsp
		dsp.Residue(
			e.oldWsp[params.PIT_MAX+params.L_SUBFR:params.PIT_MAX+params.L_FRAME],
			speech[params.L_SUBFR:params.L_FRAME],
			ap1Full[params.MP1:],
			speech[params.L_SUBFR-params.M:params.L_SUBFR],
			false,
		)
		dsp.SynthesisFilter(
			e.oldWsp[params.PIT_MAX+params.L_SUBFR:params.PIT_MAX+params.L_FRAME],
			e.oldWsp[params.PIT_MAX+params.L_SUBFR:params.PIT_MAX+params.L_FRAME],
			ap2Full[params.MP1:],
			e.memW[:],
			true,
		)
	}

	// Open-loop pitch delay
	tOp := pitch.OpenLoopPitch(e.oldWsp[:])

	// Subframe loop bounds for closed-loop pitch search
	t0Min := tOp - 3
	if t0Min < params.PIT_MIN {
		t0Min = params.PIT_MIN
	}
	t0Max := t0Min + 6
	if t0Max > params.PIT_MAX {
		t0Max = params.PIT_MAX
		t0Min = t0Max - 6
	}

	var h1 [params.L_SUBFR]float32
	var xn [params.L_SUBFR]float32
	var xn2 [params.L_SUBFR]float32
	var y1 [params.L_SUBFR]float32
	var errorBuf [params.L_SUBFR]float32
	var subfrGainPit [2]float32
	var subfrGainCode [2]float32

	// 10. Subframe processing loop
	for iSubfr := 0; iSubfr < params.L_FRAME; iSubfr += params.L_SUBFR {
		subfrIdx := iSubfr / params.L_SUBFR
		excOffset := params.L_PAST_EXC + iSubfr
		curAq := e.aqT[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]
		curAp := e.apT[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]

		// 10a. Impulse response h1 of weighted synthesis filter
		if e.cfg.Variant == VariantG729A {
			h1[0] = 1.0
			for i := 1; i < params.L_SUBFR; i++ {
				h1[i] = 0.0
			}
			var zeroMem [params.M]float32
			dsp.SynthesisFilter(h1[:], h1[:], curAp, zeroMem[:], false)
		} else {
			curAp2 := ap2Full[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]
			var aiZero [params.MP1]float32
			copy(aiZero[:], curAp[:params.MP1])
			var zeroMem [params.M]float32
			dsp.SynthesisFilter(h1[:], aiZero[:], curAq, zeroMem[:], false)
			dsp.SynthesisFilter(h1[:], h1[:], curAp2, zeroMem[:], false)
		}

		// 10b. Target vector xn for pitch search
		if e.cfg.Variant == VariantG729A {
			dsp.SynthesisFilter(xn[:], e.oldExc[excOffset:excOffset+params.L_SUBFR], curAp, e.memW0[:], false)
		} else {
			curAp1 := ap1Full[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]
			curAp2 := ap2Full[subfrIdx*params.MP1 : (subfrIdx+1)*params.MP1]
			dsp.SynthesisFilter(errorBuf[:], e.oldExc[excOffset:excOffset+params.L_SUBFR], curAq, e.memErr[:], false)
			dsp.Residue(xn[:], errorBuf[:], curAp1, e.memErr[:], false)
			dsp.SynthesisFilter(xn[:], xn[:], curAp2, e.memW0[:], false)
		}

		// 10c. Closed-loop fractional pitch search
		t0, t0Frac := pitch.ClosedLoopPitch(
			e.oldExc[:],
			excOffset,
			xn[:],
			h1[:],
			t0Min,
			t0Max,
			subfrIdx,
		)

		if subfrIdx == 0 {
			p1 := pitch.EncodePitch(t0, t0Frac, 0, &t0Min, &t0Max)
			p0 := pitch.ParityBit(uint8(p1))
			e.paramSet.P1 = uint8(p1)
			e.paramSet.P0 = uint8(p0)
		} else {
			p2 := pitch.EncodePitch(t0, t0Frac, 1, &t0Min, &t0Max)
			e.paramSet.P2 = uint8(p2)
		}

		// 10d. Filtered adaptive excitation y1
		if e.cfg.Variant == VariantG729A {
			var memZero [params.M]float32
			dsp.SynthesisFilter(y1[:], e.oldExc[excOffset:excOffset+params.L_SUBFR], curAp, memZero[:], false)
		} else {
			pitch.InterpExcitation(e.oldExc[:], excOffset, t0, t0Frac, params.L_SUBFR)
			dsp.Convolution(y1[:], h1[:], e.oldExc[excOffset:excOffset+params.L_SUBFR])
		}

		var gCoeff [5]float32
		gainPit, gCoeff2 := pitch.PitchGain(xn[:], y1[:])
		gCoeff[0] = gCoeff2[0]
		gCoeff[1] = gCoeff2[1]

		// 10e. Pitch taming
		taming := codebook.TestErr(t0, t0Frac, &e.excErr)
		if taming == 1 && gainPit > params.GP_CLIP {
			gainPit = params.GP_CLIP
		}

		// 10f. Target for algebraic codebook search
		for i := 0; i < params.L_SUBFR; i++ {
			xn2[i] = xn[i] - y1[i]*gainPit
		}

		// 10g. Algebraic codebook search
		var cPos, cSign int
		var codeVec, y2 [params.L_SUBFR]float32

		if e.cfg.Variant == VariantG729A {
			cPos, cSign, codeVec, y2 = codebook.SearchAlgebraicA(xn2[:], h1[:], t0, e.sharp)
		} else {
			cPos, cSign, codeVec, y2 = codebook.SearchAlgebraicFull(xn2[:], h1[:], t0, e.sharp, iSubfr, &e.fullSearchExtra)
		}

		if subfrIdx == 0 {
			e.paramSet.C1 = uint16(cPos)
			e.paramSet.S1 = uint8(cSign)
		} else {
			e.paramSet.C2 = uint16(cPos)
			e.paramSet.S2 = uint8(cSign)
		}

		// 10h. Gain quantization
		codebook.CorrXY2(xn[:], y1[:], y2[:], &gCoeff)
		ga, gb, qGainPit, qGainCode := codebook.QuantizeGain(codeVec[:], &gCoeff, &e.gainPred, taming)
		subfrGainPit[subfrIdx] = qGainPit
		subfrGainCode[subfrIdx] = qGainCode

		if subfrIdx == 0 {
			e.paramSet.GA1 = uint8(ga)
			e.paramSet.GB1 = uint8(gb)
		} else {
			e.paramSet.GA2 = uint8(ga)
			e.paramSet.GB2 = uint8(gb)
		}

		// 10i. Update pitch sharpening
		e.sharp = qGainPit
		if e.sharp > params.SHARPMAX {
			e.sharp = params.SHARPMAX
		}
		if e.sharp < params.SHARPMIN {
			e.sharp = params.SHARPMIN
		}

		// 10j. Update excitation
		for i := 0; i < params.L_SUBFR; i++ {
			e.oldExc[excOffset+i] = qGainPit*e.oldExc[excOffset+i] + qGainCode*codeVec[i]
		}

		// 10k. Update taming error
		codebook.UpdateExcErr(qGainPit, t0, &e.excErr)

		// 10l. Update weighting filter memories
		for i := params.L_SUBFR - params.M; i < params.L_SUBFR; i++ {
			j := i - (params.L_SUBFR - params.M)
			e.memW0[j] = xn[i] - qGainPit*y1[i] - qGainCode*y2[i]
		}
		if e.cfg.Variant == VariantG729 {
			var synth [params.L_SUBFR]float32
			dsp.SynthesisFilter(synth[:], e.oldExc[excOffset:excOffset+params.L_SUBFR], curAq, e.memSyn[:], true)
			for i := params.L_SUBFR - params.M; i < params.L_SUBFR; i++ {
				j := i - (params.L_SUBFR - params.M)
				e.memErr[j] = speech[iSubfr+i] - synth[i]
			}
		}
	}

	// 11. Shift buffers for next frame
	copy(e.oldSpeech[:params.L_TOTAL-params.L_FRAME], e.oldSpeech[params.L_FRAME:])
	copy(e.oldWsp[:params.PIT_MAX], e.oldWsp[params.L_FRAME:params.L_FRAME+params.PIT_MAX])
	copy(e.oldExc[:params.L_PAST_EXC], e.oldExc[params.L_FRAME:params.L_FRAME+params.L_PAST_EXC])

	// 12. Serialize 80 bits into dst
	bits.Pack(dst[:params.BYTES_PER_FRAME], &e.paramSet)

	if e.cfg.OnDiagnostic != nil {
		e.invokeDiagnostic(DiagnosticStats{
			FrameIndex:   e.frameCount,
			FrameType:    FrameSpeech,
			EnergyDB:     energyDB,
			ZeroCrossing: zeroCrossingRate(speech),
			VADMarker:    marker,
			PitchLag:     tOp,
			GainPitch:    subfrGainPit,
			GainCode:     subfrGainCode,
			ClippedCount: clippedCount,
			LPCFallback:  levFallback,
		})
	}

	e.stats.TotalFrames++
	e.stats.SpeechFrames++
	e.stats.BytesEmitted += params.BYTES_PER_FRAME
	e.stats.ClippedSamples += int64(clippedCount)

	return params.BYTES_PER_FRAME, FrameSpeech, nil
}

// Stats returns cumulative operational telemetry for this encoder.
func (e *Encoder) Stats() EncoderStats {
	if e == nil {
		return EncoderStats{}
	}
	return e.stats
}

// EncodeBatch encodes multiple consecutive 10 ms speech frames (multiples of 80 int16 samples) into dst.
// For zero-allocation batch encoding, use EncodeBatchInto.
func (e *Encoder) EncodeBatch(dst []byte, src []int16) (int, []FrameType, error) {
	if e == nil {
		return 0, nil, ErrNilEncoder
	}
	if len(src)%params.L_FRAME != 0 || len(src) == 0 {
		return 0, nil, errors.New("g729: src length must be a non-zero multiple of 80 samples")
	}
	numFrames := len(src) / params.L_FRAME
	frameTypes := make([]FrameType, numFrames)
	n, encoded, err := e.EncodeBatchInto(dst, src, frameTypes)
	return n, frameTypes[:encoded], err
}

// EncodeBatchInto encodes multiple 10 ms frames without allocating a frameTypes slice.
// Caller must provide frameTypes with length >= len(src)/80.
// Returns total bytes written, number of frames actually encoded, and an error if any.
func (e *Encoder) EncodeBatchInto(dst []byte, src []int16, frameTypes []FrameType) (n int, numFrames int, err error) {
	if e == nil {
		return 0, 0, ErrNilEncoder
	}
	if len(src)%params.L_FRAME != 0 || len(src) == 0 {
		return 0, 0, errors.New("g729: src length must be a non-zero multiple of 80 samples")
	}
	total := len(src) / params.L_FRAME
	if len(dst) < total*params.BYTES_PER_FRAME {
		return 0, 0, ErrInvalidOutputLen
	}
	if len(frameTypes) < total {
		return 0, 0, errors.New("g729: frameTypes length must be >= len(src)/80")
	}

	for i := 0; i < total; i++ {
		frameSrc := src[i*params.L_FRAME : (i+1)*params.L_FRAME]
		frameDst := dst[n:]
		fn, ft, encErr := e.Encode(frameDst, frameSrc)
		if encErr != nil {
			return n, i, encErr
		}
		frameTypes[i] = ft
		n += fn
	}

	return n, total, nil
}

// invokeDiagnostic calls the optional OnDiagnostic callback, recovering from any panic.
// Panics are not re-raised to keep the codec running, but are counted in
// EncoderStats.DiagnosticPanics so callers can detect a buggy callback via Stats().
func (e *Encoder) invokeDiagnostic(stats DiagnosticStats) {
	if e.cfg.OnDiagnostic == nil {
		return
	}
	defer func() {
		if recover() != nil {
			e.stats.DiagnosticPanics++
		}
	}()
	e.cfg.OnDiagnostic(stats)
}

// zeroCrossingRate returns the normalised zero-crossing rate of a speech frame
// in [0, 1]. Shared by both the SID/DTX and active speech diagnostic paths.
func zeroCrossingRate(speech []float32) float32 {
	if len(speech) < 2 {
		return 0
	}
	var zc float32
	prev := speech[0]
	for _, s := range speech[1:] {
		if prev*s < 0 {
			zc++
		}
		prev = s
	}
	return zc / float32(len(speech))
}
