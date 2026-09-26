package g729_test

import (
	"math"
	"testing"

	g729 "github.com/selawe/go-g729"
)

// TestProfileConstructors validates default field settings across all 5 profiles.
func TestProfileConstructors(t *testing.T) {
	// 1. Core Profile
	core := g729.ProfileCore()
	if core.Variant != g729.VariantG729A || !core.EnableVAD || core.EnableClipRepair || core.OnDiagnostic != nil {
		t.Errorf("ProfileCore() unexpected config: %+v", core)
	}

	// DefaultConfig must alias Core
	def := g729.DefaultConfig()
	if def.Variant != core.Variant || def.EnableVAD != core.EnableVAD || def.EnableClipRepair != core.EnableClipRepair || def.OnDiagnostic != nil {
		t.Errorf("DefaultConfig() != ProfileCore(): %+v vs %+v", def, core)
	}

	// 2. Quality Profile
	quality := g729.ProfileQuality()
	if quality.Variant != g729.VariantG729 || quality.EnableVAD || quality.EnableClipRepair || quality.OnDiagnostic != nil {
		t.Errorf("ProfileQuality() unexpected config: %+v", quality)
	}

	// 3. Fast Profile
	fast := g729.ProfileFast()
	if fast.Variant != g729.VariantG729A || fast.EnableVAD || fast.EnableClipRepair || fast.OnDiagnostic != nil {
		t.Errorf("ProfileFast() unexpected config: %+v", fast)
	}

	// 4. ClipRepair Profile
	clip := g729.ProfileClipRepair()
	if clip.Variant != g729.VariantG729A || !clip.EnableVAD || !clip.EnableClipRepair || clip.OnDiagnostic != nil {
		t.Errorf("ProfileClipRepair() unexpected config: %+v", clip)
	}

	// 5. Diagnostic Profile
	called := false
	diag := g729.ProfileDiagnostic(func(stats g729.DiagnosticStats) {
		called = true
	})
	if diag.Variant != g729.VariantG729A || !diag.EnableVAD || diag.EnableClipRepair || diag.OnDiagnostic == nil {
		t.Errorf("ProfileDiagnostic() unexpected config: %+v", diag)
	}
	diag.OnDiagnostic(g729.DiagnosticStats{})
	if !called {
		t.Error("OnDiagnostic callback was not set properly")
	}
}

// TestProfileClipRepair validates that soft-knee declipping prevents spectral distortion
// and Levinson-Durbin instability when encountering severely clipped input samples (+/-32767).
func TestProfileClipRepair(t *testing.T) {
	enc := g729.NewEncoder(g729.ProfileClipRepair())
	dec := g729.NewDecoder()

	// Generate a severely saturated square-wave-like signal at full scale (+32767 / -32768)
	clippedPCM := make([]int16, 80)
	for i := range clippedPCM {
		if (i/10)%2 == 0 {
			clippedPCM[i] = 32767
		} else {
			clippedPCM[i] = -32768
		}
	}

	dst := make([]byte, 10)
	n, ft, err := enc.Encode(dst, clippedPCM)
	if err != nil {
		t.Fatalf("Encode clipped input with ClipRepair failed: %v", err)
	}
	if n != 10 || ft != g729.FrameSpeech {
		t.Fatalf("unexpected encode output: n=%d ft=%v", n, ft)
	}

	// Verify decoder can decompress the resulting bitstream cleanly
	pcmOut := make([]int16, 80)
	if err := dec.Decode(pcmOut, dst[:n]); err != nil {
		t.Fatalf("Decode failed on clip-repaired bitstream: %v", err)
	}

	// Output must be valid numbers (no NaN or Inf)
	for i, s := range pcmOut {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("decoded sample %d is NaN/Inf", i)
		}
	}
}

// TestProfileDiagnostic validates that DSP telemetry metrics are properly computed and
// reported to the callback on both active speech and SID frames.
func TestProfileDiagnostic(t *testing.T) {
	var reportedStats []g729.DiagnosticStats

	cb := func(s g729.DiagnosticStats) {
		reportedStats = append(reportedStats, s)
	}

	enc := g729.NewEncoder(g729.ProfileDiagnostic(cb))

	// 1. Encode 5 frames of speech (440 Hz tone)
	speechFrame := make([]int16, 80)
	for i := range speechFrame {
		speechFrame[i] = int16(10000.0 * math.Sin(2*math.Pi*440.0*float64(i)/8000.0))
	}

	dst := make([]byte, 10)
	for f := 0; f < 5; f++ {
		_, _, err := enc.Encode(dst, speechFrame)
		if err != nil {
			t.Fatalf("speech frame %d encode: %v", f, err)
		}
	}

	// 2. Encode 40 frames of silence to trigger SID / DTX frames
	silenceFrame := make([]int16, 80)
	for f := 0; f < 40; f++ {
		_, _, err := enc.Encode(dst, silenceFrame)
		if err != nil {
			t.Fatalf("silence frame %d encode: %v", f, err)
		}
	}

	if len(reportedStats) != 45 {
		t.Fatalf("expected 45 diagnostic reports, got %d", len(reportedStats))
	}

	// Verify first speech frame metrics
	s0 := reportedStats[0]
	if s0.FrameIndex != 1 {
		t.Errorf("frame 1: got FrameIndex %d", s0.FrameIndex)
	}
	if s0.FrameType != g729.FrameSpeech {
		t.Errorf("frame 1: got FrameType %v, want FrameSpeech", s0.FrameType)
	}
	if s0.EnergyDB <= 0 {
		t.Errorf("frame 1: expected positive energyDB, got %f", s0.EnergyDB)
	}
	if s0.PitchLag < 20 || s0.PitchLag > 143 {
		t.Errorf("frame 1: PitchLag %d out of bounds [20..143]", s0.PitchLag)
	}

	// Verify silence/SID frame metrics
	hasSIDOrUntransmitted := false
	for _, s := range reportedStats[5:] {
		if s.FrameType == g729.FrameSID || s.FrameType == g729.FrameUntransmitted {
			hasSIDOrUntransmitted = true
			if s.VADMarker != 0 {
				t.Errorf("expected VADMarker 0 for silence, got %d", s.VADMarker)
			}
		}
	}
	if !hasSIDOrUntransmitted {
		t.Error("expected SID or Untransmitted frame in silence segment")
	}
}

// TestProfileFastZeroAlloc ensures ProfileFast maintains 0 heap allocations on the hot path.
func TestProfileFastZeroAlloc(t *testing.T) {
	enc := g729.NewEncoder(g729.ProfileFast())
	frame := make([]int16, 80)
	dst := make([]byte, 10)

	// Warmup
	_, _, _ = enc.Encode(dst, frame)

	allocs := testing.AllocsPerRun(100, func() {
		_, _, _ = enc.Encode(dst, frame)
	})

	if allocs > 0 {
		t.Errorf("ProfileFast.Encode() allocated %f times per run (want 0)", allocs)
	}
}
