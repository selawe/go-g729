package pitch

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
)

// OpenLoopPitch estimates the pitch lag for an 80-sample frame using normalized
// autocorrelation across 3 search sections with decimation and pitch multiple testing
// (ITU-T G.729A pitch_ol_fast).
//
// wsp must contain at least params.PIT_MAX (143) past samples plus params.L_FRAME (80)
// current frame samples (total length >= 223).
// The current frame starts at offset params.PIT_MAX (143).
func OpenLoopPitch(wsp []float32) int {
	bestT, _, _, _ := OpenLoopPitchCandidates(wsp)
	return bestT
}

// OpenLoopPitchCandidates returns the selected pitch lag along with the 3 section
// candidates T1 (20..39), T2 (40..79), and T3 (80..143).
func OpenLoopPitchCandidates(wsp []float32) (bestT, T1, T2, T3 int) {
	const (
		offset = params.PIT_MAX // 143
		lFrame = params.L_FRAME // 80
	)
	if len(wsp) < offset+lFrame {
		panic("pitch: wsp length must be at least PIT_MAX + L_FRAME (223)")
	}

	// Section 1: lag delay = 20 to 39
	max1 := float32(-math.MaxFloat32)
	T1 = 20
	for i := 20; i < 40; i++ {
		var sum float32
		p1Idx := offset - i
		for j := 0; j < lFrame; j += 2 {
			sum += wsp[offset+j] * wsp[p1Idx+j]
		}
		if sum > max1 {
			max1 = sum
			T1 = i
		}
	}
	var energy1 float32 = 0.01
	p1Idx := offset - T1
	for j := 0; j < lFrame; j += 2 {
		v := wsp[p1Idx+j]
		energy1 += v * v
	}
	max1 /= float32(math.Sqrt(float64(energy1)))

	// Section 2: lag delay = 40 to 79
	max2 := float32(-math.MaxFloat32)
	T2 = 40
	for i := 40; i < 80; i++ {
		var sum float32
		p2Idx := offset - i
		for j := 0; j < lFrame; j += 2 {
			sum += wsp[offset+j] * wsp[p2Idx+j]
		}
		if sum > max2 {
			max2 = sum
			T2 = i
		}
	}
	var energy2 float32 = 0.01
	p2Idx := offset - T2
	for j := 0; j < lFrame; j += 2 {
		v := wsp[p2Idx+j]
		energy2 += v * v
	}
	max2 /= float32(math.Sqrt(float64(energy2)))

	// Section 3: lag delay = 80 to 142 (decimation by 2 for candidate delays)
	max3 := float32(-math.MaxFloat32)
	T3 = 80
	for i := 80; i < 143; i += 2 {
		var sum float32
		p3Idx := offset - i
		for j := 0; j < lFrame; j += 2 {
			sum += wsp[offset+j] * wsp[p3Idx+j]
		}
		if sum > max3 {
			max3 = sum
			T3 = i
		}
	}

	// Test around T3: candT3+1 and candT3-1
	candT3 := T3
	if candT3+1 <= params.PIT_MAX {
		p3Idx := offset - (candT3 + 1)
		var sum float32
		for j := 0; j < lFrame; j += 2 {
			sum += wsp[offset+j] * wsp[p3Idx+j]
		}
		if sum > max3 {
			max3 = sum
			T3 = candT3 + 1
		}
	}
	if candT3-1 >= 80 {
		p3Idx := offset - (candT3 - 1)
		var sum float32
		for j := 0; j < lFrame; j += 2 {
			sum += wsp[offset+j] * wsp[p3Idx+j]
		}
		if sum > max3 {
			max3 = sum
			T3 = candT3 - 1
		}
	}

	var energy3 float32 = 0.01
	p3Idx := offset - T3
	for j := 0; j < lFrame; j += 2 {
		v := wsp[p3Idx+j]
		energy3 += v * v
	}
	max3 /= float32(math.Sqrt(float64(energy3)))

	// Test for pitch multiples (cumulative additions per matching multiple)
	if abs(T2*2-T3) < 5 {
		max2 += max3 * 0.25
	}
	if abs(T2*3-T3) < 7 {
		max2 += max3 * 0.25
	}
	if abs(T1*2-T2) < 5 {
		max1 += max2 * 0.20
	}
	if abs(T1*3-T2) < 7 {
		max1 += max2 * 0.20
	}

	// Compare section maxima
	bestT = T1
	bestMax := max1
	if max2 > bestMax {
		bestMax = max2
		bestT = T2
	}
	if max3 > bestMax {
		bestT = T3
	}

	return bestT, T1, T2, T3
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
