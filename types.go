package g729

import (
	"errors"
	"fmt"
)

// Variant specifies the ITU-T G.729 encoder/decoder algorithm variant.
type Variant int

const (
	// VariantG729A specifies ITU-T G.729 Annex A (reduced complexity, default).
	VariantG729A Variant = iota
	// VariantG729 specifies full-complexity ITU-T G.729 (nested ACELP search, harmonic weighting).
	VariantG729
)

// FrameType identifies the transmission type of an encoded G.729 frame.
type FrameType int

const (
	// FrameSpeech indicates an active speech frame (10 bytes = 80 bits).
	FrameSpeech FrameType = iota
	// FrameSID indicates a Silence Insertion Descriptor frame under Annex B DTX (2 bytes = 16 bits).
	FrameSID
	// FrameUntransmitted indicates a silence frame suppressed by DTX (0 bytes).
	FrameUntransmitted
)

// DiagnosticStats captures DSP telemetry metrics for one 10 ms encoded frame.
type DiagnosticStats struct {
	FrameIndex   int        // 1-indexed frame sequence number
	FrameType    FrameType  // FrameSpeech, FrameSID, or FrameUntransmitted
	EnergyDB     float32    // Full-band normalized energy in dB
	ZeroCrossing float32    // Normalized zero-crossing rate [0..1]
	VADMarker    int        // 1: Voice, 0: Noise
	PitchLag     int        // Open-loop pitch delay [20..143] (0 if uncomputed or SID)
	GainPitch    [2]float32 // Subframe pitch gains (g_p)
	GainCode     [2]float32 // Subframe algebraic codebook gains (g_c)
	ClippedCount int        // Number of input samples that saturated (|x| >= 32760)
	// LPCFallback is true when the Levinson-Durbin prediction error clamping path
	// fired during this frame's LPC analysis. Non-zero under pathological numerical
	// conditions; if you see this, investigate the input signal for saturation or NaN.
	LPCFallback bool
}

// DiagnosticCallback is a telemetry hook invoked after each frame is encoded.
// It should execute quickly without blocking or allocating on the heap.
type DiagnosticCallback func(stats DiagnosticStats)

// Config configures the operating parameters of the G.729 encoder.
type Config struct {
	// Variant selects between G.729 Annex A (fast) and full G.729.
	Variant Variant
	// EnableVAD enables Voice Activity Detection (VAD) and Discontinuous Transmission (DTX)
	// per ITU-T G.729 Annex B. When false, the encoder operates in constant bit-rate (CBR) 8 kbps.
	EnableVAD bool
	// EnableClipRepair enables soft-knee input declipping pre-processing to protect
	// LPC Levinson-Durbin analysis against hard ADC saturation and microphone clipping.
	EnableClipRepair bool
	// OnDiagnostic is an optional telemetry callback invoked per frame with DSP metrics.
	OnDiagnostic DiagnosticCallback
	// DisablePanicRecovery when true disables automatic recover() in Encode(), allowing
	// internal DSP panics to propagate directly (useful for debugging and unit tests).
	DisablePanicRecovery bool
	// IncludePanicStack when true causes ErrInternalPanic errors to include a full
	// runtime stack trace (debug.Stack). Default false to avoid leaking internal build
	// paths through error strings propagated to remote clients or user-facing logs.
	IncludePanicStack bool
}

// DecoderConfig configures the operating parameters of the G.729 decoder.
type DecoderConfig struct {
	// DisablePanicRecovery when true disables automatic recover() in Decode(),
	// allowing internal DSP panics to propagate directly (useful for debugging).
	DisablePanicRecovery bool
	// IncludePanicStack when true causes ErrInternalPanic errors to include a full
	// runtime stack trace (debug.Stack). Default false to avoid leaking internal build
	// paths through error strings propagated to remote clients or user-facing logs.
	IncludePanicStack bool
}

// DefaultConfig returns the standard configuration: G.729A with Annex B VAD enabled.
func DefaultConfig() Config {
	return ProfileCore()
}

// ProfileCore returns the standard G.729A configuration with Annex B VAD/DTX enabled (VoIP default).
func ProfileCore() Config {
	return Config{
		Variant:   VariantG729A,
		EnableVAD: true,
	}
}

