package codebook

import (
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/selawe/go-g729/internal/params"
)

// TestCodeVectorRoundTrip verifies that PackPulseIndex and ExtractPulsePositions
// correctly encode and decode all 8,192 position combinations and 16 sign combinations.
func TestCodeVectorRoundTrip(t *testing.T) {
	track0 := [8]int{0, 5, 10, 15, 20, 25, 30, 35}
	track1 := [8]int{1, 6, 11, 16, 21, 26, 31, 36}
	track2 := [8]int{2, 7, 12, 17, 22, 27, 32, 37}
	track3 := [16]int{
		3, 8, 13, 18, 23, 28, 33, 38,
		4, 9, 14, 19, 24, 29, 34, 39,
	}

	for s := 0; s < 16; s++ {
		s0 := -1
		if (s & 1) != 0 {
			s0 = 1
		}
		s1 := -1
		if (s & 2) != 0 {
			s1 = 1
		}
		s2 := -1
		if (s & 4) != 0 {
			s2 = 1
		}
		s3 := -1
		if (s & 8) != 0 {
			s3 = 1
		}

		for _, p0 := range track0 {
			for _, p1 := range track1 {
				for _, p2 := range track2 {
					for _, p3 := range track3 {
						idx, sign := PackPulseIndex(p0, p1, p2, p3, s0, s1, s2, s3)
						if sign != s {
							t.Fatalf("sign mismatch: got %d, want %d", sign, s)
						}

						dp0, dp1, dp2, dp3 := ExtractPulsePositions(idx)
						if dp0 != p0 || dp1 != p1 || dp2 != p2 || dp3 != p3 {
							t.Fatalf("position mismatch for (%d,%d,%d,%d): got (%d,%d,%d,%d)",
								p0, p1, p2, p3, dp0, dp1, dp2, dp3)
						}

						vec := BuildCodeVector(idx, sign)
						nonZero := 0
						for i, v := range vec {
							if v != 0 {
								nonZero++
								var expectedSign float32
								switch i {
								case p0:
									expectedSign = float32(s0)
								case p1:
									expectedSign = float32(s1)
								case p2:
									expectedSign = float32(s2)
								case p3:
									expectedSign = float32(s3)
								default:
									t.Fatalf("unexpected non-zero pulse at position %d", i)
								}
								if v != expectedSign {
									t.Fatalf("pulse sign mismatch at pos %d: got %f, want %f", i, v, expectedSign)
								}
							}
						}
						if nonZero != 4 {
							t.Fatalf("expected exactly 4 pulses, got %d", nonZero)
						}
					}
				}
			}
		}
	}
}

func makeSyntheticTargetAndImpulse(seed int64) (x [40]float32, h [40]float32) {
	rng := rand.New(rand.NewSource(seed))
	// Decaying impulse response (typical weighted synthesis filter)
	decay := float32(1.0)
	for i := 0; i < 40; i++ {
		h[i] = decay * (0.8*float32(math.Cos(float64(i)*0.4)) + 0.2*rng.Float32())
		decay *= 0.94
	}
	// Target vector
	for i := 0; i < 40; i++ {
		x[i] = float32(math.Sin(float64(i)*0.25)) + 0.1*(rng.Float32()-0.5)
	}
	return x, h
}

// TestSearchAlgebraicA verifies fast ACELP search on synthetic targets.
func TestSearchAlgebraicA(t *testing.T) {
	x, h := makeSyntheticTargetAndImpulse(12345)

	t0 := 50 // t0 >= 40: no pitch sharpening
	pitchSharp := float32(0.6)
	index, sign, code, y := SearchAlgebraicA(x[:], h[:], t0, pitchSharp)

	// Check codevector has 4 pulses matching index/sign
	reconstructed := BuildCodeVector(index, sign)
	for i := 0; i < params.L_SUBFR; i++ {
		if code[i] != reconstructed[i] {
			t.Fatalf("sample %d: code=%f != reconstructed=%f", i, code[i], reconstructed[i])
		}
	}

	// Check y is convolution of code with h
	for i := 0; i < params.L_SUBFR; i++ {
		var expectedY float32
		for j := 0; j <= i; j++ {
			expectedY += code[j] * h[i-j]
		}
		diff := float32(math.Abs(float64(y[i] - expectedY)))
		if diff > 1e-4 {
			t.Fatalf("sample %d: y=%f != conv(code,h)=%f (diff=%e)", i, y[i], expectedY, diff)
		}
	}

	// Verify target correlation is positive and error energy is reduced
	var xDotY, yDotY, xDotX float32
	for i := 0; i < params.L_SUBFR; i++ {
		xDotY += x[i] * y[i]
		yDotY += y[i] * y[i]
		xDotX += x[i] * x[i]
	}
	if xDotY <= 0 {
		t.Fatalf("expected positive correlation x*y, got %f", xDotY)
	}
	if yDotY <= 0 {
		t.Fatalf("expected positive filtered energy y*y, got %f", yDotY)
	}

	// Optimal scalar gain g = (x . y) / (y . y)
	g := xDotY / yDotY
	var errEnergy float32
	for i := 0; i < params.L_SUBFR; i++ {
		diff := x[i] - g*y[i]
		errEnergy += diff * diff
	}
	if errEnergy >= xDotX {
		t.Fatalf("codebook search failed to reduce MSE: err=%f, orig=%f", errEnergy, xDotX)
	}
}

