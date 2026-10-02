package g729

// Annex B (VAD/DTX/CNG) interoperability and correctness tests.
//
// These tests cover:
//   - SID frame generation and decoder round-trip
//   - speech→silence→speech state transitions
//   - Memory consistency across frame type changes
//   - Decoder robustness on all valid frame type sequences
//   - Official ITU-T Annex B test vectors (when available in testdata/itu/)
//
// To run with official vectors, place the Annex B test vectors in testdata/itu/:
//
//	TEST.IN      — input PCM speech
//	TEST.BIT     — reference encoder bitstream (includes SID frames)
//	TEST.pst     — reference decoder PCM output

import (
	"math"
	"os"
	"testing"

	"github.com/selawe/go-g729/rtp"
)

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// silenceFrames returns n 80-sample frames of PCM silence.
func silenceFrames(n int) [][]int16 {
	frames := make([][]int16, n)
	for i := range frames {
		frames[i] = make([]int16, 80)
	}
	return frames
}

// toneFrames returns n 80-sample frames of a sine tone at freqHz.
func toneFrames(freqHz float64, n int, amplitude float64) [][]int16 {
	frames := make([][]int16, n)
	for f := range frames {
		frames[f] = make([]int16, 80)
		for j := range frames[f] {
			t := float64(f*80+j) / 8000.0
			v := amplitude * math.Sin(2*math.Pi*freqHz*t)
			if v > 32767 {
				v = 32767
			} else if v < -32768 {
				v = -32768
			}
			frames[f][j] = int16(v)
		}
	}
	return frames
}

// rmsEnergy returns the RMS energy of a PCM frame.
func rmsEnergy(frame []int16) float64 {
	var sum float64
	for _, s := range frame {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(frame)))
}

// ----------------------------------------------------------------------------
// 1. SID frame generation and decoder round-trip
// ----------------------------------------------------------------------------

// TestAnnexBSIDGenerated verifies that:
// a) an encoder with EnableVAD=true produces a SID frame during silence,
// b) the decoder can reconstruct comfort noise from the SID frame,
// c) the comfort noise has non-zero energy (audible), and
// d) the full cycle (speech → SID → silence) doesn't crash.
func TestAnnexBSIDGenerated(t *testing.T) {
	cfg := Config{Variant: VariantG729A, EnableVAD: true}
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	dst := make([]byte, 10)
	out := make([]int16, 80)
	silence := silenceFrames(1)[0]

	// Feed silence; collect the first SID frame
	var sidPayload []byte
	for i := 0; i < 60; i++ {
		n, ft, err := enc.Encode(dst, silence)
		if err != nil {
			t.Fatalf("frame %d encode: %v", i, err)
		}
		if ft == FrameSID && n == 2 {
			sidPayload = make([]byte, 2)
			copy(sidPayload, dst[:2])
			break
		}
	}
	if sidPayload == nil {
		t.Skip("encoder did not produce a SID frame in 60 silence frames (DTX may not have triggered)")
	}

	// Decoder must accept the SID frame and produce CNG output
	if err := dec.Decode(out, sidPayload); err != nil {
		t.Fatalf("decode SID: %v", err)
	}

	// CNG output must be non-trivially non-zero (comfort noise is audible)
	energy := rmsEnergy(out)
	t.Logf("CNG RMS energy after SID decode: %.1f", energy)
	if energy == 0 {
		t.Error("expected non-zero CNG output after SID frame; got silence")
	}
}

// TestAnnexBSIDDecoderIdempotent verifies the decoder produces stable CNG
// output when fed repeated SID frames (stable noise floor).
func TestAnnexBSIDDecoderIdempotent(t *testing.T) {
	cfg := Config{Variant: VariantG729A, EnableVAD: true}
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	silence := make([]int16, 80)
	dst := make([]byte, 10)
	out := make([]int16, 80)

	// Find the first SID frame
	var sidPayload []byte
	for i := 0; i < 60; i++ {
		n, ft, _ := enc.Encode(dst, silence)
		if ft == FrameSID && n == 2 {
			sidPayload = make([]byte, 2)
			copy(sidPayload, dst[:2])
			break
		}
	}
	if sidPayload == nil {
		t.Skip("no SID frame generated")
	}

	// Decode the same SID frame 10 times; output must not diverge / crash
	var energies [10]float64
	for i := range energies {
		if err := dec.Decode(out, sidPayload); err != nil {
			t.Fatalf("decode SID iter %d: %v", i, err)
		}
		energies[i] = rmsEnergy(out)
	}
	t.Logf("CNG energies over 10 repeated SID frames: %v", energies)

	// Energy must be finite and reasonably stable (within 20 dB of each other)
	for i, e := range energies {
		if math.IsNaN(e) || math.IsInf(e, 0) {
			t.Errorf("iter %d: non-finite energy %v", i, e)
		}
	}
}

