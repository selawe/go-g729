package g729

import (
	"sync"
	"testing"
)

func TestDecoderValidation(t *testing.T) {
	dec := NewDecoder()

	var dstGood [80]int16
	var dstBad [79]int16
	var srcGoodSpeech [10]byte
	var srcGoodSID [2]byte
	var srcBadLen [5]byte

	// 1. Output buffer too small
	err := dec.Decode(dstBad[:], srcGoodSpeech[:])
	if err != ErrInvalidOutputLen {
		t.Fatalf("expected ErrInvalidOutputLen, got: %v", err)
	}

	// 2. Invalid frame length
	err = dec.Decode(dstGood[:], srcBadLen[:])
	if err != ErrInvalidFrameLen {
		t.Fatalf("expected ErrInvalidFrameLen, got: %v", err)
	}

	// 3. Valid frame lengths (0, 2, 10) must not return ErrInvalidFrameLen
	if err := dec.Decode(dstGood[:], srcGoodSpeech[:]); err != nil {
		t.Fatalf("unexpected error on 10-byte frame: %v", err)
	}
	if err := dec.Decode(dstGood[:], srcGoodSID[:]); err != nil {
		t.Fatalf("unexpected error on 2-byte frame: %v", err)
	}
	if err := dec.Decode(dstGood[:], nil); err != nil {
		t.Fatalf("unexpected error on nil/0-byte frame: %v", err)
	}
}

func TestDecoderSpeechRoundTrip(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	// Generate 100 frames of 440 Hz sine tone
	signal := generateSine(440.0, 8000, 10000.0)
	totalFrames := len(signal) / 80

	var bitstream [10]byte
	var decoded [80]int16

	for f := 0; f < totalFrames; f++ {
		frameIn := signal[f*80 : (f+1)*80]
		n, fType, err := enc.Encode(bitstream[:], frameIn)
		if err != nil {
			t.Fatalf("encode frame %d failed: %v", f, err)
		}
		if n != 10 || fType != FrameSpeech {
			t.Fatalf("frame %d: expected 10 bytes FrameSpeech, got n=%d, type=%v", f, n, fType)
		}

		err = dec.Decode(decoded[:], bitstream[:])
		if err != nil {
			t.Fatalf("decode frame %d failed: %v", f, err)
		}

	}
}

func TestDecoderParityError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	signal := generateSine(300.0, 160, 8000.0)
	var bitstream [10]byte
	var decoded [80]int16

	// Frame 0: normal
	_, _, err := enc.Encode(bitstream[:], signal[0:80])
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	if err := dec.Decode(decoded[:], bitstream[:]); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	// Frame 1: corrupt parity bit P0 (bit 5 in byte 3)
	_, _, err = enc.Encode(bitstream[:], signal[80:160])
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	bitstream[3] ^= 0x20 // Flip P0 bit

	// Decoding should succeed using concealment for subframe 0 pitch
	if err := dec.Decode(decoded[:], bitstream[:]); err != nil {
		t.Fatalf("decode with parity error failed: %v", err)
	}

}

func TestDecoderComfortNoise(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = true
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	// 10 frames of speech followed by 30 frames of silence
	speech := generateSine(1000.0, 800, 12000.0)
	silence := make([]int16, 2400)
	combined := append(speech, silence...)
	totalFrames := len(combined) / 80

	var bitstream [10]byte
	var decoded [80]int16

	for f := 0; f < totalFrames; f++ {
		frameIn := combined[f*80 : (f+1)*80]
		n, _, err := enc.Encode(bitstream[:], frameIn)
		if err != nil {
			t.Fatalf("frame %d encode failed: %v", f, err)
		}

		err = dec.Decode(decoded[:], bitstream[:n])
		if err != nil {
			t.Fatalf("frame %d decode failed (n=%d): %v", f, n, err)
		}

	}
}

func TestDecoderPLCAndMuting(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	// 5 good frames of tone
	signal := generateSine(500.0, 400, 15000.0)
	var bitstream [10]byte
	var decoded [80]int16

	for f := 0; f < 5; f++ {
		_, _, _ = enc.Encode(bitstream[:], signal[f*80:(f+1)*80])
		if err := dec.Decode(decoded[:], bitstream[:]); err != nil {
			t.Fatalf("good frame %d failed: %v", f, err)
		}
	}

	// Calculate energy of last good frame
	var lastGoodEnergy float64
	for _, s := range decoded {
		lastGoodEnergy += float64(s) * float64(s)
	}

	// Simulate 20 consecutive lost frames (200 ms packet loss)
	var energyHistory [20]float64
	for f := 0; f < 20; f++ {
		if err := dec.Decode(decoded[:], nil); err != nil {
			t.Fatalf("PLC frame %d failed: %v", f, err)
		}

		var frameEnergy float64
		for _, s := range decoded {
			frameEnergy += float64(s) * float64(s)
		}
		energyHistory[f] = frameEnergy
	}

	// Energy must monotonically decrease and muting must occur
	if energyHistory[19] >= energyHistory[0] {
		t.Fatalf("expected energy decay during PLC, got frame 0: %f, frame 19: %f", energyHistory[0], energyHistory[19])
	}

	// After 20 lost frames (badFrames=20 > 6), signal should be deeply attenuated
	if energyHistory[19] > lastGoodEnergy*0.1 {
		t.Fatalf("expected deep attenuation after 20 lost frames, got %f vs last good %f", energyHistory[19], lastGoodEnergy)
	}
}

