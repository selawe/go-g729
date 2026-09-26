package pitch

import (
	"math"
	"math/bits"
	"math/rand"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/dsp"
	"github.com/selawe/go-g729/internal/params"
)

// Helper to generate a sinusoidal tone
func generateTone(freqHz float64, sampleRate int, numSamples int) []float32 {
	sig := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		sig[i] = float32(math.Sin(2.0 * math.Pi * freqHz * float64(i) / float64(sampleRate)) * 10000.0)
	}
	return sig
}

// TestOpenLoopPitchTone300Hz tests open-loop pitch estimation on a 300 Hz tone at 8 kHz:
// Period = 8000 / 300 = 26.67 samples -> expected lag T in [25, 29].
func TestOpenLoopPitchTone300Hz(t *testing.T) {
	totalLen := params.PIT_MAX + params.L_FRAME // 143 + 80 = 223
	tone := generateTone(300.0, 8000, totalLen)

	tOp := OpenLoopPitch(tone)
	diff := math.Abs(float64(tOp - 27))
	if diff > 2 {
		t.Errorf("expected 300 Hz pitch lag ~ 27, got %d", tOp)
	}
}

// TestOpenLoopPitchLowTone tests open-loop pitch estimation on a 100 Hz tone:
// Period = 8000 / 100 = 80 samples -> expected lag T in [78, 82].
func TestOpenLoopPitchLowTone(t *testing.T) {
	totalLen := params.PIT_MAX + params.L_FRAME
	tone := generateTone(100.0, 8000, totalLen)

	tOp := OpenLoopPitch(tone)
	diff := math.Abs(float64(tOp - 80))
	if diff > 2 {
		t.Errorf("expected 100 Hz pitch lag ~ 80, got %d", tOp)
	}
}

// TestParityBit verifies odd parity for all 256 possible 8-bit pitch indices.
func TestParityBit(t *testing.T) {
	for p1 := 0; p1 < 256; p1++ {
		p1Byte := uint8(p1)
		p0 := ParityBit(p1Byte)

		// Check parity verification function
		if !CheckParity(p1Byte, p0) {
			t.Errorf("CheckParity failed for p1=%d, p0=%d", p1, p0)
		}

		// Verify odd parity of (p1>>2) + p0
		msb6 := p1Byte >> 2
		popcount := bits.OnesCount8(msb6) + int(p0)
		if popcount%2 != 1 {
			t.Errorf("parity is not odd for p1=%d: popcount=%d", p1, popcount)
		}

		// Flipping parity bit must fail CheckParity
		if CheckParity(p1Byte, p0^1) {
			t.Errorf("CheckParity passed with flipped parity bit for p1=%d", p1)
		}
	}
}

// TestPitchEncodeDecodeRoundTrip verifies round-trip pitch encoding and decoding
// across all valid lags and fractions for both subframes.
func TestPitchEncodeDecodeRoundTrip(t *testing.T) {
	// Subframe 1: lags 19..143
	for t0 := 20; t0 <= 143; t0++ {
		fracs := []int{0}
		if t0 <= 84 {
			fracs = []int{-1, 0, 1}
		}

		for _, frac := range fracs {
			var t0Min, t0Max int
			index := EncodePitch(t0, frac, 0, &t0Min, &t0Max)

			if index < 0 || index > 255 {
				t.Fatalf("subframe 1 index out of 8-bit range: %d (t0=%d, frac=%d)", index, t0, frac)
			}

			var decMin, decMax int
			decT0, decFrac := DecodePitch(index, 0, &decMin, &decMax)

			if decT0 != t0 || decFrac != frac {
				t.Fatalf("subframe 1 round-trip mismatch: orig=(%d, %d), dec=(%d, %d)", t0, frac, decT0, decFrac)
			}
			if decMin != t0Min || decMax != t0Max {
				t.Fatalf("subframe 1 range mismatch: enc=(%d, %d), dec=(%d, %d)", t0Min, t0Max, decMin, decMax)
			}

			// Subframe 2: lags within [t0Min, t0Max]
			for t0_2 := t0Min; t0_2 <= t0Max; t0_2++ {
				for _, frac2 := range []int{-1, 0, 1} {
					idx2 := EncodePitch(t0_2, frac2, 1, &t0Min, &t0Max)
					if idx2 < 0 || idx2 > 31 {
						t.Fatalf("subframe 2 index out of 5-bit range: %d (t0=%d, frac=%d, min=%d)", idx2, t0_2, frac2, t0Min)
					}

					decT0_2, decFrac2 := DecodePitch(idx2, 1, &decMin, &decMax)
					if decT0_2 != t0_2 || decFrac2 != frac2 {
						t.Fatalf("subframe 2 round-trip mismatch: orig=(%d, %d), dec=(%d, %d)", t0_2, frac2, decT0_2, decFrac2)
					}
				}
			}
		}
	}
}

// TestSincInterpolationAccuracy verifies sinc interpolation on an integer lag:
// When frac=0, the interpolated waveform matches the original signal closely.
func TestSincInterpolationAccuracy(t *testing.T) {
	exc := make([]float32, params.EXC_BUF_LEN)
	const offset = params.L_PAST_EXC // 154
	const t0 = 60

	// Sine wave in excitation history
	for i := 0; i < params.EXC_BUF_LEN; i++ {
		exc[i] = float32(math.Sin(2.0 * math.Pi * 440.0 * float64(i) / 8000.0) * 1000.0)
	}

	InterpExcitation(exc, offset, t0, 0, params.L_SUBFR)

	// Compare with exc[offset - t0 + j]
	for j := 0; j < params.L_SUBFR; j++ {
		orig := exc[offset-t0+j]
		interp := exc[offset+j]
		diff := math.Abs(float64(orig - interp))
		// Normalized error < 1%
		if diff/1000.0 > 0.02 {
			t.Errorf("sample %d sinc error too high: orig=%f, interp=%f, diff=%f", j, orig, interp, diff)
		}
	}
}