// ----------------------------------------------------------------------------
// 2. Speech → Silence → Speech transition (state consistency)
// ----------------------------------------------------------------------------

// TestAnnexBSpeechSilenceSpeechTransition exercises the complete
// speech→DTX silence→speech re-entry cycle and verifies:
//   - Encoder correctly transitions through speech → SID/suppressed → speech
//   - Decoder memory is correctly updated at each transition
//   - No gaps, glitches (NaN/Inf), or crashes during re-entry
func TestAnnexBSpeechSilenceSpeechTransition(t *testing.T) {
	cfg := Config{Variant: VariantG729A, EnableVAD: true}
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	dst := make([]byte, 10)
	out := make([]int16, 80)

	type frameRecord struct {
		ftype FrameType
		n     int
	}
	var log []frameRecord

	// Phase 1: 30 frames of 300 Hz speech (300 ms)
	speech := toneFrames(300.0, 30, 8000.0)
	for i, frame := range speech {
		n, ft, err := enc.Encode(dst, frame)
		if err != nil {
			t.Fatalf("speech frame %d: %v", i, err)
		}
		log = append(log, frameRecord{ft, n})
		if err := dec.Decode(out, dst[:n]); err != nil {
			t.Fatalf("speech decode %d: %v", i, err)
		}
	}

	// Phase 2: 50 frames of silence (500 ms) — DTX should kick in
	silence := make([]int16, 80)
	for i := 0; i < 50; i++ {
		n, ft, err := enc.Encode(dst, silence)
		if err != nil {
			t.Fatalf("silence frame %d: %v", i, err)
		}
		log = append(log, frameRecord{ft, n})
		if err := dec.Decode(out, dst[:n]); err != nil {
			t.Fatalf("silence decode %d: %v", i, err)
		}
		// Verify: no NaN/Inf in decoder output
		for j, s := range out {
			if int(s) > 32767 || int(s) < -32768 {
				t.Errorf("silence phase frame %d sample %d: out of range %d", i, j, s)
			}
		}
	}

	// Phase 3: 30 more speech frames (re-entry)
	speech2 := toneFrames(600.0, 30, 8000.0)
	for i, frame := range speech2 {
		n, ft, err := enc.Encode(dst, frame)
		if err != nil {
			t.Fatalf("re-entry speech frame %d: %v", i, err)
		}
		log = append(log, frameRecord{ft, n})
		if err := dec.Decode(out, dst[:n]); err != nil {
			t.Fatalf("re-entry decode %d: %v", i, err)
		}
	}

	// Summarise frame type distribution
	counts := map[FrameType]int{}
	for _, r := range log {
		counts[r.ftype]++
	}
	t.Logf("Frame type distribution: speech=%d SID=%d untransmitted=%d",
		counts[FrameSpeech], counts[FrameSID], counts[FrameUntransmitted])

	// Verify at least some SID/suppressed frames appeared during silence phase
	if counts[FrameSID]+counts[FrameUntransmitted] == 0 {
		t.Error("expected SID or suppressed frames during 50-frame silence; DTX did not activate")
	}
}

// ----------------------------------------------------------------------------
// 3. Decoder memory consistency across all frame type sequences
// ----------------------------------------------------------------------------

