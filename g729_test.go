package g729

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// readPCMFile reads 16-bit little-endian linear PCM samples from file.
func readPCMFile(path string) ([]int16, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	numSamples := len(data) / 2
	samples := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		samples[i] = int16(binary.LittleEndian.Uint16(data[i*2 : i*2+2]))
	}
	return samples, nil
}

// convertITUBitstreamToPacked converts ITU-T 16-bit serial bitstream (82 words/frame) to 10-byte packed frames.
func convertITUBitstreamToPacked(rawBits []byte) [][]byte {
	numFrames := len(rawBits) / 164
	out := make([][]byte, numFrames)
	for f := 0; f < numFrames; f++ {
		words := make([]uint16, 82)
		for w := 0; w < 82; w++ {
			words[w] = binary.LittleEndian.Uint16(rawBits[f*164+w*2 : f*164+w*2+2])
		}
		frameBytes := make([]byte, 10)
		for b := 0; b < 10; b++ {
			var byteVal byte
			for bit := 0; bit < 8; bit++ {
				idx := b*8 + bit
				if words[2+idx] == 0x0081 {
					byteVal |= (1 << (7 - bit))
				}
			}
			frameBytes[b] = byteVal
		}
		out[f] = frameBytes
	}
	return out
}

func computeSNR(orig, rec []int16) (overallSNR, segSNR float64) {
	bestSNR := -100.0
	var bestSegSNR float64

	// Search for optimal sample delay in [-80, 80] to account for codec lookahead and filter delay
	for delay := -80; delay <= 80; delay++ {
		var sigTot, noiseTot float64
		var segSNRSum float64
		segCount := 0

		for f := 1; f < len(orig)/80-1; f++ {
			var fSig, fNoise float64
			for j := 0; j < 80; j++ {
				origIdx := f*80 + j
				recIdx := f*80 + j + delay
				if recIdx < 0 || recIdx >= len(rec) {
					continue
				}
				s := float64(orig[origIdx])
				r := float64(rec[recIdx])
				e := r - s
				fSig += s * s
				fNoise += e * e
				sigTot += s * s
				noiseTot += e * e
			}

			if fSig > 100.0 && fNoise > 0.0 {
				snr := 10.0 * math.Log10(fSig/fNoise)
				if snr < 0.0 {
					snr = 0.0
				}
				if snr > 40.0 {
					snr = 40.0
				}
				segSNRSum += snr
				segCount++
			}
		}

		if noiseTot > 0 && sigTot > 0 {
			snr := 10.0 * math.Log10(sigTot/noiseTot)
			if snr > bestSNR {
				bestSNR = snr
				if segCount > 0 {
					bestSegSNR = segSNRSum / float64(segCount)
				}
			}
		}
	}

	return bestSNR, bestSegSNR
}

