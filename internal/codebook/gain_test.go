package codebook

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/params"
)

// TestGainQuantizeRoundTrip verifies that QuantizeGain and DequantizeGain produce
// matching pitch gain and codebook gain, and keep MA energy predictor memory synchronized.
func TestGainQuantizeRoundTrip(t *testing.T) {
	var encMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}
	var decMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}

	rng := rand.New(rand.NewSource(42))

	for frame := 0; frame < 20; frame++ {
		// Generate synthetic codevector with 4 pulses
		idx := rng.Intn(8192)
		sign := rng.Intn(16)
		code := BuildCodeVector(idx, sign)

		// Generate synthetic target xn, filtered adaptive y1, filtered innovative y2
		var xn, y1, y2 [40]float32
		for i := 0; i < 40; i++ {
			y1[i] = rng.Float32()*2 - 1
			y2[i] = rng.Float32()*2 - 1
			// xn target is combination with noise
			xn[i] = 0.75*y1[i] + 1.2*y2[i] + 0.1*(rng.Float32()-0.5)
		}

		// Compute gCoeff
		var gCoeff [5]float32
		var y1y1, xny1 float32
		for i := 0; i < 40; i++ {
			y1y1 += y1[i] * y1[i]
			xny1 += xn[i] * y1[i]
		}
		gCoeff[0] = y1y1
		gCoeff[1] = -2.0 * xny1
		CorrXY2(xn[:], y1[:], y2[:], &gCoeff)

		// Encoder: QuantizeGain
		ga, gb, encPit, encCode := QuantizeGain(code[:], &gCoeff, &encMem, 0)

		// Decoder: DequantizeGain
		decPit, decCode := DequantizeGain(ga, gb, code[:], &decMem, 0, nil, nil)

		// Verify pitch gain match
		if math.Abs(float64(encPit-decPit)) > 1e-5 {
			t.Fatalf("frame %d: pitch gain mismatch enc=%f, dec=%f", frame, encPit, decPit)
		}

		// Verify codebook gain match
		if math.Abs(float64(encCode-decCode)) > 1e-4 {
			t.Fatalf("frame %d: code gain mismatch enc=%f, dec=%f", frame, encCode, decCode)
		}

		// Verify memory synchronization
		for i := 0; i < params.MA_NP; i++ {
			if math.Abs(float64(encMem[i]-decMem[i])) > 1e-5 {
				t.Fatalf("frame %d: MA memory[%d] desynchronized enc=%f, dec=%f",
					frame, i, encMem[i], decMem[i])
			}
		}
	}
}

// TestGainErasureConcealment verifies gain attenuation during frame erasures.
func TestGainErasureConcealment(t *testing.T) {
	var decMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}
	var lastPit float32 = 0.8
	var lastCode float32 = 2.5

	code := BuildCodeVector(100, 5)

	// Subframe with erasure (bfi = 1)
	pit1, code1 := DequantizeGain(0, 0, code[:], &decMem, 1, &lastPit, &lastCode)

	expectedPit := float32(0.8 * 0.9)
	expectedCode := float32(2.5 * 0.98)

	if math.Abs(float64(pit1-expectedPit)) > 1e-5 {
		t.Fatalf("erasure pitch gain got %f, want %f", pit1, expectedPit)
	}
	if math.Abs(float64(code1-expectedCode)) > 1e-5 {
		t.Fatalf("erasure code gain got %f, want %f", code1, expectedCode)
	}
}

// TestTamingProcedure verifies error accumulation and clipping flag generation.
func TestTamingProcedure(t *testing.T) {
	var excErr [4]float32
	InitExcErr(&excErr)

	// Initially error is 1.0, test_err should be 0 (no taming needed)
	if flag := TestErr(50, 0, &excErr); flag != 0 {
		t.Fatalf("expected no taming initially, got %d", flag)
	}

	// Repeatedly update with high pitch gain to simulate error buildup
	for i := 0; i < 50; i++ {
		UpdateExcErr(1.15, 25, &excErr)
	}

	// After large amplification on short pitch lag, test_err should trigger
	if flag := TestErr(25, 0, &excErr); flag != 1 {
		t.Fatalf("expected taming trigger after error buildup (excErr[0]=%f), got %d", excErr[0], flag)
	}
}

// TestGainConcurrency ensures QuantizeGain and DequantizeGain are safe from data races.
func TestGainConcurrency(t *testing.T) {
	const numGoroutines = 16
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			var encMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}
			var decMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}

			code := BuildCodeVector(gid*100, gid%16)
			var gCoeff = [5]float32{10.0, -5.0, 8.0, -4.0, 3.0}

			ga, gb, _, _ := QuantizeGain(code[:], &gCoeff, &encMem, 0)
			_, _ = DequantizeGain(ga, gb, code[:], &decMem, 0, nil, nil)
		}(g)
	}

	wg.Wait()
}

func BenchmarkQuantizeGain(b *testing.B) {
	var encMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}
	code := BuildCodeVector(1234, 9)
	var gCoeff = [5]float32{15.0, -8.0, 12.0, -6.0, 4.0}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = QuantizeGain(code[:], &gCoeff, &encMem, 0)
	}
}

func BenchmarkDequantizeGain(b *testing.B) {
	var decMem = [params.MA_NP]float32{-14.0, -14.0, -14.0, -14.0}
	code := BuildCodeVector(1234, 9)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DequantizeGain(3, 7, code[:], &decMem, 0, nil, nil)
	}
}
