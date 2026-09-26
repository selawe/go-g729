package g729

import "errors"

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
)

// Encoder defines the interface for compressing 8 kHz 16-bit linear PCM audio into G.729 bitstream frames.
// An Encoder instance is NOT safe for concurrent use across multiple goroutines; each audio stream
// must have its own Encoder instance.
type Encoder interface {
	// Encode processes 80 samples (10 ms) of 16-bit linear PCM audio in src, and writes the encoded
	// bitstream into dst.
	//
	// dst must have capacity of at least 10 bytes.
	//
	// Returns:
	//   - n: number of bytes written to dst (10 for FrameSpeech, 2 for FrameSID, 0 for FrameUntransmitted)
	//   - frameType: type of the produced frame
	//   - err: nil on success, or an error if buffer lengths are invalid
	Encode(dst []byte, src []int16) (n int, frameType FrameType, err error)

	// Reset clears all internal state, delay lines, and history buffers to their initial reset state.
	Reset()
}

// Decoder defines the interface for decompressing G.729 bitstream frames into 8 kHz 16-bit linear PCM audio.
// A Decoder instance is NOT safe for concurrent use across multiple goroutines; each audio stream
// must have its own Decoder instance.
type Decoder interface {
	// Decode decompresses a G.729 bitstream frame in src (10 bytes for speech, 2 bytes for SID,
	// or 0 bytes / nil for packet loss erasure / untransmitted frame) into 80 16-bit PCM samples in dst.
	//
	// dst must have capacity of at least 80 samples.
	//
	// Returns nil on success, or an error if dst or src lengths are invalid.
	Decode(dst []int16, src []byte) error

	// Reset clears all internal state, delay lines, and history buffers to their initial reset state.
	Reset()
}

