package vad

import (
	"math"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/codebook"
	"github.com/selawe/go-g729/internal/dsp"
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

func TestVADStateReset(t *testing.T) {
	s := NewVADState()
	if s.Min != math.MaxFloat32 {
		t.Errorf("expected Min to be MaxFloat32, got %v", s.Min)
	}
	if s.Flag != 1 {
		t.Errorf("expected Flag to be 1, got %v", s.Flag)
	}
	for i, v := range s.MeanLSF {
		if v != 0.0 {
			t.Errorf("expected MeanLSF[%d] to be 0, got %v", i, v)
		}
	}
}

func TestVADMakeDecBoundaries(t *testing.T) {
	// Extreme voice conditions
	if res := makeDec(100.0, -100.0, 1.0, 0.5); res != Voice {
		t.Errorf("expected Voice for high energy/distortion, got %v", res)
	}

	// Typical stationary background noise parameters
	if res := makeDec(0.0, 0.0, 0.0001, 0.0); res != Noise {
		t.Errorf("expected Noise for low distortion/energy change, got %v", res)
	}
}

func TestVADSpeechVsSilence(t *testing.T) {
	s := NewVADState()

	var lsf [params.M]float32
	for i := 0; i < params.M; i++ {
		lsf[i] = float32(i+1) * 0.28
	}

	// 1. Feed low-energy silence
	var rxxSilence [params.NP + 1]float32
	rxxSilence[0] = 10.0 // Low energy ~16 dB
	var speechSilence [params.L_TOTAL]float32

	prevMarker := Noise
	pprevMarker := Noise
	for frame := 1; frame <= 40; frame++ {
		marker, _ := s.Process(0.1, lsf[:], rxxSilence[:], speechSilence[:], frame, prevMarker, pprevMarker)
		pprevMarker = prevMarker
		prevMarker = marker
	}
	if prevMarker != Noise {
		t.Errorf("expected silence frames to be classified as Noise, got %v", prevMarker)
	}

	// 2. Feed high-energy synthetic tone (Voice)
	s.Reset()
	var speechTone [params.L_TOTAL]float32
	for i := 0; i < params.L_TOTAL; i++ {
		speechTone[i] = 1000.0 * float32(math.Sin(2.0*math.Pi*float64(i)*1000.0/8000.0))
	}
	var rxxTone [params.NP + 1]float32
	dsp.Autocorr(rxxTone[:], speechTone[:], nil, params.NP)

	prevMarker = Voice
	pprevMarker = Voice
	var voiceCount int
	for frame := 1; frame <= 50; frame++ {
		marker, _ := s.Process(-0.5, lsf[:], rxxTone[:], speechTone[:], frame, prevMarker, pprevMarker)
		pprevMarker = prevMarker
		prevMarker = marker
		if marker == Voice {
			voiceCount++
		}
	}
	if voiceCount < 40 {
		t.Errorf("expected tone frames to be classified mostly as Voice, got %d/50", voiceCount)
	}
}

func TestQuaSidgainRoundTrip(t *testing.T) {
	// Test minimum energy boundary
	enerQ, idx := QuantEnergy(params.MIN_ENER)
	if idx != 0 || enerQ != -12.0 {
		t.Errorf("expected MIN_ENER to map to idx 0, -12 dB, got idx=%d, enerQ=%f", idx, enerQ)
	}

	// Test high energy boundary (> 65 dB)
	enerQ, idx = QuantEnergy(5.0e7)
	if idx != 31 || enerQ != 66.0 {
		t.Errorf("expected high energy to map to idx 31, 66 dB, got idx=%d, enerQ=%f", idx, enerQ)
	}

	// Test intermediate energy
	enerQ, idx = QuantEnergy(100.0) // 10*log10(100) = 20 dB
	if idx < 6 || idx > 15 {
		t.Errorf("unexpected index %d for 20 dB energy", idx)
	}
	expectedQ := 2.0*float32(idx) + 4.0
	if enerQ != expectedQ {
		t.Errorf("expected enerQ %f, got %f", expectedQ, enerQ)
	}

	// Verify TabSidGain values are monotonically increasing
	for i := 1; i < len(tables.TabSidGain); i++ {
		if tables.TabSidGain[i] <= tables.TabSidGain[i-1] {
			t.Errorf("TabSidGain not strictly increasing at index %d: %f <= %f",
				i, tables.TabSidGain[i], tables.TabSidGain[i-1])
		}
	}
}

func TestLsfqNoiseRoundTrip(t *testing.T) {
	var freqPrevEnc [params.MA_NP][params.M]float32
	var freqPrevDec [params.MA_NP][params.M]float32
	for k := 0; k < params.MA_NP; k++ {
		copy(freqPrevEnc[k][:], tables.FreqPrevReset[:])
		copy(freqPrevDec[k][:], tables.FreqPrevReset[:])
	}

	// Create typical stable LSP vector
	var lspOrig [params.M]float32
	for i := 0; i < params.M; i++ {
		lsfVal := float32(i+1) * 0.28
		lspOrig[i] = float32(math.Cos(float64(lsfVal)))
	}

	var lspQEnc [params.M]float32
	mode, l1, l2 := LsfqNoise(lspOrig[:], &lspQEnc, &freqPrevEnc)

	if mode < 0 || mode > 1 {
		t.Errorf("invalid mode %d", mode)
	}
	if l1 < 0 || l1 > 31 {
		t.Errorf("invalid l1 index %d", l1)
	}
	if l2 < 0 || l2 > 15 {
		t.Errorf("invalid l2 index %d", l2)
	}

	var lspQDec [params.M]float32
	SidLsfqDecode(mode, l1, l2, &lspQDec, &freqPrevDec)

	for i := 0; i < params.M; i++ {
		diff := float32(math.Abs(float64(lspQEnc[i] - lspQDec[i])))
		if diff > 1e-5 {
			t.Errorf("encoder/decoder LSP mismatch at %d: enc=%f, dec=%f, diff=%f",
				i, lspQEnc[i], lspQDec[i], diff)
		}
	}

	// Check that freqPrev is updated identically on both sides
	for k := 0; k < params.MA_NP; k++ {
		for i := 0; i < params.M; i++ {
			diff := float32(math.Abs(float64(freqPrevEnc[k][i] - freqPrevDec[k][i])))
			if diff > 1e-5 {
				t.Errorf("freqPrev memory mismatch at [%d][%d]: enc=%f, dec=%f",
					k, i, freqPrevEnc[k][i], freqPrevDec[k][i])
			}
		}
	}
}

func TestSIDPackUnpack(t *testing.T) {
	testCases := []struct {
		mode      int
		l1        int
		l2        int
		gainIndex int
	}{
		{0, 0, 0, 0},
		{1, 31, 15, 31},
		{0, 17, 9, 23},
		{1, 5, 12, 8},
	}

	for i, tc := range testCases {
		orig := SIDParams{
			Transmitted: true,
			Mode:        tc.mode,
			L1:          tc.l1,
			L2:          tc.l2,
			GainIndex:   tc.gainIndex,
		}

		packed := PackSID(orig)
		unpacked := UnpackSID(packed)

		if unpacked.Mode != orig.Mode {
			t.Errorf("case %d: Mode mismatch: expected %d, got %d", i, orig.Mode, unpacked.Mode)
		}
		if unpacked.L1 != orig.L1 {
			t.Errorf("case %d: L1 mismatch: expected %d, got %d", i, orig.L1, unpacked.L1)
		}
		if unpacked.L2 != orig.L2 {
			t.Errorf("case %d: L2 mismatch: expected %d, got %d", i, orig.L2, unpacked.L2)
		}
		if unpacked.GainIndex != orig.GainIndex {
			t.Errorf("case %d: GainIndex mismatch: expected %d, got %d", i, orig.GainIndex, unpacked.GainIndex)
		}

		// Verify padding bit (bit 0 of 2nd byte) is 0
		if (packed[1] & 1) != 0 {
			t.Errorf("case %d: LSB padding bit must be 0, got %d", i, packed[1]&1)
		}
	}
}

func TestDTXEncoderTransitions(t *testing.T) {
	dtx := NewDTXEncoderState()

	var excBuf [params.EXC_BUF_LEN]float32
	var lspOldQ [params.M]float32
	for i := 0; i < params.M; i++ {
		lspOldQ[i] = float32(math.Cos(float64(tables.FreqPrevReset[i])))
	}
	var aq [2 * params.MP1]float32
	var freqPrev [params.MA_NP][params.M]float32
	for k := 0; k < params.MA_NP; k++ {
		copy(freqPrev[k][:], tables.FreqPrevReset[:])
	}
	seed := params.INIT_SEED
	var excErr [4]float32
	codebook.InitExcErr(&excErr)

	// Simulated autocorrelation for silence (realistic 16-bit PCM scale)
	var rSilence [params.MP1]float32
	rSilence[0] = 2000.0
	for i := 1; i < params.MP1; i++ {
		rSilence[i] = 400.0 * float32(math.Pow(0.8, float64(i)))
	}

	// 1. First silence frame (pastVad = 1) -> Must transmit SID frame
	dtx.UpdateCNG(rSilence[:], 0)
	sid1 := dtx.CodCNG(excBuf[:], params.L_PAST_EXC, 1, &lspOldQ, &aq, &freqPrev, &seed, &excErr)
	if !sid1.Transmitted {
		t.Fatalf("first silence frame must transmit SID frame")
	}

	// 2. Next 2 silence frames (pastVad = 0, countFr0 < FR_SID_MIN) -> Must NOT transmit
	for f := 0; f < 2; f++ {
		dtx.UpdateCNG(rSilence[:], 0)
		sidNext := dtx.CodCNG(excBuf[:], params.L_PAST_EXC, 0, &lspOldQ, &aq, &freqPrev, &seed, &excErr)
		if sidNext.Transmitted {
			t.Errorf("subsequent silence frame %d should be untransmitted", f+1)
		}
	}

	// 3. Shift energy by > 6 dB -> Should trigger new SID frame after FR_SID_MIN
	var rLoudSilence [params.MP1]float32
	rLoudSilence[0] = 20000.0 // 10 dB increase
	for i := 1; i < params.MP1; i++ {
		rLoudSilence[i] = 4000.0 * float32(math.Pow(0.8, float64(i)))
	}

	dtx.UpdateCNG(rLoudSilence[:], 0)
	sidShift := dtx.CodCNG(excBuf[:], params.L_PAST_EXC, 0, &lspOldQ, &aq, &freqPrev, &seed, &excErr)
	if !sidShift.Transmitted {
		t.Errorf("energy shift frame should trigger SID transmission")
	}
}

func TestCNGExcitationEnergy(t *testing.T) {
	var excBuf [params.EXC_BUF_LEN]float32
	seed := params.INIT_SEED

	// 1. Zero gain excitation
	CalcExcRand(0.0, excBuf[:], params.L_PAST_EXC, &seed, false, nil)
	for i := params.L_PAST_EXC; i < params.L_PAST_EXC+params.L_FRAME; i++ {
		if excBuf[i] != 0.0 {
			t.Errorf("expected 0 excitation for 0 gain, got %f at index %d", excBuf[i], i)
		}
	}

	// 2. Non-zero gain excitation
	seed = params.INIT_SEED
	targetGain := float32(20.0)
	var excErr [4]float32
	codebook.InitExcErr(&excErr)
	CalcExcRand(targetGain, excBuf[:], params.L_PAST_EXC, &seed, true, &excErr)

	var energy float32
	for i := params.L_PAST_EXC; i < params.L_PAST_EXC+params.L_FRAME; i++ {
		val := excBuf[i]
		if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			t.Fatalf("excitation contained NaN/Inf at index %d", i)
		}
		energy += val * val
	}
	if energy <= 0.0 {
		t.Errorf("expected non-zero energy, got %f", energy)
	}
}

