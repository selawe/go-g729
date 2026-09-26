package dsp

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// TestLevinsonAR1 verifies Levinson-Durbin on an AR(1) process:
// r[k] = rho^k -> a[1] = -rho, a[2..10] = 0, error = 1 - rho^2.
func TestLevinsonAR1(t *testing.T) {
	testCases := []float32{0.9, 0.5, -0.7, 0.25}

	for _, rho := range testCases {
		r := [11]float32{}
		for k := range r {
			r[k] = float32(math.Pow(float64(rho), float64(k)))
		}

		a, rc, err := Levinson(r[:], params.M)
		if err != nil {
			t.Fatalf("Levinson returned unexpected error for rho=%f: %v", rho, err)
		}

		// a[0] must be 1.0
		if math.Abs(float64(a[0]-1.0)) > 1e-6 {
			t.Errorf("expected a[0] = 1.0, got %f", a[0])
		}

		// a[1] must equal -rho
		expectedA1 := -rho
		if math.Abs(float64(a[1]-expectedA1)) > 1e-5 {
			t.Errorf("rho=%f: expected a[1] = %f, got %f", rho, expectedA1, a[1])
		}

		// a[2..10] must be near 0
		for k := 2; k <= params.M; k++ {
			if math.Abs(float64(a[k])) > 1e-5 {
				t.Errorf("rho=%f: expected a[%d] ~ 0, got %f", rho, k, a[k])
			}
		}

		// rc[0] must equal -rho, rc[1..9] must be near 0
		if math.Abs(float64(rc[0]-expectedA1)) > 1e-5 {
			t.Errorf("rho=%f: expected rc[0] = %f, got %f", rho, expectedA1, rc[0])
		}
		for k := 1; k < params.M; k++ {
			if math.Abs(float64(rc[k])) > 1e-5 {
				t.Errorf("rho=%f: expected rc[%d] ~ 0, got %f", rho, k, rc[k])
			}
		}
	}
}

// TestLevinsonAR2 verifies Levinson-Durbin on a 2nd order autoregressive process:
// y[n] = 0.6 y[n-1] - 0.25 y[n-2] + e[n]
// A(z) = 1 - 0.6 z^-1 + 0.25 z^-2 -> a[1] = -0.6, a[2] = 0.25, a[3..10] = 0.
func TestLevinsonAR2(t *testing.T) {
	a1True := 0.6
	a2True := -0.25

	// Yule-Walker for AR(2):
	// r[0] = 1.0
	// r[1] = a1 / (1 - a2)
	// r[2] = a1*r[1] + a2*r[0]
	// r[k] = a1*r[k-1] + a2*r[k-2]
	r := [11]float32{}
	r[0] = 1.0
	r[1] = float32(a1True / (1.0 - a2True))
	r[2] = float32(a1True*float64(r[1]) + a2True*float64(r[0]))
	for k := 3; k <= params.M; k++ {
		r[k] = float32(a1True*float64(r[k-1]) + a2True*float64(r[k-2]))
	}

	a, _, err := Levinson(r[:], params.M)
	if err != nil {
		t.Fatalf("Levinson error on AR(2): %v", err)
	}

	expectedA1 := float32(-a1True)
	expectedA2 := float32(-a2True)

	if math.Abs(float64(a[1]-expectedA1)) > 1e-5 {
		t.Errorf("expected a[1] = %f, got %f", expectedA1, a[1])
	}
	if math.Abs(float64(a[2]-expectedA2)) > 1e-5 {
		t.Errorf("expected a[2] = %f, got %f", expectedA2, a[2])
	}
	for k := 3; k <= params.M; k++ {
		if math.Abs(float64(a[k])) > 1e-5 {
			t.Errorf("expected a[%d] ~ 0, got %f", k, a[k])
		}
	}
}