func TestDecoderResetIdempotency(t *testing.T) {
	cfg := DefaultConfig()
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)
	dec1 := NewDecoder()
	dec2 := NewDecoder()

	signal := generateSine(440.0, 800, 8000.0)
	var bitstream [10][10]byte
	for f := 0; f < 10; f++ {
		_, _, _ = enc.Encode(bitstream[f][:], signal[f*80:(f+1)*80])
	}

	var out1 [10][80]int16
	var out2 [10][80]int16

	for f := 0; f < 10; f++ {
		_ = dec1.Decode(out1[f][:], bitstream[f][:])
	}

	dec1.Reset()
	for f := 0; f < 10; f++ {
		_ = dec1.Decode(out1[f][:], bitstream[f][:])
		_ = dec2.Decode(out2[f][:], bitstream[f][:])
		if out1[f] != out2[f] {
			t.Fatalf("frame %d: dec1 after Reset() differs from fresh dec2", f)
		}
	}
}

func TestDecoderConcurrency(t *testing.T) {
	const numGoroutines = 16
	const numFrames = 30

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		freq := 300.0 + float64(g)*40.0

		go func(fHz float64, gid int) {
			defer wg.Done()

			cfg := DefaultConfig()
			cfg.EnableVAD = (gid%2 == 0)
			enc := NewEncoder(cfg)
			dec := NewDecoder()

			sig := generateSine(fHz, numFrames*80, 7000.0)
			var bitstream [10]byte
			var pcmOut [80]int16

			for f := 0; f < numFrames; f++ {
				// Inject occasional packet loss on odd goroutines
				if gid%3 == 0 && f%5 == 0 {
					if err := dec.Decode(pcmOut[:], nil); err != nil {
						t.Errorf("PLC decode error: %v", err)
						return
					}
					continue
				}

				n, _, err := enc.Encode(bitstream[:], sig[f*80:(f+1)*80])
				if err != nil {
					t.Errorf("concurrency encode error: %v", err)
					return
				}

				if err := dec.Decode(pcmOut[:], bitstream[:n]); err != nil {
					t.Errorf("concurrency decode error: %v", err)
					return
				}
			}
		}(freq, g)
	}

	wg.Wait()
}

func TestDecoderAdversarialPitchBounds(t *testing.T) {
	dec := NewDecoder()
	var dst [80]int16

	// Bitstream with P1 = 0 and P2 = 0 (all zeros frame)
	var zeroFrame [10]byte
	if err := dec.Decode(dst[:], zeroFrame[:]); err != nil {
		t.Fatalf("decode zero frame failed: %v", err)
	}

	// Bitstream with all 0xFF bytes
	var ffFrame [10]byte
	for i := range ffFrame {
		ffFrame[i] = 0xFF
	}
	if err := dec.Decode(dst[:], ffFrame[:]); err != nil {
		t.Fatalf("decode 0xFF frame failed: %v", err)
	}
}

func TestDecoderStats(t *testing.T) {
	dec := NewDecoder()
	var dst [80]int16
	var speechFrame [10]byte
	var sidFrame [2]byte

	// 5 speech frames
	for i := 0; i < 5; i++ {
		_ = dec.Decode(dst[:], speechFrame[:])
	}
	// 3 PLC erasures (following active speech)
	for i := 0; i < 3; i++ {
		_ = dec.Decode(dst[:], nil)
	}
	// 2 SID frames
	for i := 0; i < 2; i++ {
		_ = dec.Decode(dst[:], sidFrame[:])
	}
	// 2 Untransmitted DTX silence frames (following SID)
	for i := 0; i < 2; i++ {
		_ = dec.Decode(dst[:], nil)
	}

	st := dec.Stats()
	if st.TotalFrames != 12 || st.SpeechFrames != 5 || st.ConcealedFrames != 3 || st.SIDFrames != 2 || st.Untransmitted != 2 {
		t.Errorf("unexpected decoder stats: %+v", st)
	}

	dec.Reset()
	stReset := dec.Stats()
	if stReset.TotalFrames != 0 || stReset.ConcealedFrames != 0 {
		t.Errorf("decoder stats not cleared on reset: %+v", stReset)
	}
}