func TestCNGDecoderDecCNG(t *testing.T) {
	cngDec := NewCNGDecoderState()

	var excBuf [params.EXC_BUF_LEN]float32
	var lspOld [params.M]float32
	for i := 0; i < params.M; i++ {
		lspOld[i] = float32(math.Cos(float64(tables.FreqPrevReset[i])))
	}
	var aT [2 * params.MP1]float32
	var freqPrev [params.MA_NP][params.M]float32
	for k := 0; k < params.MA_NP; k++ {
		copy(freqPrev[k][:], tables.FreqPrevReset[:])
	}

	sidParams := SIDParams{
		Transmitted: true,
		Mode:        0,
		L1:          5,
		L2:          8,
		GainIndex:   12,
	}

	// Decode SID frame
	cngDec.DecCNG(true, 50.0, sidParams, excBuf[:], params.L_PAST_EXC, &lspOld, &aT, &freqPrev)

	if cngDec.SidGain != tables.TabSidGain[12] {
		t.Errorf("expected SidGain %f, got %f", tables.TabSidGain[12], cngDec.SidGain)
	}
	if cngDec.CurGain != cngDec.SidGain {
		t.Errorf("first silence frame CurGain must match SidGain: %f vs %f", cngDec.CurGain, cngDec.SidGain)
	}

	// Decode untransmitted frame
	prevGain := cngDec.CurGain
	cngDec.DecCNG(false, 50.0, SIDParams{Transmitted: false}, excBuf[:], params.L_PAST_EXC, &lspOld, &aT, &freqPrev)

	expectedSmoothedGain := prevGain*params.A_GAIN0 + cngDec.SidGain*params.A_GAIN1
	diff := float32(math.Abs(float64(cngDec.CurGain - expectedSmoothedGain)))
	if diff > 1e-4 {
		t.Errorf("gain smoothing mismatch: expected %f, got %f", expectedSmoothedGain, cngDec.CurGain)
	}
}