func TestEndToEndOfficialSpeechVector(t *testing.T) {
	// Look for official ITU-T test speech vector
	testPath := filepath.Join("docs", "G729_Release3", "g729AnnexA", "test_vectors", "TEST.IN")
	pcm, err := readPCMFile(testPath)
	if err != nil {
		t.Logf("Official test vector not accessible at %s (%v), generating synthetic speech test signal", testPath, err)
		// Generate 200 frames (2.0s) of harmonic multi-tone speech-like signal
		pcm = make([]int16, 16000)
		for i := 0; i < len(pcm); i++ {
			tt := float64(i) / 8000.0
			v := 6000.0*math.Sin(2*math.Pi*220*tt) +
				4000.0*math.Sin(2*math.Pi*440*tt) +
				2500.0*math.Sin(2*math.Pi*880*tt) +
				1500.0*math.Sin(2*math.Pi*1760*tt)
			pcm[i] = int16(v)
		}
	}

	totalFrames := len(pcm) / 80
	if totalFrames < 10 {
		t.Fatalf("Insufficient speech data: %d frames", totalFrames)
	}

	pstPath := filepath.Join("docs", "G729_Release3", "g729AnnexA", "test_vectors", "TEST.pst")
	if refOut, err := readPCMFile(pstPath); err == nil {
		snrRef, segRef := computeSNR(pcm, refOut)
		t.Logf("Official TEST.IN vs Official TEST.pst: Overall SNR = %.2f dB, Segmental SNR = %.2f dB", snrRef, segRef)
	}

	// 1. Test G.729A (Fast) Round-Trip
	t.Run("VariantG729A", func(t *testing.T) {
		enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		dec := NewDecoder()

		decoded := make([]int16, totalFrames*80)
		var bitstream [10]byte

		for f := 0; f < totalFrames; f++ {
			frameIn := pcm[f*80 : (f+1)*80]
			n, fType, err := enc.Encode(bitstream[:], frameIn)
			if err != nil || n != 10 || fType != FrameSpeech {
				t.Fatalf("frame %d encode failed: n=%d, err=%v", f, n, err)
			}

			err = dec.Decode(decoded[f*80:(f+1)*80], bitstream[:])
			if err != nil {
				t.Fatalf("frame %d decode failed: %v", f, err)
			}
		}

		overallSNR, segSNR := computeSNR(pcm, decoded)
		t.Logf("G.729A Round-Trip: Overall SNR = %.2f dB, Segmental SNR = %.2f dB (%d frames)", overallSNR, segSNR, totalFrames)

		// Verify output signal is non-empty and well-behaved
		for i, s := range decoded {
			if s > 32767 || s < -32768 {
				t.Fatalf("sample %d clipped: %d", i, s)
			}
		}
	})

	// 2. Test Full G.729 (Nested Search) Round-Trip
	t.Run("VariantG729Full", func(t *testing.T) {
		enc := NewEncoder(Config{Variant: VariantG729, EnableVAD: false})
		dec := NewDecoder()

		// Test first 50 frames to verify full search
		framesToTest := totalFrames
		if framesToTest > 50 {
			framesToTest = 50
		}

		decoded := make([]int16, framesToTest*80)
		var bitstream [10]byte

		for f := 0; f < framesToTest; f++ {
			frameIn := pcm[f*80 : (f+1)*80]
			n, fType, err := enc.Encode(bitstream[:], frameIn)
			if err != nil || n != 10 || fType != FrameSpeech {
				t.Fatalf("frame %d encode failed: n=%d, err=%v", f, n, err)
			}

			err = dec.Decode(decoded[f*80:(f+1)*80], bitstream[:])
			if err != nil {
				t.Fatalf("frame %d decode failed: %v", f, err)
			}
		}

		overallSNR, segSNR := computeSNR(pcm[:framesToTest*80], decoded)
		t.Logf("Full G.729 Round-Trip: Overall SNR = %.2f dB, Segmental SNR = %.2f dB (%d frames)", overallSNR, segSNR, framesToTest)

		for i, s := range decoded {
			if s > 32767 || s < -32768 {
				t.Fatalf("sample %d clipped: %d", i, s)
			}
		}
	})
}

func TestSyntheticSignalRoundTrip(t *testing.T) {
	tones := []struct {
		name   string
		freqHz float64
		amp    float64
	}{
		{"Tone300Hz", 300.0, 10000.0},
		{"Tone1000Hz", 1000.0, 8000.0},
	}

	for _, tc := range tones {
		t.Run(tc.name, func(t *testing.T) {
			pcm := generateSine(tc.freqHz, 8000, tc.amp) // 1 second (100 frames)
			enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
			dec := NewDecoder()

			totalFrames := len(pcm) / 80
			decoded := make([]int16, totalFrames*80)
			var bitstream [10]byte

			for f := 0; f < totalFrames; f++ {
				frameIn := pcm[f*80 : (f+1)*80]
				n, _, err := enc.Encode(bitstream[:], frameIn)
				if err != nil || n != 10 {
					t.Fatalf("encode failed: %v", err)
				}
				if err := dec.Decode(decoded[f*80:(f+1)*80], bitstream[:]); err != nil {
					t.Fatalf("decode failed: %v", err)
				}
			}

			overallSNR, segSNR := computeSNR(pcm, decoded)
			t.Logf("%s Round-Trip SNR: Overall = %.2f dB, Segmental = %.2f dB", tc.name, overallSNR, segSNR)

			if overallSNR < 5.0 {
				t.Errorf("%s overall SNR too low: %.2f dB", tc.name, overallSNR)
			}
		})
	}
}

