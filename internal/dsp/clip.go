package dsp

import "math"

// SoftClip applies a smooth soft-knee compression to audio samples exceeding the threshold.
//
// When speech signals hit the digital ceiling (+/-32767) due to hot microphone gain or
// line overdrive, hard square-wave clipping injects broadband high-frequency energy.
// This severely distorts the autocorrelation matrix and can cause Levinson-Durbin
// reflection coefficients or LPC synthesis filters to become unstable.
//
// SoftClip compresses samples in the [threshold, maxVal] region using a hyperbolic
// tangent curve, preserving a continuous first derivative and smoothing abrupt
// clipping boundaries while leaving signals below threshold unaltered.
//
// Returns the count of samples that exceeded the threshold.
func SoftClip(samples []float32, threshold, maxVal float32) int {
	clippedCount := 0
	diff := maxVal - threshold
	if diff <= 0 {
		return 0
	}
	invDiff := 1.0 / float64(diff)

	for i, x := range samples {
		if x > threshold {
			clippedCount++
			if x >= maxVal {
				samples[i] = maxVal
			} else {
				norm := float64(x-threshold) * invDiff
				samples[i] = threshold + diff*float32(math.Tanh(norm))
			}
		} else if x < -threshold {
			clippedCount++
			if x <= -maxVal {
				samples[i] = -maxVal
			} else {
				norm := float64(-x-threshold) * invDiff
				samples[i] = -(threshold + diff*float32(math.Tanh(norm)))
			}
		}
	}
	return clippedCount
}
