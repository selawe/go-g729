package g729

import (
	"math"
	"sync"
	"testing"
)

// generateSine generates a 16-bit PCM sine wave at freqHz with sample rate 8000 Hz.
func generateSine(freqHz float64, numSamples int, amplitude float64) []int16 {
	out := make([]int16, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / 8000.0
		val := amplitude * math.Sin(2.0*math.Pi*freqHz*t)
		if val > 32767 {
			val = 32767
		} else if val < -32768 {
			val = -32768
		}
		out[i] = int16(val)
	}
	return out
}

func TestEncoderValidation(t *testing.T) {
	enc := NewEncoder(DefaultConfig())

	var dst [10]byte
	var srcBadLen [79]int16
	var srcGood [80]int16
	var dstBadLen [9]byte

	// Bad input length
	_, _, err := enc.Encode(dst[:], srcBadLen[:])
	if err != ErrInvalidInputLen {
		t.Fatalf("expected ErrInvalidInputLen, got: %v", err)
	}

	// Bad output length
	_, _, err = enc.Encode(dstBadLen[:], srcGood[:])
	if err != ErrInvalidOutputLen {
		t.Fatalf("expected ErrInvalidOutputLen, got: %v", err)
	}
}

func TestEncoderVariants(t *testing.T) {
	variants := []struct {
		name string
		v    Variant
	}{
		{"G729A", VariantG729A},
		{"G729Full", VariantG729},
	}

	// 1 second 440 Hz tone (100 frames)
	signal := generateSine(440.0, 8000, 8000.0)

	for _, tc := range variants {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Variant = tc.v
			cfg.EnableVAD = false

			enc := NewEncoder(cfg)
			var dst [10]byte

			for f := 0; f < 100; f++ {
				frame := signal[f*80 : (f+1)*80]
				n, fType, err := enc.Encode(dst[:], frame)
				if err != nil {
					t.Fatalf("frame %d encode failed: %v", f, err)
				}
				if n != 10 {
					t.Errorf("frame %d: expected 10 bytes, got %d", f, n)
				}
				if fType != FrameSpeech {
					t.Errorf("frame %d: expected FrameSpeech, got %v", f, fType)
				}
			}
		})
	}
}

func TestEncoderVADTransitions(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = true
	enc := NewEncoder(cfg)

	// Create 20 frames of tone (speech) followed by 50 frames of digital silence
	speech := generateSine(1000.0, 1600, 10000.0)
	silence := make([]int16, 4000)
	combined := append(speech, silence...)

	var dst [10]byte
	totalFrames := len(combined) / 80

	var speechCount, sidCount, untransmittedCount int

	for f := 0; f < totalFrames; f++ {
		frame := combined[f*80 : (f+1)*80]
		n, fType, err := enc.Encode(dst[:], frame)
		if err != nil {
			t.Fatalf("frame %d encode error: %v", f, err)
		}

		switch fType {
		case FrameSpeech:
			speechCount++
			if n != 10 {
				t.Errorf("frame %d: FrameSpeech must be 10 bytes, got %d", f, n)
			}
		case FrameSID:
			sidCount++
			if n != 2 {
				t.Errorf("frame %d: FrameSID must be 2 bytes, got %d", f, n)
			}
		case FrameUntransmitted:
			untransmittedCount++
			if n != 0 {
				t.Errorf("frame %d: FrameUntransmitted must be 0 bytes, got %d", f, n)
			}
		}
	}

	if speechCount == 0 {
		t.Errorf("expected some FrameSpeech, got %d", speechCount)
	}
	if sidCount+untransmittedCount == 0 {
		t.Errorf("expected VAD/DTX to detect silence and produce SID/Untransmitted frames, got SID=%d, Untransmitted=%d", sidCount, untransmittedCount)
	}
}

func TestEncoderResetIdempotency(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = false

	enc1 := NewEncoder(cfg)
	enc2 := NewEncoder(cfg)

	signal := generateSine(500.0, 800, 6000.0) // 10 frames
	var dst1 [10]byte
	var dst2 [10]byte

	// Run enc1 on signal
	var out1 [10][10]byte
	for f := 0; f < 10; f++ {
		frame := signal[f*80 : (f+1)*80]
		_, _, _ = enc1.Encode(dst1[:], frame)
		out1[f] = dst1
	}

	// Reset enc1 and re-run on signal
	enc1.Reset()
	for f := 0; f < 10; f++ {
		frame := signal[f*80 : (f+1)*80]
		_, _, _ = enc1.Encode(dst1[:], frame)
		_, _, _ = enc2.Encode(dst2[:], frame)

		if dst1 != out1[f] {
			t.Errorf("frame %d: enc1 after Reset() differs from initial run", f)
		}
		if dst1 != dst2 {
			t.Errorf("frame %d: enc1 after Reset() differs from fresh enc2", f)
		}
	}
}

func TestEncoderConcurrency(t *testing.T) {
	const numGoroutines = 16
	const numFrames = 25

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		freq := 200.0 + float64(g)*50.0
		variant := VariantG729A
		if g%2 == 1 {
			variant = VariantG729
		}

		go func(fHz float64, v Variant) {
			defer wg.Done()

			cfg := DefaultConfig()
			cfg.Variant = v
			cfg.EnableVAD = (g%4 == 0)
			enc := NewEncoder(cfg)

			sig := generateSine(fHz, numFrames*80, 5000.0)
			var dst [10]byte

			for f := 0; f < numFrames; f++ {
				frame := sig[f*80 : (f+1)*80]
				n, fType, err := enc.Encode(dst[:], frame)
				if err != nil {
					t.Errorf("concurrency encode failed: %v", err)
					return
				}
				if fType == FrameSpeech && n != 10 {
					t.Errorf("expected 10 bytes for speech, got %d", n)
				}
			}
		}(freq, variant)
	}

	wg.Wait()
}