// TestDecoderStatsParityErrors verifies that intentionally corrupted parity
// bits are counted in DecoderStats.ParityErrors — useful for detecting bit-error
// rates on lossy transports.
func TestDecoderStatsParityErrors(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	dec := NewDecoder()

	frame := generateSine(440.0, 80, 8000.0)
	var bs [10]byte
	if n, _, err := enc.Encode(bs[:], frame); err != nil || n != 10 {
		t.Fatalf("encode failed: n=%d err=%v", n, err)
	}

	var dst [80]int16
	// Baseline: correct parity, no counter increment.
	if err := dec.Decode(dst[:], bs[:]); err != nil {
		t.Fatalf("baseline decode failed: %v", err)
	}
	if got := dec.Stats().ParityErrors; got != 0 {
		t.Errorf("baseline ParityErrors = %d, want 0", got)
	}

	// Corrupt the parity bit: P0 lives in bit 5 of byte 3 (see bits.Pack layout).
	// Flip it to force a parity mismatch.
	corrupt := bs
	corrupt[3] ^= 1 << 5

	before := dec.Stats().ParityErrors
	if err := dec.Decode(dst[:], corrupt[:]); err != nil {
		t.Fatalf("corrupt decode failed: %v", err)
	}
	after := dec.Stats().ParityErrors
	if after != before+1 {
		t.Errorf("ParityErrors = %d after corrupt frame, want %d", after, before+1)
	}

	// Reset must clear the counter.
	dec.Reset()
	if got := dec.Stats().ParityErrors; got != 0 {
		t.Errorf("ParityErrors after Reset = %d, want 0", got)
	}
}

func TestDecodeBatch(t *testing.T) {
	dec := NewDecoder()
	f1 := make([]byte, 10)
	f2 := make([]byte, 10)
	dst := make([]int16, 160)

	err := dec.DecodeBatch(dst, [][]byte{f1, f2})
	if err != nil {
		t.Fatalf("DecodeBatch failed: %v", err)
	}

	// Buffer too small
	err = dec.DecodeBatch(make([]int16, 100), [][]byte{f1, f2})
	if err != ErrInvalidOutputLen {
		t.Fatalf("expected ErrInvalidOutputLen, got %v", err)
	}
}

func BenchmarkDecodeSpeech(b *testing.B) {
	cfg := DefaultConfig()
	cfg.EnableVAD = false
	enc := NewEncoder(cfg)
	dec := NewDecoder()

	frame := generateSine(1000.0, 80, 8000.0)
	var bitstream [10]byte
	_, _, _ = enc.Encode(bitstream[:], frame)

	var dst [80]int16

	b.ReportAllocs()
	for b.Loop() {
		_ = dec.Decode(dst[:], bitstream[:])
	}
	b.StopTimer()
	reportFrameThroughput(b, 1)
}

func BenchmarkDecodeSID(b *testing.B) {
	dec := NewDecoder()
	// Synthetic SID frame (2 bytes)
	sidBytes := [2]byte{0x55, 0xAA}
	var dst [80]int16

	b.ReportAllocs()
	for b.Loop() {
		_ = dec.Decode(dst[:], sidBytes[:])
	}
	b.StopTimer()
	reportFrameThroughput(b, 1)
}

func BenchmarkDecodePLC(b *testing.B) {
	dec := NewDecoder()
	var dst [80]int16

	b.ReportAllocs()
	for b.Loop() {
		_ = dec.Decode(dst[:], nil)
	}
	b.StopTimer()
	reportFrameThroughput(b, 1)
}

func TestDecoderPLCLagExtrapolation(t *testing.T) {
	dec := NewDecoder()
	if dec.oldT0 != 60 {
		t.Fatalf("expected initial oldT0=60, got %d", dec.oldT0)
	}

	var dst [80]int16
	// 1st lost frame (ITU-T G.729 §4.4.1):
	// Subframe 0: uses T0 = 60, then oldT0 becomes 61
	// Subframe 1: uses T0 = 61, then oldT0 becomes 62
	if err := dec.Decode(dst[:], nil); err != nil {
		t.Fatalf("decode nil failed: %v", err)
	}
	if dec.oldT0 != 62 {
		t.Errorf("after 1 lost frame (2 subframes): expected oldT0=62, got %d", dec.oldT0)
	}

	// After many consecutive lost frames, oldT0 must clamp at PIT_MAX (143)
	for i := 0; i < 100; i++ {
		_ = dec.Decode(dst[:], nil)
	}
	if dec.oldT0 != 143 {
		t.Errorf("expected oldT0 clamped to PIT_MAX (143), got %d", dec.oldT0)
	}
}