func TestVADConcurrency(t *testing.T) {
	const goroutines = 16
	const framesPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gId int) {
			defer wg.Done()

			vState := NewVADState()
			dtxEnc := NewDTXEncoderState()
			cngDec := NewCNGDecoderState()

			var excBufEnc [params.EXC_BUF_LEN]float32
			var excBufDec [params.EXC_BUF_LEN]float32
			var lspOldEnc [params.M]float32
			var lspOldDec [params.M]float32
			for i := 0; i < params.M; i++ {
				v := float32(math.Cos(float64(tables.FreqPrevReset[i])))
				lspOldEnc[i] = v
				lspOldDec[i] = v
			}
			var aqEnc [2 * params.MP1]float32
			var aTDec [2 * params.MP1]float32
			var freqPrevEnc [params.MA_NP][params.M]float32
			var freqPrevDec [params.MA_NP][params.M]float32
			for k := 0; k < params.MA_NP; k++ {
				copy(freqPrevEnc[k][:], tables.FreqPrevReset[:])
				copy(freqPrevDec[k][:], tables.FreqPrevReset[:])
			}
			seedEnc := int16(11111 + gId*100)
			var excErr [4]float32
			codebook.InitExcErr(&excErr)

			var speech [params.L_TOTAL]float32
			var rxx [params.NP + 1]float32
			rxx[0] = 50.0 + float32(gId)*5.0
			for i := 1; i <= params.NP; i++ {
				rxx[i] = 10.0 * float32(math.Pow(0.8, float64(i)))
			}

			prevMarker := Voice
			pprevMarker := Voice

			for f := 1; f <= framesPerGoroutine; f++ {
				marker, _ := vState.Process(0.1, tables.FreqPrevReset[:], rxx[:], speech[:], f, prevMarker, pprevMarker)
				dtxEnc.UpdateCNG(rxx[:params.MP1], marker)

				if marker == Noise {
					sid := dtxEnc.CodCNG(excBufEnc[:], params.L_PAST_EXC, prevMarker, &lspOldEnc, &aqEnc, &freqPrevEnc, &seedEnc, &excErr)
					cngDec.DecCNG(prevMarker == Voice, 50.0, sid, excBufDec[:], params.L_PAST_EXC, &lspOldDec, &aTDec, &freqPrevDec)
				}
				pprevMarker = prevMarker
				prevMarker = marker
			}
		}(g)
	}

	wg.Wait()
}

