package g729

import (
	"bytes"
	"flag"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// updateGolden regenerates golden files when passed as -update-golden.
// Usage: go test -run TestGolden -update-golden .
var updateGolden = flag.Bool("update-golden", false, "regenerate golden files from current encoder output")

// goldenPath returns the golden-file path for the current CPU architecture.
// On amd64 the canonical testdata/golden/<name> is used. Other architectures
// (e.g. arm64 / Apple Silicon) can produce different float32 rounding that
// leads to different but valid ACELP codebook choices, so their golden files
// live in testdata/golden/<GOARCH>/<name>.  If the arch-specific file does not
// exist the calling test will skip gracefully rather than fail.
func goldenPath(name string) string {
	if runtime.GOARCH == "amd64" {
		return filepath.Join("testdata", "golden", name)
	}
	return filepath.Join("testdata", "golden", runtime.GOARCH, name)
}

// goldenSignal builds the deterministic 1-second synthetic multi-tone signal
// used as the canonical input for all golden file tests. Using a harmonic
// signal (not a pure tone) gives better coverage of the pitch estimator,
// LSP quantizer, and gain codebook.
func goldenSignal(numSamples int) []int16 {
	out := make([]int16, numSamples)
	for i := range out {
		t := float64(i) / 8000.0
		v := 5000.0*math.Sin(2*math.Pi*220*t) +
			3500.0*math.Sin(2*math.Pi*440*t) +
			2000.0*math.Sin(2*math.Pi*880*t) +
			1000.0*math.Sin(2*math.Pi*1760*t)
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = int16(v)
	}
	return out
}

// encodeGoldenSignal encodes the canonical signal with the given variant and
// returns the concatenated bitstream bytes.
func encodeGoldenSignal(t *testing.T, variant Variant) []byte {
	t.Helper()
	pcm := goldenSignal(8000) // 100 frames × 80 samples
	enc := NewEncoder(Config{Variant: variant, EnableVAD: false})

	var out []byte
	buf := make([]byte, 10)
	for f := 0; f < len(pcm)/80; f++ {
		n, ft, err := enc.Encode(buf, pcm[f*80:(f+1)*80])
		if err != nil {
			t.Fatalf("frame %d encode error: %v", f, err)
		}
		if ft != FrameSpeech || n != 10 {
			t.Fatalf("frame %d: expected 10-byte speech frame, got n=%d ft=%v", f, n, ft)
		}
		out = append(out, buf[:n]...)
	}
	return out
}

// TestGoldenEncoderG729A verifies that the G.729A encoder produces bit-exact
// output relative to a committed golden file. Any change to the encoder
// pipeline (LP analysis, LSP quantization, pitch search, codebook search, gain
// quantization, bitstream packing) will cause this test to fail, signalling a
// potential regression.
//
// To regenerate the golden file after an intentional encoder change:
//
//	go test -run TestGoldenEncoderG729A -update-golden .
func TestGoldenEncoderG729A(t *testing.T) {
	golden := goldenPath("g729a_encoder.bit")

	got := encodeGoldenSignal(t, VariantG729A)

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden file written: %s (%d bytes, %d frames)", golden, len(got), len(got)/10)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Skipf("golden file not found at %s — run with -update-golden to create it: %v", golden, err)
	}

	if !bytes.Equal(got, want) {
		// Report first differing byte for quick diagnosis
		for i := range want {
			if i >= len(got) || got[i] != want[i] {
				t.Errorf("encoder output differs at byte %d (frame %d, offset %d): got 0x%02x, want 0x%02x",
					i, i/10, i%10, got[i], want[i])
				break
			}
		}
		t.Fatalf("G.729A encoder regression: output differs from %s (%d bytes got vs %d want)",
			golden, len(got), len(want))
	}

	t.Logf("G.729A golden file match: %s (%d frames)", golden, len(got)/10)
}

// TestGoldenEncoderFull verifies bit-exact output from the G.729 Full encoder.
//
// To regenerate:
//
//	go test -run TestGoldenEncoderFull -update-golden .
func TestGoldenEncoderFull(t *testing.T) {
	golden := goldenPath("g729_full_encoder.bit")

	got := encodeGoldenSignal(t, VariantG729)

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden file written: %s (%d bytes, %d frames)", golden, len(got), len(got)/10)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Skipf("golden file not found at %s — run with -update-golden to create it: %v", golden, err)
	}

	if !bytes.Equal(got, want) {
		for i := range want {
			if i >= len(got) || got[i] != want[i] {
				t.Errorf("encoder output differs at byte %d (frame %d, offset %d): got 0x%02x, want 0x%02x",
					i, i/10, i%10, got[i], want[i])
				break
			}
		}
		t.Fatalf("G.729 Full encoder regression: output differs from %s (%d bytes got vs %d want)",
			golden, len(got), len(want))
	}

	t.Logf("G.729 Full golden file match: %s (%d frames)", golden, len(got)/10)
}

// TestGoldenDecoderG729A verifies that decoding the G.729A golden bitstream
// produces the expected PCM output. This catches decoder regressions
// independently of the encoder.
//
// To regenerate:
//
//	go test -run TestGoldenDecoderG729A -update-golden .
func TestGoldenDecoderG729A(t *testing.T) {
	goldenBit := goldenPath("g729a_encoder.bit")
	goldenPCM := goldenPath("g729a_decoder.pcm")

	// Load encoded bitstream (skip if golden hasn't been generated yet)
	bitstream, err := os.ReadFile(goldenBit)
	if err != nil {
		t.Skipf("bitstream golden not found (%v) — run TestGoldenEncoderG729A -update-golden first", err)
	}

	dec := NewDecoder()
	numFrames := len(bitstream) / 10
	got := make([]byte, numFrames*160) // 80 int16 per frame × 2 bytes each

	frame := make([]byte, 10)
	dst := make([]int16, 80)
	for f := range numFrames {
		copy(frame, bitstream[f*10:(f+1)*10])
		if err := dec.Decode(dst, frame); err != nil {
			t.Fatalf("frame %d decode error: %v", f, err)
		}
		for j, s := range dst {
			got[f*160+j*2] = byte(s)
			got[f*160+j*2+1] = byte(s >> 8)
		}
	}

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenPCM), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(goldenPCM, got, 0o644); err != nil {
			t.Fatalf("write golden PCM: %v", err)
		}
		t.Logf("golden PCM written: %s (%d samples)", goldenPCM, len(got)/2)
		return
	}

	want, err := os.ReadFile(goldenPCM)
	if err != nil {
		t.Skipf("PCM golden not found at %s — run with -update-golden to create it: %v", goldenPCM, err)
	}

	if !bytes.Equal(got, want) {
		for i := range want {
			if i >= len(got) || got[i] != want[i] {
				t.Errorf("decoder output differs at byte %d (sample %d): got 0x%02x, want 0x%02x",
					i, i/2, got[i], want[i])
				break
			}
		}
		t.Fatalf("G.729A decoder regression: PCM output differs from %s", goldenPCM)
	}

	t.Logf("G.729A decoder golden match: %s (%d samples)", goldenPCM, len(got)/2)
}
