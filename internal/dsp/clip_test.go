package dsp_test

import (
	"math"
	"testing"

	"github.com/selawe/go-g729/internal/dsp"
)

func TestSoftClip(t *testing.T) {
	// 1. Below threshold: values should remain completely untouched
	samples := []float32{0, 1000, -1000, 20000, -20000, 28000, -28000}
	orig := make([]float32, len(samples))
	copy(orig, samples)

	clipped := dsp.SoftClip(samples, 28000.0, 32760.0)
	if clipped != 0 {
		t.Errorf("expected 0 clipped samples, got %d", clipped)
	}
	for i := range samples {
		if samples[i] != orig[i] {
			t.Errorf("sample %d altered below threshold: got %f, want %f", i, samples[i], orig[i])
		}
	}

	// 2. Above threshold: smooth saturation curve without exceeding maxVal
	hotSamples := []float32{29000, 31000, 32767, 40000, -29000, -31000, -32768, -40000}
	clipped = dsp.SoftClip(hotSamples, 28000.0, 32760.0)
	if clipped != len(hotSamples) {
		t.Errorf("expected %d clipped samples, got %d", len(hotSamples), clipped)
	}

	for i, s := range hotSamples {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("sample %d is NaN/Inf", i)
		}
		if s > 32760.0 || s < -32760.0 {
			t.Errorf("sample %d exceeded ceiling: got %f (max 32760.0)", i, s)
		}
	}

	// Verify monotonic compression for positive samples
	if !(hotSamples[0] < hotSamples[1] && hotSamples[1] < hotSamples[2] && hotSamples[2] <= hotSamples[3]) {
		t.Errorf("positive compression is not monotonic: %+v", hotSamples[:4])
	}
}