// TestLevinsonStability checks edge cases and error handling.
func TestLevinsonStability(t *testing.T) {
	// 1. Non-positive r[0]
	rZero := [11]float32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	_, _, err := Levinson(rZero[:], params.M)
	if err != ErrSingularMatrix {
		t.Errorf("expected ErrSingularMatrix for r[0]=0, got %v", err)
	}

	rNeg := [11]float32{-5.0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	_, _, err = Levinson(rNeg[:], params.M)
	if err != ErrSingularMatrix {
		t.Errorf("expected ErrSingularMatrix for r[0]<0, got %v", err)
	}

	// 2. NaN in r[0]
	rNaN := [11]float32{float32(math.NaN()), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	_, _, err = Levinson(rNaN[:], params.M)
	if err != ErrSingularMatrix {
		t.Errorf("expected ErrSingularMatrix for NaN, got %v", err)
	}

	// 3. Short slice
	rShort := [5]float32{1, 0.5, 0.2, 0.1, 0.05}
	_, _, err = Levinson(rShort[:], params.M)
	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput for short slice, got %v", err)
	}

	// 4. Invalid order m
	_, _, err = Levinson(rZero[:], 0)
	if err != ErrInvalidInput {
		t.Errorf("expected ErrInvalidInput for m=0, got %v", err)
	}

	// 5. Unstable reflection coefficient (|r[1]| >= r[0])
	rUnstable := [11]float32{1.0, 1.5, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	_, _, err = Levinson(rUnstable[:], params.M)
	if err != ErrUnstableFilter {
		t.Errorf("expected ErrUnstableFilter for |r[1]| >= r[0], got %v", err)
	}
}

// TestHighPassFilterDC verifies that DC (constant signal) is attenuated by ~40 dB
// as designed by ITU-T (SPPACK efi command -40 dB att).
func TestHighPassFilterDC(t *testing.T) {
	var state HPFState
	const inputDC = 1000.0
	dc := make([]float32, 800) // 100 ms of constant 1000.0 DC offset
	for i := range dc {
		dc[i] = inputDC
	}

	HighPassFilter(dc, &state)

	// Theoretical settled DC value: X_dc * sum(b) / (1 - a1 - a2) ≈ 10.0075 (-40 dB attenuation)
	expectedDC := float32(inputDC * (tables.HPF140_B[0] + tables.HPF140_B[1] + tables.HPF140_B[2]) / (1.0 - tables.HPF140_A[1] - tables.HPF140_A[2]))
	lastVal := dc[len(dc)-1]

	if math.Abs(float64(lastVal-expectedDC)) > 0.1 {
		t.Errorf("expected settled DC output ~ %f, got %f", expectedDC, lastVal)
	}

	// Verify attenuation is at least 39.9 dB
	attenuationDB := -20.0 * math.Log10(float64(math.Abs(float64(lastVal))/inputDC))
	if attenuationDB < 39.5 {
		t.Errorf("expected attenuation >= 39.5 dB, got %f dB", attenuationDB)
	}
}

// TestHighPassFilterPassband verifies that audio frequencies well above 140 Hz
// pass with near unit gain (0 dB).
func TestHighPassFilterPassband(t *testing.T) {
	var state HPFState
	// 1000 Hz tone at 8000 Hz sampling rate
	n := 800
	sig := make([]float32, n)
	for i := range sig {
		sig[i] = 10000.0 * float32(math.Sin(2.0*math.Pi*1000.0*float64(i)/8000.0))
	}

	HighPassFilter(sig, &state)

	// In steady state (last 200 samples), peak amplitude should be near 10000.0 (+/- 5%)
	var maxAmp float32
	for i := 600; i < n; i++ {
		amp := float32(math.Abs(float64(sig[i])))
		if amp > maxAmp {
			maxAmp = amp
		}
	}

	ratio := maxAmp / 10000.0
	if ratio < 0.95 || ratio > 1.05 {
		t.Errorf("expected passband amplitude ratio ~ 1.0, got %f", ratio)
	}
}

// TestPostProcessFilter verifies that 100 Hz post-filter attenuates DC by ~40 dB.
func TestPostProcessFilter(t *testing.T) {
	var state HPFState
	const inputDC = 2000.0
	dc := make([]float32, 800)
	for i := range dc {
		dc[i] = inputDC
	}

	PostProcessFilter(dc, &state)

	expectedDC := float32(inputDC * (tables.HPF100_B[0] + tables.HPF100_B[1] + tables.HPF100_B[2]) / (1.0 - tables.HPF100_A[1] - tables.HPF100_A[2]))
	lastVal := dc[len(dc)-1]

	if math.Abs(float64(lastVal-expectedDC)) > 0.1 {
		t.Errorf("expected post-filter DC output ~ %f, got %f", expectedDC, lastVal)
	}

	attenuationDB := -20.0 * math.Log10(float64(math.Abs(float64(lastVal))/inputDC))
	if attenuationDB < 39.5 {
		t.Errorf("expected post-filter attenuation >= 39.5 dB, got %f dB", attenuationDB)
	}
}

// TestSynthesisResidueInverse verifies that Residue (analysis A(z)) and SynthesisFilter (1/A(z))
// are exact mathematical inverses of each other.
func TestSynthesisResidueInverse(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	// Stable LP coefficients (order 10)
	a := [11]float32{
		1.0,
		-0.85, 0.42, -0.21, 0.15, -0.08,
		0.05, -0.03, 0.02, -0.01, 0.005,
	}

	// 1. Single subframe (40 samples), zero memory
	orig := make([]float32, params.L_SUBFR)
	for i := range orig {
		orig[i] = (rng.Float32() - 0.5) * 2000.0
	}

	residual := make([]float32, params.L_SUBFR)
	reconstructed := make([]float32, params.L_SUBFR)

	Residue(residual, orig, a[:], nil, false)
	SynthesisFilter(reconstructed, residual, a[:], nil, false)

	for i := 0; i < params.L_SUBFR; i++ {
		diff := math.Abs(float64(orig[i] - reconstructed[i]))
		if diff > 5e-4 {
			t.Fatalf("subframe sample %d mismatch: orig=%f, recon=%f, diff=%e", i, orig[i], reconstructed[i], diff)
		}
	}

	// 2. Chained multi-subframe test with memory updating
	numSubfr := 10
	var resMem [params.M]float32
	var synMem [params.M]float32


	for sf := 0; sf < numSubfr; sf++ {
		sfOrig := make([]float32, params.L_SUBFR)
		for i := range sfOrig {
			sfOrig[i] = (rng.Float32() - 0.5) * 4000.0
		}
		sfRes := make([]float32, params.L_SUBFR)
		sfRecon := make([]float32, params.L_SUBFR)

		Residue(sfRes, sfOrig, a[:], resMem[:], true)
		SynthesisFilter(sfRecon, sfRes, a[:], synMem[:], true)

		for i := 0; i < params.L_SUBFR; i++ {
			diff := math.Abs(float64(sfOrig[i] - sfRecon[i]))
			if diff > 1e-3 {
				t.Fatalf("sf %d sample %d mismatch: orig=%f, recon=%f, diff=%e", sf, i, sfOrig[i], sfRecon[i], diff)
			}
		}
	}
}

// TestConvolution verifies linear convolution against an impulse and known manual formula.
func TestConvolution(t *testing.T) {
	// 1. Impulse input delta[n] = {1, 0, 0, ...} -> out[n] = h[n]
	x := make([]float32, params.L_SUBFR)
	x[0] = 1.0

	h := make([]float32, params.L_SUBFR)
	for i := range h {
		h[i] = float32(i + 1)
	}

	out := make([]float32, params.L_SUBFR)
	Convolution(out, h, x)

	for i := 0; i < params.L_SUBFR; i++ {
		if math.Abs(float64(out[i]-h[i])) > 1e-6 {
			t.Errorf("impulse response mismatch at %d: expected %f, got %f", i, h[i], out[i])
		}
	}

	// 2. Direct formula test on random vectors
	rng := rand.New(rand.NewSource(12345))
	for i := range x {
		x[i] = rng.Float32()
		h[i] = rng.Float32()
	}

	Convolution(out, h, x)

	for n := 0; n < params.L_SUBFR; n++ {
		var expected float32
		for i := 0; i <= n; i++ {
			expected += x[i] * h[n-i]
		}
		if math.Abs(float64(out[n]-expected)) > 1e-5 {
			t.Errorf("conv mismatch at n=%d: expected %f, got %f", n, expected, out[n])
		}
	}
}

// TestAutocorr verifies autocorrelation lag-0 dominance and lag-windowing.
func TestAutocorr(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	speech := make([]float32, params.L_WINDOW)
	for i := range speech {
		speech[i] = (rng.Float32() - 0.5) * 1000.0
	}

	rRaw := make([]float32, params.M+1)
	AutocorrRaw(rRaw, speech, nil, params.M)

	// Lag-0 must be >= all other lags
	for k := 1; k <= params.M; k++ {
		if math.Abs(float64(rRaw[k])) > float64(rRaw[0]) {
			t.Errorf("autocorr lag %d (|%f|) > lag 0 (%f)", k, rRaw[k], rRaw[0])
		}
	}

	rLag := make([]float32, params.M+1)
	Autocorr(rLag, speech, nil, params.M)

	// Verify lag window was applied correctly: rLag[k] == rRaw[k] * LagWindow[k]
	for k := 0; k <= params.M; k++ {
		expected := rRaw[k] * tables.LagWindow[k]
		if math.Abs(float64(rLag[k]-expected)) > 1e-4 {
			t.Errorf("lag window mismatch at k=%d: expected %f, got %f", k, expected, rLag[k])
		}
	}
}

// TestDSPConcurrency ensures all DSP routines are safe from data races.
func TestDSPConcurrency(t *testing.T) {
	const goroutines = 16
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))

			var hpfState HPFState
			speech := make([]float32, params.L_WINDOW)
			r := make([]float32, params.M+1)
			out := make([]float32, params.L_SUBFR)
			x := make([]float32, params.L_SUBFR)
			h := make([]float32, params.L_SUBFR)
			var mem [params.M]float32

			for it := 0; it < iterations; it++ {
				for i := range speech {
					speech[i] = (rng.Float32() - 0.5) * 500.0
				}
				for i := range x {
					x[i] = rng.Float32()
					h[i] = rng.Float32()
				}

				HighPassFilter(speech[:params.L_FRAME], &hpfState)
				Autocorr(r, speech, nil, params.M)
				a, _, err := Levinson(r, params.M)
				if err == nil {
					Residue(out, x, a[:], mem[:], true)
					SynthesisFilter(x, out, a[:], mem[:], true)
				}
				Convolution(out, h, x)
			}
		}(int64(g + 1))
	}

	wg.Wait()
}