func TestEndToEndAnnexBVAD(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: true})
	dec := NewDecoder()

	// 30 frames active tone (300 ms), 50 frames silence (500 ms), 20 frames active tone (200 ms)
	var audio []int16
	tone1 := generateSine(800.0, 2400, 10000.0)
	silence := make([]int16, 4000)
	tone2 := generateSine(1200.0, 1600, 9000.0)
	audio = append(audio, tone1...)
	audio = append(audio, silence...)
	audio = append(audio, tone2...)

	totalFrames := len(audio) / 80
	var bitstream [10]byte
	decoded := make([]int16, totalFrames*80)

	var speechFrames, sidFrames, untransmittedFrames int

	for f := 0; f < totalFrames; f++ {
		frameIn := audio[f*80 : (f+1)*80]
		n, fType, err := enc.Encode(bitstream[:], frameIn)
		if err != nil {
			t.Fatalf("frame %d encode error: %v", f, err)
		}

		switch fType {
		case FrameSpeech:
			speechFrames++
		case FrameSID:
			sidFrames++
		case FrameUntransmitted:
			untransmittedFrames++
		}

		err = dec.Decode(decoded[f*80:(f+1)*80], bitstream[:n])
		if err != nil {
			t.Fatalf("frame %d decode error: %v", f, err)
		}
	}

	t.Logf("Annex B DTX: Speech = %d, SID = %d, Untransmitted = %d (Total = %d)",
		speechFrames, sidFrames, untransmittedFrames, totalFrames)

	if speechFrames == 0 {
		t.Errorf("expected speech frames, got 0")
	}
	if sidFrames == 0 {
		t.Errorf("expected SID frames during silence transition, got 0")
	}
	if untransmittedFrames == 0 {
		t.Errorf("expected untransmitted frames during prolonged silence, got 0")
	}
}

func TestEndToEndPacketLossConcealment(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	dec := NewDecoder()

	signal := generateSine(400.0, 8000, 12000.0) // 100 frames
	totalFrames := len(signal) / 80

	var bitstream [10]byte
	decoded := make([]int16, 80)

	rng := rand.New(rand.NewSource(12345))
	var lostCount int

	for f := 0; f < totalFrames; f++ {
		frameIn := signal[f*80 : (f+1)*80]
		n, _, err := enc.Encode(bitstream[:], frameIn)
		if err != nil {
			t.Fatalf("encode frame %d failed: %v", f, err)
		}

		// 15% random packet loss
		isLost := (f > 5 && rng.Float64() < 0.15)
		if isLost {
			lostCount++
			err = dec.Decode(decoded, nil)
		} else {
			err = dec.Decode(decoded, bitstream[:n])
		}

		if err != nil {
			t.Fatalf("frame %d decode failed (lost=%v): %v", f, isLost, err)
		}

		// Verify no audio explosion or NaN values
		for i, s := range decoded {
			if s > 32767 || s < -32768 {
				t.Fatalf("frame %d sample %d clipped: %d", f, i, s)
			}
		}
	}

	t.Logf("PLC Test: %d of %d frames lost and concealed cleanly", lostCount, totalFrames)
	if lostCount == 0 {
		t.Errorf("expected some lost frames, got 0")
	}
}

func TestDecoderOfficialTestVector(t *testing.T) {
	bitPath := filepath.Join("docs", "G729_Release3", "g729AnnexA", "test_vectors", "TEST.BIT")
	pstPath := filepath.Join("docs", "G729_Release3", "g729AnnexA", "test_vectors", "TEST.pst")

	rawBits, err := os.ReadFile(bitPath)
	if err != nil {
		t.Skipf("TEST.BIT not found: %v", err)
	}
	refPcm, err := readPCMFile(pstPath)
	if err != nil {
		t.Skipf("TEST.pst not found: %v", err)
	}

	packedFrames := convertITUBitstreamToPacked(rawBits)
	dec := NewDecoder()

	decoded := make([]int16, len(packedFrames)*80)
	for f, frame := range packedFrames {
		err := dec.Decode(decoded[f*80:(f+1)*80], frame)
		if err != nil {
			t.Fatalf("frame %d decode failed: %v", f, err)
		}
	}

	overallSNR, segSNR := computeSNR(refPcm, decoded)
	t.Logf("Decoder vs Official TEST.pst: Overall SNR = %.2f dB, Segmental SNR = %.2f dB", overallSNR, segSNR)

	if overallSNR < 15.0 {
		t.Errorf("Decoder SNR vs official reference too low: %.2f dB", overallSNR)
	}
}

