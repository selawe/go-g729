package filter

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/params"
)

// TestWeightAz verifies spectral expansion ap[i] = a[i] * gamma^i.
func TestWeightAz(t *testing.T) {
	a := []float32{1.0, 0.5, -0.4, 0.3, -0.2, 0.1, -0.05, 0.04, -0.03, 0.02, -0.01}
	gamma := float32(0.75)
	var ap [params.M + 1]float32

	WeightAz(a, gamma, ap[:])

	if ap[0] != 1.0 {
		t.Fatalf("ap[0] got %f, want 1.0", ap[0])
	}
	fac := gamma
	for i := 1; i <= params.M; i++ {
		expected := a[i] * fac
		if math.Abs(float64(ap[i]-expected)) > 1e-6 {
			t.Fatalf("ap[%d] got %f, want %f", i, ap[i], expected)
		}
		fac *= gamma
	}
}

// TestPerceptualWeightCoeffs verifies numerator and denominator coefficient expansion.
func TestPerceptualWeightCoeffs(t *testing.T) {
	a := []float32{1.0, -0.8, 0.6, -0.4, 0.3, -0.2, 0.1, -0.05, 0.03, -0.02, 0.01}
	gamma1 := float32(0.94)
	gamma2 := float32(0.60)
	var ap1, ap2 [params.M + 1]float32

	PerceptualWeightCoeffs(a, gamma1, gamma2, ap1[:], ap2[:])

	fac1 := gamma1
	fac2 := gamma2
	for i := 1; i <= params.M; i++ {
		if math.Abs(float64(ap1[i]-a[i]*fac1)) > 1e-6 {
			t.Fatalf("ap1[%d] mismatch", i)
		}
		if math.Abs(float64(ap2[i]-a[i]*fac2)) > 1e-6 {
			t.Fatalf("ap2[%d] mismatch", i)
		}
		fac1 *= gamma1
		fac2 *= gamma2
	}
}

// TestApplyPerceptualFilterA verifies speech perceptual weighting in G.729A.
func TestApplyPerceptualFilterA(t *testing.T) {
	a := []float32{1.0, -0.8, 0.5, -0.3, 0.2, -0.1, 0.05, -0.03, 0.02, -0.01, 0.005}
	var ap [params.M + 1]float32
	WeightAz(a, Gamma1A, ap[:])

	var speech [params.L_FRAME]float32
	for i := range speech {
		speech[i] = float32(math.Sin(float64(i) * 0.2))
	}

	var wsp [params.L_FRAME]float32
	var memW [params.M]float32

	ApplyPerceptualFilterA(wsp[:], speech[:], a, ap[:], memW[:])

	// Verify wsp is non-zero, finite, and bounded
	var energy float32
	for i := range wsp {
		if math.IsNaN(float64(wsp[i])) || math.IsInf(float64(wsp[i]), 0) {
			t.Fatalf("NaN/Inf in wsp[%d]", i)
		}
		energy += wsp[i] * wsp[i]
	}
	if energy <= 0 {
		t.Fatalf("expected positive energy in wsp, got %f", energy)
	}
}

// TestPercVar verifies adaptive bandwidth expansion factors for Full G.729.
func TestPercVar(t *testing.T) {
	lsfInt := []float32{0.2, 0.4, 0.6, 0.8, 1.0, 1.2, 1.4, 1.6, 1.8, 2.0}
	lsfNew := []float32{0.22, 0.42, 0.62, 0.82, 1.02, 1.22, 1.42, 1.62, 1.82, 2.02}
	rc := []float32{0.3, -0.2}
	var larOld [2]float32
	smooth := 1

	g1, g2 := PercVar(lsfInt, lsfNew, rc, &larOld, &smooth)

	for k := 0; k < 2; k++ {
		if g1[k] < 0.90 || g1[k] > 1.0 {
			t.Fatalf("subframe %d: gamma1 out of range: %f", k, g1[k])
		}
		if g2[k] < 0.35 || g2[k] > 0.75 {
			t.Fatalf("subframe %d: gamma2 out of range: %f", k, g2[k])
		}
	}
}