// TestClosedLoopPitch verifies that closed-loop pitch finds a known synthetic peak.
func TestClosedLoopPitch(t *testing.T) {
	exc := make([]float32, params.EXC_BUF_LEN)
	const offset = params.L_PAST_EXC // 154

	// Place a periodic impulse train with period T=50
	const trueLag = 50
	for i := offset - 100; i < offset; i += trueLag {
		exc[i] = 1000.0
	}

	xn := make([]float32, params.L_SUBFR)
	h := make([]float32, params.L_SUBFR)
	h[0] = 1.0 // Identity impulse response

	// Target xn is the filtered excitation at lag 50
	for j := 0; j < params.L_SUBFR; j++ {
		xn[j] = exc[offset-trueLag+j]
	}

	t0, frac := ClosedLoopPitch(exc, offset, xn, h, 40, 60, 0)

	if t0 != trueLag {
		t.Errorf("expected closed-loop lag %d, got %d (frac=%d)", trueLag, t0, frac)
	}
	if frac != 0 {
		t.Errorf("expected frac 0 for exact integer lag, got %d", frac)
	}
}

// TestPitchGain verifies gain calculation and clamping to [0, 1.2].
func TestPitchGain(t *testing.T) {
	xn := make([]float32, params.L_SUBFR)
	y1 := make([]float32, params.L_SUBFR)

	for i := range y1 {
		y1[i] = 100.0
	}

	// 1. Exact gain 0.75
	for i := range xn {
		xn[i] = 75.0
	}
	g, gCoeff := PitchGain(xn, y1)
	if math.Abs(float64(g-0.75)) > 0.01 {
		t.Errorf("expected gain 0.75, got %f", g)
	}
	if gCoeff[0] <= 0 {
		t.Errorf("expected positive energy gCoeff[0], got %f", gCoeff[0])
	}

	// 2. High gain -> clamped to 1.2
	for i := range xn {
		xn[i] = 200.0
	}
	g, _ = PitchGain(xn, y1)
	if g != params.GP_MAX {
		t.Errorf("expected gain clamped to %f, got %f", params.GP_MAX, g)
	}

	// 3. Negative gain -> clamped to 0.0
	for i := range xn {
		xn[i] = -50.0
	}
	g, _ = PitchGain(xn, y1)
	if g != params.GP_MIN {
		t.Errorf("expected gain clamped to %f, got %f", params.GP_MIN, g)
	}
}

// TestPitchConcurrency verifies thread safety across goroutines.
func TestPitchConcurrency(t *testing.T) {
	const goroutines = 16
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))

			totalLen := params.PIT_MAX + params.L_FRAME
			wsp := make([]float32, totalLen)
			exc := make([]float32, params.EXC_BUF_LEN)
			xn := make([]float32, params.L_SUBFR)
			h := make([]float32, params.L_SUBFR)
			h[0] = 1.0

			for it := 0; it < iterations; it++ {
				for i := range wsp {
					wsp[i] = (rng.Float32() - 0.5) * 2000.0
				}
				for i := range exc {
					exc[i] = (rng.Float32() - 0.5) * 2000.0
				}
				for i := range xn {
					xn[i] = rng.Float32() * 500.0
				}

				tOp := OpenLoopPitch(wsp)
				t0Min := tOp - 3
				if t0Min < params.PIT_MIN {
					t0Min = params.PIT_MIN
				}
				t0Max := t0Min + 6
				if t0Max > params.PIT_MAX {
					t0Max = params.PIT_MAX
					t0Min = t0Max - 6
				}

				t0, frac := ClosedLoopPitch(exc, params.L_PAST_EXC, xn, h, t0Min, t0Max, 0)
				idx := EncodePitch(t0, frac, 0, &t0Min, &t0Max)
				p0 := ParityBit(uint8(idx))
				_ = CheckParity(uint8(idx), p0)
				_, _ = DecodePitch(idx, 0, &t0Min, &t0Max)

				var y1 [params.L_SUBFR]float32
				dsp.Convolution(y1[:], h, exc[params.L_PAST_EXC:params.L_PAST_EXC+params.L_SUBFR])
				_, _ = PitchGain(xn, y1[:])
			}
		}(int64(g + 1))
	}

	wg.Wait()
}

// Benchmarks for Phase 4.

func BenchmarkOpenLoopPitch(b *testing.B) {
	totalLen := params.PIT_MAX + params.L_FRAME
	tone := generateTone(250.0, 8000, totalLen)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = OpenLoopPitch(tone)
	}
}

func BenchmarkClosedLoopPitch(b *testing.B) {
	exc := make([]float32, params.EXC_BUF_LEN)
	for i := range exc {
		exc[i] = float32(i)
	}
	xn := make([]float32, params.L_SUBFR)
	h := make([]float32, params.L_SUBFR)
	h[0] = 1.0
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = ClosedLoopPitch(exc, params.L_PAST_EXC, xn, h, 20, 26, 0)
	}
}

func BenchmarkInterpExcitation(b *testing.B) {
	exc := make([]float32, params.EXC_BUF_LEN)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		InterpExcitation(exc, params.L_PAST_EXC, 45, 1, params.L_SUBFR)
	}
}

func BenchmarkParityBit(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = ParityBit(uint8(i))
	}
}
