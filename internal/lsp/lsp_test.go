package lsp

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/dsp"
	"github.com/selawe/go-g729/internal/params"
)

// Helper to generate a guaranteed stable 10th-order LPC filter using Autocorr + Levinson.
func generateStableLPC(rng *rand.Rand) [params.M + 1]float32 {
	speech := make([]float32, params.L_WINDOW)
	for i := range speech {
		speech[i] = (rng.Float32() - 0.5) * 5000.0
	}
	var r [params.M + 1]float32
	dsp.Autocorr(r[:], speech, nil, params.M)
	a, _, err := dsp.Levinson(r[:], params.M)
	if err != nil {
		// Fallback to simple bandwidth expanded filter
		a[0] = 1.0
		a[1] = -0.9
		for i := 2; i <= params.M; i++ {
			a[i] = 0.0
		}
	}
	return a
}

// TestLPC2LSPRootCount verifies that LPC2LSP produces 10 strictly ordered roots
// in the cosine domain for a stable LPC filter.
func TestLPC2LSPRootCount(t *testing.T) {
	rng := rand.New(rand.NewSource(1001))

	for iter := 0; iter < 20; iter++ {
		a := generateStableLPC(rng)
		lsp, ok := LPC2LSP(&a, nil)
		if !ok {
			t.Fatalf("iter %d: LPC2LSP failed to find 10 roots for stable filter", iter)
		}

		// In cosine domain, roots must be strictly descending: lsp[0] > lsp[1] > ... > lsp[9]
		// and all in (-1, 1).
		for i := 0; i < params.M; i++ {
			if lsp[i] <= -1.0 || lsp[i] >= 1.0 {
				t.Errorf("iter %d: lsp[%d] = %f outside (-1, 1)", iter, i, lsp[i])
			}
			if i > 0 && lsp[i] >= lsp[i-1] {
				t.Errorf("iter %d: lsp not strictly descending at %d: %f >= %f", iter, i, lsp[i], lsp[i-1])
			}
		}

		// In LSF frequency domain (radians), roots must be strictly ascending in (0, pi)
		lsf := LSP2LSF(lsp)
		for i := 0; i < params.M; i++ {
			if lsf[i] <= 0 || lsf[i] >= math.Pi {
				t.Errorf("iter %d: lsf[%d] = %f outside (0, pi)", iter, i, lsf[i])
			}
			if i > 0 && lsf[i] <= lsf[i-1] {
				t.Errorf("iter %d: lsf not strictly ascending at %d: %f <= %f", iter, i, lsf[i], lsf[i-1])
			}
		}
	}
}

// TestLPCLSPRoundTrip verifies that A(z) -> LSP -> A(z) reconstructs the original
// LPC prediction coefficients with high precision (error < 1e-4).
func TestLPCLSPRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(2023))

	for iter := 0; iter < 50; iter++ {
		aOrig := generateStableLPC(rng)
		lsp, ok := LPC2LSP(&aOrig, nil)
		if !ok {
			continue
		}

		aBack := LSP2LPC(lsp)

		// a[0] must be exactly 1.0
		if math.Abs(float64(aBack[0]-1.0)) > 1e-6 {
			t.Errorf("iter %d: expected aBack[0] = 1.0, got %f", iter, aBack[0])
		}

		for i := 1; i <= params.M; i++ {
			diff := math.Abs(float64(aOrig[i] - aBack[i]))
			if diff > 1e-4 {
				t.Errorf("iter %d: a[%d] mismatch: orig=%f, recon=%f, diff=%e", iter, i, aOrig[i], aBack[i], diff)
			}
		}
	}
}

// TestLSPStabilize verifies that non-monotonic or tightly-spaced LSFs are stabilized
// to satisfy minimum separation gap d_min = 0.005 radians.
func TestLSPStabilize(t *testing.T) {
	// Create an un-ordered and tightly clustered LSF vector
	lsf := [params.M]float32{0.20, 0.201, 0.50, 0.49, 1.10, 1.102, 1.70, 2.00, 2.001, 3.14}
	minGap := float32(0.005)

	StabilizeLSF(&lsf, minGap)

	// Check lower and upper bounds
	if lsf[0] < LLimit {
		t.Errorf("lsf[0] = %f < LLimit (%f)", lsf[0], LLimit)
	}
	if lsf[params.M-1] > MLimit {
		t.Errorf("lsf[9] = %f > MLimit (%f)", lsf[params.M-1], MLimit)
	}

	// Check minimum distance between adjacent frequencies
	for i := 1; i < params.M; i++ {
		gap := lsf[i] - lsf[i-1]
		if gap < minGap-1e-6 {
			t.Errorf("gap between lsf[%d] and lsf[%d] = %f < minGap (%f)", i-1, i, gap, minGap)
		}
	}
}