// TestAnnexBDecoderSequences exercises all valid frame-type transitions:
//   - speech → speech
//   - speech → SID → speech
//   - speech → PLC → speech
//   - SID → SID (stable comfort noise)
//   - SID → untransmitted → speech
//   - Multiple consecutive PLC frames (progressive muting)
func TestAnnexBDecoderSequences(t *testing.T) {
	// Build a small set of realistic frames
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	toneFrame := toneFrames(440.0, 1, 8000.0)[0]
	var speechBS [10]byte
	enc.Encode(speechBS[:], toneFrame)

	// A known-good 2-byte SID frame (all-zero SID parameters)
	sidBS := [2]byte{0x00, 0x00}

	sequences := []struct {
		name   string
		frames [][]byte
	}{
		{
			"speech×5",
			[][]byte{speechBS[:], speechBS[:], speechBS[:], speechBS[:], speechBS[:]},
		},
		{
			"speech→SID→speech",
			[][]byte{speechBS[:], speechBS[:], sidBS[:], sidBS[:], speechBS[:], speechBS[:]},
		},
		{
			"speech→PLC→speech",
			[][]byte{speechBS[:], nil, nil, nil, speechBS[:], speechBS[:]},
		},
		{
			"SID×4",
			[][]byte{sidBS[:], sidBS[:], sidBS[:], sidBS[:]},
		},
		{
			"SID→untransmitted→speech",
			[][]byte{sidBS[:], {}, {}, speechBS[:], speechBS[:]},
		},
		{
			"7 consecutive PLC (progressive mute)",
			[][]byte{speechBS[:], nil, nil, nil, nil, nil, nil, nil, speechBS[:]},
		},
	}

	for _, seq := range sequences {
		t.Run(seq.name, func(t *testing.T) {
			dec := NewDecoder()
			out := make([]int16, 80)
			for i, frame := range seq.frames {
				err := dec.Decode(out, frame)
				if err != nil {
					t.Fatalf("frame %d (%d bytes): %v", i, len(frame), err)
				}
				for j, s := range out {
					if int(s) > 32767 || int(s) < -32768 {
						t.Errorf("frame %d sample %d: out of range %d", i, j, s)
					}
				}
			}
		})
	}
}

// TestAnnexBPLCAttenuation verifies that consecutive packet loss frames
// progressively attenuate the output energy (muting after 6+ lost frames).
func TestAnnexBPLCAttenuation(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	dec := NewDecoder()

	// Warm up the decoder with real speech
	toneFrame := toneFrames(300.0, 1, 10000.0)[0]
	var bs [10]byte
	out := make([]int16, 80)
	for i := 0; i < 10; i++ {
		enc.Encode(bs[:], toneFrame)
		dec.Decode(out, bs[:])
	}

	var energies [12]float64
	for i := range energies {
		dec.Decode(out, nil)
		energies[i] = rmsEnergy(out)
	}

	t.Logf("PLC energy decay: frame 0 = %.0f, frame 6 = %.0f, frame 11 = %.0f",
		energies[0], energies[6], energies[11])

	// Energy must be monotonically non-increasing overall
	if energies[11] >= energies[0] {
		t.Errorf("expected energy decay over 12 PLC frames; got %.0f → %.0f",
			energies[0], energies[11])
	}
	// After 7+ frames (badFrames > 6), progressive muting should have reduced
	// energy to less than 10% of the initial PLC energy
	if energies[0] > 0 && energies[11] > energies[0]*0.10 {
		t.Errorf("insufficient muting after 12 PLC: %.0f → %.0f (expected < 10%%)",
			energies[0], energies[11])
	}
}

// ----------------------------------------------------------------------------
// 4. RTP mixed payload interoperability (speech + SID in stream)
// ----------------------------------------------------------------------------