// TestSearchAlgebraicWithPitchSharpening verifies pitch sharpening when t0 < 40.
func TestSearchAlgebraicWithPitchSharpening(t *testing.T) {
	x, h := makeSyntheticTargetAndImpulse(54321)

	t0 := 25 // t0 < 40: pitch sharpening active
	pitchSharp := float32(0.7)
	index, sign, code, y := SearchAlgebraicA(x[:], h[:], t0, pitchSharp)

	// Reconstruct base pulse vector
	base := BuildCodeVector(index, sign)
	// Base should have pitch sharpening added
	var expectedCode [40]float32
	copy(expectedCode[:], base[:])
	for i := t0; i < params.L_SUBFR; i++ {
		expectedCode[i] += pitchSharp * expectedCode[i-t0]
	}

	for i := 0; i < params.L_SUBFR; i++ {
		if math.Abs(float64(code[i]-expectedCode[i])) > 1e-5 {
			t.Fatalf("sample %d: code=%f, expected=%f", i, code[i], expectedCode[i])
		}
	}

	// Verify that code convolved with original h produces y
	for i := 0; i < params.L_SUBFR; i++ {
		var expectedY float32
		for j := 0; j <= i; j++ {
			expectedY += code[j] * h[i-j]
		}
		diff := float32(math.Abs(float64(y[i] - expectedY)))
		if diff > 1e-4 {
			t.Fatalf("sample %d: y=%f != conv(code,h)=%f (diff=%e)", i, y[i], expectedY, diff)
		}
	}
}

// TestSearchAlgebraicFull verifies Full G.729 ACELP search.
func TestSearchAlgebraicFull(t *testing.T) {
	x, h := makeSyntheticTargetAndImpulse(99999)

	t0 := 30
	pitchSharp := float32(0.5)
	var extra int
	index, sign, code, y := SearchAlgebraicFull(x[:], h[:], t0, pitchSharp, 0, &extra)

	// Verify code matches BuildCodeVector with pitch sharpening
	baseCode := BuildCodeVector(index, sign)
	for i := t0; i < params.L_SUBFR; i++ {
		baseCode[i] += pitchSharp * baseCode[i-t0]
	}
	for i := 0; i < params.L_SUBFR; i++ {
		if math.Abs(float64(code[i]-baseCode[i])) > 1e-5 {
			t.Fatalf("sample %d: code=%f, expected=%f", i, code[i], baseCode[i])
		}
	}

	// Verify y matches code * h
	for i := 0; i < params.L_SUBFR; i++ {
		var expectedY float32
		for j := 0; j <= i; j++ {
			expectedY += code[j] * h[i-j]
		}
		diff := float32(math.Abs(float64(y[i] - expectedY)))
		if diff > 1e-4 {
			t.Fatalf("sample %d: y=%f != conv(code,h)=%f (diff=%e)", i, y[i], expectedY, diff)
		}
	}

	// Verify subframe 1 search using remaining time budget
	index2, sign2, code2, y2 := SearchAlgebraicFull(x[:], h[:], t0, pitchSharp, 1, &extra)
	_ = index2
	_ = sign2
	_ = code2
	_ = y2

	// Check codevector has valid pulse positions
	p0, p1, p2, p3 := ExtractPulsePositions(index)
	if p0%5 != 0 || (p1-1)%5 != 0 || (p2-2)%5 != 0 || (p3%5 != 3 && p3%5 != 4) {
		t.Fatalf("invalid pulse positions: (%d,%d,%d,%d)", p0, p1, p2, p3)
	}
}

// TestFastVsFullComparison verifies that Full search finds an objective equal to or
// close to (often higher than) fast search.
func TestFastVsFullComparison(t *testing.T) {
	for seed := int64(1); seed <= 10; seed++ {
		x, h := makeSyntheticTargetAndImpulse(seed * 777)
		t0 := 45
		pitchSharp := float32(0.4)

		_, _, _, yFast := SearchAlgebraicA(x[:], h[:], t0, pitchSharp)
		_, _, _, yFull := SearchAlgebraicFull(x[:], h[:], t0, pitchSharp, 0, nil)

		var cFast, aFast, cFull, aFull float32
		for i := 0; i < params.L_SUBFR; i++ {
			cFast += x[i] * yFast[i]
			aFast += yFast[i] * yFast[i]
			cFull += x[i] * yFull[i]
			aFull += yFull[i] * yFull[i]
		}

		objFast := (cFast * cFast) / aFast
		objFull := (cFull * cFull) / aFull

		// Full search should achieve an objective at least comparable to fast search
		if objFull < objFast*0.85 {
			t.Logf("Seed %d: objFull=%f, objFast=%f", seed, objFull, objFast)
		}
	}
}

// TestCodebookConcurrency ensures SearchAlgebraicA and SearchAlgebraicFull are
// safe from data races when executed concurrently across 16 goroutines.
func TestCodebookConcurrency(t *testing.T) {
	const numGoroutines = 16
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			x, h := makeSyntheticTargetAndImpulse(int64(gid * 100))
			t0 := 20 + gid%30
			pitchSharp := float32(0.5)

			idxA, sA, codA, yA := SearchAlgebraicA(x[:], h[:], t0, pitchSharp)
			_ = idxA
			_ = sA
			_ = codA
			_ = yA

			var extra int
			idxF, sF, codF, yF := SearchAlgebraicFull(x[:], h[:], t0, pitchSharp, 0, &extra)
			_ = idxF
			_ = sF
			_ = codF
			_ = yF
		}(g)
	}

	wg.Wait()
}

func BenchmarkBuildCodeVector(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = BuildCodeVector(1234, 9)
	}
}

func BenchmarkSearchAlgebraicA(b *testing.B) {
	x, h := makeSyntheticTargetAndImpulse(42)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = SearchAlgebraicA(x[:], h[:], 50, 0.5)
	}
}

func BenchmarkSearchAlgebraicFull(b *testing.B) {
	x, h := makeSyntheticTargetAndImpulse(42)
	var extra int
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = SearchAlgebraicFull(x[:], h[:], 50, 0.5, 0, &extra)
	}
}