// Benchmarks for performance and zero-allocation verification.

func BenchmarkHighPassFilter(b *testing.B) {
	sig := make([]float32, params.L_FRAME)
	for i := range sig {
		sig[i] = 1000.0
	}
	var state HPFState
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		HighPassFilter(sig, &state)
	}
}

func BenchmarkAutocorr(b *testing.B) {
	speech := make([]float32, params.L_WINDOW)
	for i := range speech {
		speech[i] = float32(i)
	}
	var r [params.M + 1]float32
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		Autocorr(r[:], speech, nil, params.M)
	}
}

func BenchmarkLevinson(b *testing.B) {
	r := [11]float32{1.0, 0.9, 0.81, 0.729, 0.656, 0.59, 0.53, 0.47, 0.42, 0.38, 0.34}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = Levinson(r[:], params.M)
	}
}

func BenchmarkSynthesisFilter(b *testing.B) {
	out := make([]float32, params.L_SUBFR)
	x := make([]float32, params.L_SUBFR)
	a := [11]float32{1.0, -0.9, 0.7, -0.5, 0.3, -0.1, 0.05, -0.02, 0.01, -0.005, 0.001}
	var mem [params.M]float32
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		SynthesisFilter(out, x, a[:], mem[:], true)
	}
}

func BenchmarkResidue(b *testing.B) {
	out := make([]float32, params.L_SUBFR)
	x := make([]float32, params.L_SUBFR)
	a := [11]float32{1.0, -0.9, 0.7, -0.5, 0.3, -0.1, 0.05, -0.02, 0.01, -0.005, 0.001}
	var mem [params.M]float32
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		Residue(out, x, a[:], mem[:], true)
	}
}

func BenchmarkConvolution(b *testing.B) {
	out := make([]float32, params.L_SUBFR)
	h := make([]float32, params.L_SUBFR)
	x := make([]float32, params.L_SUBFR)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		Convolution(out, h, x)
	}
}