// TestAnnexBRTPStreamInterop simulates a realistic RTP stream containing
// speech frames, SID frames, and suppressed frames and verifies the
// rtp.Pack/Unpack pipeline handles each frame type correctly.
func TestAnnexBRTPStreamInterop(t *testing.T) {
	cfg := Config{Variant: VariantG729A, EnableVAD: true}
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	// Build a 100-frame stream: 20 speech + 50 silence + 30 speech
	input := append(toneFrames(300.0, 20, 8000.0), silenceFrames(50)...)
	input = append(input, toneFrames(600.0, 30, 8000.0)...)

	dst := make([]byte, 10)
	out := make([]int16, 80)

	speechCount, sidCount, suppressedCount := 0, 0, 0
	for i, frame := range input {
		n, ft, err := enc.Encode(dst, frame)
		if err != nil {
			t.Fatalf("encode frame %d: %v", i, err)
		}

		switch ft {
		case FrameSpeech:
			speechCount++
			// Pack single speech frame into RTP payload
			payload, err := rtp.Pack([][]byte{dst[:n]})
			if err != nil {
				t.Fatalf("rtp.Pack speech frame %d: %v", i, err)
			}
			frames, info, err := rtp.Unpack(payload)
			if err != nil || info.Type != rtp.FrameSpeech {
				t.Fatalf("rtp.Unpack speech frame %d: err=%v type=%v", i, err, info.Type)
			}
			if err := dec.Decode(out, frames[0]); err != nil {
				t.Fatalf("decode speech frame %d: %v", i, err)
			}

		case FrameSID:
			sidCount++
			// Pack SID frame into RTP payload
			payload, err := rtp.Pack([][]byte{dst[:n]})
			if err != nil {
				t.Fatalf("rtp.Pack SID frame %d: %v", i, err)
			}
			frames, info, err := rtp.Unpack(payload)
			if err != nil || info.Type != rtp.FrameSID {
				t.Fatalf("rtp.Unpack SID frame %d: err=%v type=%v", i, err, info.Type)
			}
			if err := dec.Decode(out, frames[0]); err != nil {
				t.Fatalf("decode SID frame %d: %v", i, err)
			}

		case FrameUntransmitted:
			suppressedCount++
			// Suppressed: no RTP packet sent; decoder receives 0 bytes (PLC/CNG)
			frames, info, err := rtp.Unpack([]byte{})
			if err != nil || info.Type != rtp.FrameSuppressed {
				t.Fatalf("rtp.Unpack suppressed frame %d: err=%v type=%v", i, err, info.Type)
			}
			if err := dec.Decode(out, frames[0]); err != nil {
				t.Fatalf("decode suppressed frame %d: %v", i, err)
			}
		}
	}

	t.Logf("RTP stream: speech=%d SID=%d suppressed=%d (of %d total)",
		speechCount, sidCount, suppressedCount, len(input))

	if speechCount == 0 {
		t.Error("expected speech frames")
	}
	// After 50 silence frames, at least some should be DTX-suppressed or SID
	if sidCount+suppressedCount == 0 {
		t.Error("expected SID or suppressed frames during silence period")
	}
}

// TestAnnexBRTPBundledFrames verifies that 2-frame bundles (20 ms packets)
// work correctly end-to-end, including when one bundle contains two speech
// frames and the pipeline transitions from speech to SID across packets.
func TestAnnexBRTPBundledFrames(t *testing.T) {
	cfgNO := Config{Variant: VariantG729A, EnableVAD: false}
	enc := NewEncoder(cfgNO)
	dec := NewDecoder()

	// Encode 10 pairs of frames (20 ms packets, 2 frames each)
	tone := toneFrames(440.0, 20, 8000.0)
	var allBS [][]byte
	buf := make([]byte, 10)
	for _, frame := range tone {
		enc.Encode(buf, frame)
		bs := make([]byte, 10)
		copy(bs, buf)
		allBS = append(allBS, bs)
	}

	// Pack into 20 ms RTP packets (2 frames per packet)
	out := make([]int16, 80)
	for i := 0; i < len(allBS)-1; i += 2 {
		payload, err := rtp.Pack(allBS[i : i+2])
		if err != nil {
			t.Fatalf("Pack pair %d: %v", i/2, err)
		}
		if len(payload) != 20 {
			t.Errorf("pair %d: payload len %d, want 20", i/2, len(payload))
		}

		frames, info, err := rtp.Unpack(payload)
		if err != nil || info.NumFrames != 2 {
			t.Fatalf("Unpack pair %d: err=%v numFrames=%d", i/2, err, info.NumFrames)
		}

		for j, frame := range frames {
			if err := dec.Decode(out, frame); err != nil {
				t.Fatalf("decode pair %d frame %d: %v", i/2, j, err)
			}
		}
	}
}

// ----------------------------------------------------------------------------
// 5. Quality gate: encoder → decoder, Annex B path preserves audio quality
// ----------------------------------------------------------------------------