func TestOnDiagnosticPanicSafety(t *testing.T) {
	panicked := false
	cfg := ProfileDiagnostic(func(stats DiagnosticStats) {
		panicked = true
		panic("user callback panicked intentionally")
	})
	enc := NewEncoder(cfg)

	frame := generateSine(1000.0, 80, 8000.0)
	var dst [10]byte

	n, ft, err := enc.Encode(dst[:], frame)
	if err != nil {
		t.Fatalf("unexpected encode error: %v", err)
	}
	if n != 10 || ft != FrameSpeech {
		t.Errorf("got n=%d, ft=%v; want 10, FrameSpeech", n, ft)
	}
	if !panicked {
		t.Error("expected diagnostic callback to have run")
	}
}

func TestEncoderStats(t *testing.T) {
	cfg := ProfileFast()
	enc := NewEncoder(cfg)

	frame := generateSine(1000.0, 80, 8000.0)
	var dst [10]byte

	for i := 0; i < 10; i++ {
		_, _, _ = enc.Encode(dst[:], frame)
	}

	st := enc.Stats()
	if st.TotalFrames != 10 || st.SpeechFrames != 10 || st.BytesEmitted != 100 {
		t.Errorf("unexpected encoder stats: %+v", st)
	}

	enc.Reset()
	stReset := enc.Stats()
	if stReset.TotalFrames != 0 || stReset.BytesEmitted != 0 {
		t.Errorf("stats not cleared on reset: %+v", stReset)
	}
}

func TestEncodeBatch(t *testing.T) {
	enc := NewEncoder(ProfileFast())

	// 2 frames (160 samples = 20 ms)
	src20ms := generateSine(1000.0, 160, 8000.0)
	dst := make([]byte, 20)

	n, ftypes, err := enc.EncodeBatch(dst, src20ms)
	if err != nil {
		t.Fatalf("EncodeBatch 20ms: %v", err)
	}
	if n != 20 || len(ftypes) != 2 || ftypes[0] != FrameSpeech || ftypes[1] != FrameSpeech {
		t.Errorf("unexpected batch result: n=%d types=%v", n, ftypes)
	}

	// Invalid input length (e.g. 100 samples)
	_, _, err = enc.EncodeBatch(dst, make([]int16, 100))
	if err == nil {
		t.Fatal("expected error for non-multiple of 80 samples, got nil")
	}

	// Buffer too small
	_, _, err = enc.EncodeBatch(make([]byte, 10), src20ms)
	if err != ErrInvalidOutputLen {
		t.Fatalf("expected ErrInvalidOutputLen, got %v", err)
	}
}

func TestEncodeBatchInto(t *testing.T) {
	enc := NewEncoder(ProfileFast())

	src20ms := generateSine(1000.0, 160, 8000.0)
	dst := make([]byte, 20)
	frameTypes := make([]FrameType, 2)

	n, encoded, err := enc.EncodeBatchInto(dst, src20ms, frameTypes)
	if err != nil {
		t.Fatalf("EncodeBatchInto: %v", err)
	}
	if n != 20 || encoded != 2 {
		t.Errorf("expected n=20 encoded=2, got n=%d encoded=%d", n, encoded)
	}
	if frameTypes[0] != FrameSpeech || frameTypes[1] != FrameSpeech {
		t.Errorf("unexpected frame types: %v", frameTypes)
	}

	// frameTypes too small
	_, _, err = enc.EncodeBatchInto(dst, src20ms, make([]FrameType, 1))
	if err == nil {
		t.Fatal("expected error for undersized frameTypes")
	}

	// dst too small
	_, _, err = enc.EncodeBatchInto(make([]byte, 10), src20ms, frameTypes)
	if err != ErrInvalidOutputLen {
		t.Fatalf("expected ErrInvalidOutputLen, got %v", err)
	}
}

func BenchmarkEncodeBatchInto(b *testing.B) {
	enc := NewEncoder(ProfileFast())
	src := generateSine(1000.0, 320, 8000.0) // 4 frames
	dst := make([]byte, 40)
	frameTypes := make([]FrameType, 4)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = enc.EncodeBatchInto(dst, src, frameTypes)
	}
}

func BenchmarkEncodeG729A(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Variant = VariantG729A
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)

	frame := generateSine(1000.0, 80, 8000.0)
	var dst [10]byte

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = enc.Encode(dst[:], frame)
	}
}

func BenchmarkEncodeG729Full(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Variant = VariantG729
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)

	frame := generateSine(1000.0, 80, 8000.0)
	var dst [10]byte

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = enc.Encode(dst[:], frame)
	}
}

func BenchmarkEncodeG729A_WithVAD(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Variant = VariantG729A
	cfg.EnableVAD = true
	enc := NewEncoder(cfg)

	frame := generateSine(1000.0, 80, 8000.0)
	var dst [10]byte

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = enc.Encode(dst[:], frame)
	}
}