func BenchmarkVADProcess(b *testing.B) {
	s := NewVADState()
	var lsf [params.M]float32
	copy(lsf[:], tables.FreqPrevReset[:])
	var rxx [params.NP + 1]float32
	rxx[0] = 100.0
	for i := 1; i <= params.NP; i++ {
		rxx[i] = 20.0 * float32(math.Pow(0.8, float64(i)))
	}
	var sigpp [params.L_TOTAL]float32

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		s.Process(0.2, lsf[:], rxx[:], sigpp[:], 150, Voice, Voice)
	}
}

func BenchmarkDTXCodCNG(b *testing.B) {
	dtx := NewDTXEncoderState()
	var excBuf [params.EXC_BUF_LEN]float32
	var lspOldQ [params.M]float32
	copy(lspOldQ[:], tables.FreqPrevReset[:])
	var aq [2 * params.MP1]float32
	var freqPrev [params.MA_NP][params.M]float32
	for k := 0; k < params.MA_NP; k++ {
		copy(freqPrev[k][:], tables.FreqPrevReset[:])
	}
	seed := params.INIT_SEED
	var excErr [4]float32
	codebook.InitExcErr(&excErr)

	var r [params.MP1]float32
	r[0] = 50.0
	for i := 1; i < params.MP1; i++ {
		r[i] = 10.0 * float32(math.Pow(0.8, float64(i)))
	}
	dtx.UpdateCNG(r[:], 0)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		dtx.CodCNG(excBuf[:], params.L_PAST_EXC, 1, &lspOldQ, &aq, &freqPrev, &seed, &excErr)
	}
}

func BenchmarkCNGCalcExcRand(b *testing.B) {
	var excBuf [params.EXC_BUF_LEN]float32
	seed := params.INIT_SEED
	var excErr [4]float32
	codebook.InitExcErr(&excErr)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		CalcExcRand(20.0, excBuf[:], params.L_PAST_EXC, &seed, true, &excErr)
	}
}

func BenchmarkCNGDecCNG(b *testing.B) {
	cngDec := NewCNGDecoderState()
	var excBuf [params.EXC_BUF_LEN]float32
	var lspOld [params.M]float32
	copy(lspOld[:], tables.FreqPrevReset[:])
	var aT [2 * params.MP1]float32
	var freqPrev [params.MA_NP][params.M]float32
	for k := 0; k < params.MA_NP; k++ {
		copy(freqPrev[k][:], tables.FreqPrevReset[:])
	}
	sid := SIDParams{
		Transmitted: true,
		Mode:        0,
		L1:          5,
		L2:          8,
		GainIndex:   12,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		cngDec.DecCNG(true, 50.0, sid, excBuf[:], params.L_PAST_EXC, &lspOld, &aT, &freqPrev)
	}
}
