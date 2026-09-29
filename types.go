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
	TotalFrames    int   // Total frames processed
	SpeechFrames   int   // Active speech frames encoded (10 bytes)
	SIDFrames      int   // Comfort noise SID frames emitted (2 bytes)
	Untransmitted  int   // DTX suppressed frames (0 bytes)
	BytesEmitted   int64 // Total bitstream bytes written (speech + SID)
	ClippedSamples int64 // Cumulative number of soft-clipped/saturated input samples
}

// DecoderStats contains cumulative runtime metrics for a Decoder instance.
type DecoderStats struct {
	TotalFrames     int // Total frames received and decoded
	SpeechFrames    int // Active speech frames decoded
	SIDFrames       int // Comfort noise SID frames processed
	ConcealedFrames int // Lost speech frames concealed via PLC
	Untransmitted   int // Untransmitted DTX silence frames
	LastBFICount    int // Consecutive bad frame (BFI) count currently active
}

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

	// EncodeBatch encodes multiple consecutive 10 ms speech frames (multiples of 80 int16 samples,
	// e.g. 160 samples for 20 ms, 320 samples for 40 ms) sequentially into dst.
	//
	// dst must have capacity of at least (len(src)/80)*10 bytes.
	// Returns total bytes written, a slice of FrameType for each encoded frame, or an error.
	//
	// For zero-allocation batch encoding in high-throughput transcoders, use EncodeBatchInto.
	EncodeBatch(dst []byte, src []int16) (n int, frameTypes []FrameType, err error)

	// EncodeBatchInto encodes multiple 10 ms frames without allocating a frameTypes slice.
	// Caller must provide frameTypes with length >= len(src)/80. Returns total bytes written
	// and the number of frames actually encoded (may be less than cap on error).
	EncodeBatchInto(dst []byte, src []int16, frameTypes []FrameType) (n int, numFrames int, err error)

	// Stats returns cumulative operational telemetry for this encoder.
	Stats() EncoderStats

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

	// DecodeBatch decompresses multiple G.729 bitstream frames sequentially into dst.
	// dst must have capacity of at least len(frames)*80 samples.
	DecodeBatch(dst []int16, frames [][]byte) error

	// Stats returns cumulative operational and PLC telemetry for this decoder.
	Stats() DecoderStats

	// Reset clears all internal state, delay lines, and history buffers to their initial reset state.
	Reset()
}