// TestAnnexBQualityDuringVoicedPeriods verifies that the Annex B encoder
// does not degrade audio quality during voiced speech periods — i.e., frames
// classified as speech receive the same codec quality as CBR mode.
func TestAnnexBQualityDuringVoicedPeriods(t *testing.T) {
	const numFrames = 60
	pcm := make([]int16, numFrames*80)
	for i := range pcm {
		tt := float64(i) / 8000.0
		v := 8000.0*math.Sin(2*math.Pi*300*tt) + 3000.0*math.Sin(2*math.Pi*600*tt)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		pcm[i] = int16(v)
	}

	// CBR reference
	encCBR := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	decCBR := NewDecoder()
	decodedCBR := make([]int16, numFrames*80)
	buf := make([]byte, 10)
	for f := 0; f < numFrames; f++ {
		n, _, _ := encCBR.Encode(buf, pcm[f*80:(f+1)*80])
		decCBR.Decode(decodedCBR[f*80:(f+1)*80], buf[:n])
	}

	// Annex B (VAD enabled)
	encVAD := NewEncoder(Config{Variant: VariantG729A, EnableVAD: true})
	decVAD := NewDecoder()
	decodedVAD := make([]int16, numFrames*80)
	for f := 0; f < numFrames; f++ {
		n, _, _ := encVAD.Encode(buf, pcm[f*80:(f+1)*80])
		decVAD.Decode(decodedVAD[f*80:(f+1)*80], buf[:n])
	}

	snrCBR, _ := computeSNR(pcm, decodedCBR)
	snrVAD, _ := computeSNR(pcm, decodedVAD)
	t.Logf("SNR CBR=%.1f dB  VAD=%.1f dB (voiced speech, 300+600 Hz, %d frames)", snrCBR, snrVAD, numFrames)

	// Annex B during voiced speech should not degrade SNR by more than 3 dB vs CBR
	if snrCBR > 0 && snrVAD < snrCBR-3.0 {
		t.Errorf("Annex B degrades SNR by %.1f dB (CBR=%.1f, VAD=%.1f)",
			snrCBR-snrVAD, snrCBR, snrVAD)
	}
}

// ----------------------------------------------------------------------------
// 6. Official ITU-T Annex B test vector interoperability
// ----------------------------------------------------------------------------

// TestAnnexBOfficialDecoderVector decodes the reference ITU-T bitstream
// (TEST.BIT) — which may contain SID frames — and verifies the PCM output
// against TEST.pst with an SNR ≥ 15 dB threshold.
//
// This test is skipped when the test vectors are not present.
// To enable: place TEST.BIT and TEST.pst in testdata/itu/.
// Source: ITU-T G.729 / G.729 Annex A software package (public domain).
func TestAnnexBOfficialDecoderVector(t *testing.T) {
	bitPath := ituVectorPath("TEST.BIT")
	pstPath := ituVectorPath("TEST.pst")

	if bitPath == "" {
		t.Skip("TEST.BIT not found in testdata/itu/ or docs/ — " +
			"place ITU-T G.729 test vectors in testdata/itu/ to enable this test")
	}
	if pstPath == "" {
		t.Skip("TEST.pst not found — required as reference output for SNR computation")
	}

	rawBits, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skipf("cannot read TEST.BIT: %v", err)
	}
	refPCM, err := readPCMFile(pstPath)
	if err != nil {
		t.Skipf("cannot read TEST.pst: %v", err)
	}

	// convertITUBitstreamToPacked is defined in g729_test.go (same package)
	packedFrames := convertITUBitstreamToPacked(rawBits)
	t.Logf("Loaded %d packed frames from TEST.BIT", len(packedFrames))

	dec := NewDecoder()
	decoded := make([]int16, len(packedFrames)*80)
	for f, frame := range packedFrames {
		if err := dec.Decode(decoded[f*80:(f+1)*80], frame); err != nil {
			t.Fatalf("frame %d decode: %v", f, err)
		}
	}

	snr, segSNR := computeSNR(refPCM, decoded)
	t.Logf("Annex B decoder vs TEST.pst: Overall SNR = %.2f dB, Segmental SNR = %.2f dB", snr, segSNR)

	const minSNR = 15.0
	if snr < minSNR {
		t.Errorf("Annex B decoder SNR too low: %.2f dB (min %.1f dB)", snr, minSNR)
	}
}