// ProfileQuality returns the full-complexity ITU-T G.729 configuration with nested ACELP search
// and harmonic weighting for maximum audio fidelity and SNR.
//
// VAD is intentionally disabled (CBR mode) because this profile targets scenarios such as
// recording, transcoding, and quality benchmarking where every frame must be encoded at full
// quality. DTX silence transitions can introduce spectral discontinuities that defeat A/B
// comparisons. To combine G.729 Full quality with Annex B efficiency, override after
// construction: cfg := ProfileQuality(); cfg.EnableVAD = true.
func ProfileQuality() Config {
	return Config{
		Variant:   VariantG729,
		EnableVAD: false,
	}
}

// ProfileFast returns the reduced-complexity G.729A configuration with VAD disabled (CBR 8 kbps)
// for minimal CPU latency and maximum throughput (~250x real-time).
func ProfileFast() Config {
	return Config{
		Variant:   VariantG729A,
		EnableVAD: false,
	}
}

// ProfileClipRepair returns a G.729A configuration with soft-knee saturation repair enabled
// to mitigate harsh clipping and LPC instability from high-gain microphones or PSTN line overdrive.
func ProfileClipRepair() Config {
	return Config{
		Variant:          VariantG729A,
		EnableVAD:        true,
		EnableClipRepair: true,
	}
}

// ProfileDiagnostic returns a G.729A configuration with per-frame DSP telemetry reporting
// via the provided DiagnosticCallback.
func ProfileDiagnostic(cb DiagnosticCallback) Config {
	return Config{
		Variant:      VariantG729A,
		EnableVAD:    true,
		OnDiagnostic: cb,
	}
}

// Common sentinel errors returned by G.729 Encoder and Decoder.
var (
	// ErrInvalidInputLen is returned when the input PCM sample slice does not contain exactly 80 samples.
	ErrInvalidInputLen = errors.New("g729: src must be exactly 80 int16 samples")

	// ErrInvalidOutputLen is returned when the destination buffer lacks capacity (min 10 bytes for encoder, 80 int16 for decoder).
	ErrInvalidOutputLen = errors.New("g729: dst buffer capacity is insufficient")

	// ErrInvalidFrameLen is returned when the decoder receives a frame byte slice of invalid length (valid: 0, 2, or 10 bytes).
	ErrInvalidFrameLen = errors.New("g729: src length must be 0, 2, or 10 bytes")

	// ErrInternalPanic is returned when an internal DSP panic is trapped by panic recovery.
	ErrInternalPanic = errors.New("g729: internal DSP panic")

	// ErrNilEncoder is returned when a method is called on a nil *Encoder.
	ErrNilEncoder = errors.New("g729: nil Encoder")

	// ErrNilDecoder is returned when a method is called on a nil *Decoder.
	ErrNilDecoder = errors.New("g729: nil Decoder")
)

// buildPanicError formats a recovered panic value as an error wrapping
// ErrInternalPanic. When includeStack is true a runtime stack trace is
// appended; otherwise the returned error contains only the panic value,
// avoiding disclosure of internal build paths in propagated errors.
func buildPanicError(recovered any, stack []byte, includeStack bool) error {
	if includeStack {
		return fmt.Errorf("%w: %v\nstack:\n%s", ErrInternalPanic, recovered, stack)
	}
	return fmt.Errorf("%w: %v", ErrInternalPanic, recovered)
}

// EncoderStats contains cumulative runtime metrics for an Encoder instance.
type EncoderStats struct {
	TotalFrames      int   // Total frames processed
	SpeechFrames     int   // Active speech frames encoded (10 bytes)
	SIDFrames        int   // Comfort noise SID frames emitted (2 bytes)
	Untransmitted    int   // DTX suppressed frames (0 bytes)
	BytesEmitted     int64 // Total bitstream bytes written (speech + SID)
	ClippedSamples   int64 // Cumulative number of soft-clipped/saturated input samples
	DiagnosticPanics int   // Panics recovered from OnDiagnostic callback; non-zero indicates a buggy callback
}

// DecoderStats contains cumulative runtime metrics for a Decoder instance.
type DecoderStats struct {
	TotalFrames     int // Total frames received and decoded
	SpeechFrames    int // Active speech frames decoded
	SIDFrames       int // Comfort noise SID frames processed
	ConcealedFrames int // Lost speech frames concealed via PLC
	Untransmitted   int // Untransmitted DTX silence frames
	LastBFICount    int // Consecutive bad frame (BFI) count currently active
	// ParityErrors is the cumulative number of speech frames whose P1 pitch-delay
	// parity bit mismatched P0, indicating bit-error corruption in transit. When
	// this counter grows, it signals a lossy or noisy transport (radio, unreliable
	// UDP path) and useful for driving RTCP receiver-report style feedback.
	ParityErrors int
}