// TestLSPQuantizeDequantize verifies that QuantizeLSP and DequantizeLSP
// produce identical reconstructed LSPs and maintain synchronized MA memories.
func TestLSPQuantizeDequantize(t *testing.T) {
	rng := rand.New(rand.NewSource(777))

	var encMA [params.MA_NP][params.M]float32
	var decMA [params.MA_NP][params.M]float32
	InitLSPMA(&encMA)
	InitLSPMA(&decMA)

	numFrames := 30
	for frame := 0; frame < numFrames; frame++ {
		a := generateStableLPC(rng)
		lsp, ok := LPC2LSP(&a, nil)
		if !ok {
			continue
		}

		l0, l1, l2, l3, lspQ := QuantizeLSP(lsp, &encMA)
		lspDec := DequantizeLSP(l0, l1, l2, l3, &decMA)

		// 1. Encoder quantized LSP and Decoder reconstructed LSP must be identical
		for i := 0; i < params.M; i++ {
			if lspQ[i] != lspDec[i] {
				t.Fatalf("frame %d: lspQ[%d]=%f != lspDec[%d]=%f", frame, i, lspQ[i], i, lspDec[i])
			}
		}

		// 2. Encoder and Decoder MA memories must match exactly
		for k := 0; k < params.MA_NP; k++ {
			for j := 0; j < params.M; j++ {
				if encMA[k][j] != decMA[k][j] {
					t.Fatalf("frame %d: MA memory mismatch at [%d][%d]: enc=%f, dec=%f", frame, k, j, encMA[k][j], decMA[k][j])
				}
			}
		}

		// 3. Quantization distortion must be small (MSE in cosine domain < 0.01)
		var mse float64
		for i := 0; i < params.M; i++ {
			diff := float64(lsp[i] - lspQ[i])
			mse += diff * diff
		}
		mse /= float64(params.M)
		if mse > 0.01 {
			t.Errorf("frame %d: quantization MSE too high: %f", frame, mse)
		}

		// 4. Verify valid code indices
		if l0 < 0 || l0 > 1 {
			t.Errorf("frame %d: invalid l0: %d", frame, l0)
		}
		if l1 < 0 || l1 >= params.NC0 {
			t.Errorf("frame %d: invalid l1: %d", frame, l1)
		}
		if l2 < 0 || l2 >= params.NC1 {
			t.Errorf("frame %d: invalid l2: %d", frame, l2)
		}
		if l3 < 0 || l3 >= params.NC1 {
			t.Errorf("frame %d: invalid l3: %d", frame, l3)
		}
	}
}

// TestLSPInterpolate verifies subframe 1 and 2 interpolation behavior.
func TestLSPInterpolate(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	aPrev := generateStableLPC(rng)
	aCurr := generateStableLPC(rng)

	lspPrev, _ := LPC2LSP(&aPrev, nil)
	lspCurr, _ := LPC2LSP(&aCurr, nil)

	lspSF1, lspSF2 := InterpolateLSPDirect(lspPrev, lspCurr)

	for i := 0; i < params.M; i++ {
		expectedSF1 := 0.5*lspPrev[i] + 0.5*lspCurr[i]
		if math.Abs(float64(lspSF1[i]-expectedSF1)) > 1e-6 {
			t.Errorf("sf1 mismatch at %d: got %f, expected %f", i, lspSF1[i], expectedSF1)
		}
		if lspSF2[i] != lspCurr[i] {
			t.Errorf("sf2 mismatch at %d: got %f, expected %f", i, lspSF2[i], lspCurr[i])
		}
	}
}

// TestLSPConcurrency ensures thread safety across goroutines.
func TestLSPConcurrency(t *testing.T) {
	const goroutines = 16
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))

			var encMA [params.MA_NP][params.M]float32
			var decMA [params.MA_NP][params.M]float32
			InitLSPMA(&encMA)
			InitLSPMA(&decMA)

			for it := 0; it < iterations; it++ {
				a := generateStableLPC(rng)
				lsp, ok := LPC2LSP(&a, nil)
				if !ok {
					continue
				}
				l0, l1, l2, l3, lspQ := QuantizeLSP(lsp, &encMA)
				lspDec := DequantizeLSP(l0, l1, l2, l3, &decMA)
				_ = LSP2LPC(lspDec)
				_ = lspQ
			}
		}(int64(g + 1))
	}

	wg.Wait()
}

// Benchmarks for Phase 3.

func BenchmarkLPC2LSP(b *testing.B) {
	a := [11]float32{1.0, -1.25, 0.78, -0.42, 0.25, -0.15, 0.10, -0.06, 0.04, -0.02, 0.01}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = LPC2LSP(&a, nil)
	}
}

func BenchmarkLSP2LPC(b *testing.B) {
	lsp := [10]float32{0.95, 0.85, 0.70, 0.50, 0.25, 0.0, -0.25, -0.50, -0.70, -0.85}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = LSP2LPC(lsp)
	}
}

func BenchmarkQuantizeLSP(b *testing.B) {
	lsp := [10]float32{0.95, 0.85, 0.70, 0.50, 0.25, 0.0, -0.25, -0.50, -0.70, -0.85}
	var ma [params.MA_NP][params.M]float32
	InitLSPMA(&ma)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _, _, _ = QuantizeLSP(lsp, &ma)
	}
}

func BenchmarkDequantizeLSP(b *testing.B) {
	var ma [params.MA_NP][params.M]float32
	InitLSPMA(&ma)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = DequantizeLSP(1, 45, 12, 23, &ma)
	}
}