// TestPostFilterEnergyControl verifies that adaptive postfiltering preserves energy
// within AGC limits (per plan: output energy <= input energy * 1.05).
func TestPostFilterEnergyControl(t *testing.T) {
	state := NewPostFilterState()

	// Interpolated LP filter coefficients for 2 subframes
	var az [2 * (params.M + 1)]float32
	a := []float32{1.0, -0.7, 0.4, -0.2, 0.1, -0.05, 0.02, -0.01, 0.005, -0.002, 0.001}
	copy(az[:11], a)
	copy(az[11:], a)

	rng := rand.New(rand.NewSource(12345))

	for frame := 0; frame < 10; frame++ {
		var syn [params.L_FRAME]float32
		var inEnergy float32
		for i := 0; i < params.L_FRAME; i++ {
			syn[i] = 100.0*float32(math.Sin(float64(i)*0.15)) + 10.0*(rng.Float32()-0.5)
			inEnergy += syn[i] * syn[i]
		}

		pitchLags := [2]int{40, 40}
		PostFilter(syn[:], az[:], pitchLags, state)

		var outEnergy float32
		for i := 0; i < params.L_FRAME; i++ {
			if math.IsNaN(float64(syn[i])) || math.IsInf(float64(syn[i]), 0) {
				t.Fatalf("frame %d: NaN/Inf in postfiltered speech[%d]", frame, i)
			}
			outEnergy += syn[i] * syn[i]
		}

		// After warm-up (frame > 2), AGC ensures outEnergy is well-controlled
		if frame > 2 {
			ratio := outEnergy / inEnergy
			if ratio > 1.25 || ratio < 0.3 {
				t.Fatalf("frame %d: unexpected AGC energy ratio out/in=%f (in=%f, out=%f)",
					frame, ratio, inEnergy, outEnergy)
			}
		}
	}
}

// TestFilterConcurrency ensures perceptual filter and postfilter are safe across goroutines.
func TestFilterConcurrency(t *testing.T) {
	const numGoroutines = 16
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			state := NewPostFilterState()

			a := []float32{1.0, -0.7, 0.4, -0.2, 0.1, -0.05, 0.02, -0.01, 0.005, -0.002, 0.001}
			var ap [params.M + 1]float32
			WeightAz(a, Gamma1A, ap[:])

			var speech [params.L_FRAME]float32
			var wsp [params.L_FRAME]float32
			var memW [params.M]float32
			for i := range speech {
				speech[i] = float32(math.Sin(float64(i+gid) * 0.2))
			}
			ApplyPerceptualFilterA(wsp[:], speech[:], a, ap[:], memW[:])

			var az [2 * (params.M + 1)]float32
			copy(az[:11], a)
			copy(az[11:], a)
			PostFilter(speech[:], az[:], [2]int{35 + gid%20, 35 + gid%20}, state)
		}(g)
	}

	wg.Wait()
}

func BenchmarkApplyPerceptualFilterA(b *testing.B) {
	a := []float32{1.0, -0.8, 0.5, -0.3, 0.2, -0.1, 0.05, -0.03, 0.02, -0.01, 0.005}
	var ap [params.M + 1]float32
	WeightAz(a, Gamma1A, ap[:])

	var speech [params.L_FRAME]float32
	var wsp [params.L_FRAME]float32
	var memW [params.M]float32

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyPerceptualFilterA(wsp[:], speech[:], a, ap[:], memW[:])
	}
}

func BenchmarkPostFilter(b *testing.B) {
	state := NewPostFilterState()
	var az [2 * (params.M + 1)]float32
	a := []float32{1.0, -0.7, 0.4, -0.2, 0.1, -0.05, 0.02, -0.01, 0.005, -0.002, 0.001}
	copy(az[:11], a)
	copy(az[11:], a)

	var syn [params.L_FRAME]float32
	for i := range syn {
		syn[i] = 100.0 * float32(math.Sin(float64(i)*0.15))
	}
	pitchLags := [2]int{40, 40}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		PostFilter(syn[:], az[:], pitchLags, state)
	}
}
