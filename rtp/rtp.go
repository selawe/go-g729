// Package rtp provides G.729 RTP payload packetization per RFC 3551 and RFC 3389.
//
// G.729 RTP basics:
//   - Static payload type: 18 (G.729 and G.729A share the same payload type)
//   - Clock rate: 8000 Hz
//   - Channels: 1 (mono)
//   - Active speech frame: 10 bytes (80 bits per 10 ms frame)
//   - SID frame (Annex B comfort noise): 2 bytes
//   - Suppressed frames: no RTP packet is sent
//
// Multiple consecutive G.729 frames may be bundled into a single RTP packet
// to reduce header overhead at the cost of higher latency. A 20 ms packet
// carries 2 frames (20 bytes); 40 ms carries 4 frames (40 bytes).
//
// Annex B (DTX) interoperability:
//   - SID frames use payload type 18 with a 2-byte payload.
//   - Suppressed frames produce no RTP packet; the receiver generates comfort
//     noise from the last received SID parameters.
//   - Packet Loss Concealment (PLC) is triggered by calling Decoder.Decode
//     with a nil or zero-length source slice.
package rtp

import (
	"errors"
	"fmt"
)

const (
	// PayloadType is the IANA-registered static RTP payload type for G.729.
	PayloadType = 18

	// ClockRate is the RTP timestamp clock rate for G.729 in Hz.
	ClockRate = 8000

	// FrameBytes is the number of bytes per active G.729 speech frame (10 ms).
	FrameBytes = 10

	// SIDBytes is the number of bytes per G.729 Annex B SID frame.
	SIDBytes = 2

	// FrameDurationMs is the duration of one G.729 frame in milliseconds.
	FrameDurationMs = 10

	// TimestampIncrement is the RTP timestamp increment per frame (samples @ 8 kHz).
	TimestampIncrement = 80
)

// Sentinel errors.
var (
	// ErrInvalidPayload is returned when a payload byte slice cannot be
	// parsed as valid G.729 frame data.
	ErrInvalidPayload = errors.New("rtp: invalid G.729 payload length")

	// ErrEmptyPayload is returned when Pack receives no frames.
	ErrEmptyPayload = errors.New("rtp: no frames to pack")

	// ErrBufferTooSmall is returned when a destination slice has insufficient capacity.
	ErrBufferTooSmall = errors.New("rtp: destination buffer too small")
)

// FrameType classifies the content type of a G.729 payload or frame.
type FrameType int

const (
	// FrameSpeech indicates an active speech frame (10 bytes).
	FrameSpeech FrameType = iota
	// FrameSID indicates a Silence Insertion Descriptor frame (2 bytes, Annex B).
	FrameSID
	// FrameSuppressed indicates a DTX-suppressed frame (no bytes transmitted).
	FrameSuppressed
)

func (f FrameType) String() string {
	switch f {
	case FrameSpeech:
		return "speech"
	case FrameSID:
		return "SID"
	case FrameSuppressed:
		return "suppressed"
	default:
		return fmt.Sprintf("FrameType(%d)", int(f))
	}
}

// PayloadInfo describes the content of a parsed RTP payload.
type PayloadInfo struct {
	// Type is the frame type present in the payload.
	Type FrameType
	// NumFrames is the number of speech frames bundled in the payload.
	// Always 0 for SID and Suppressed.
	NumFrames int
	// DurationMs is the total audio duration covered by this payload in ms.
	DurationMs int
}

// Pack bundles one or more G.729 encoded frames into a single RTP payload.
//
// Rules:
//   - Speech frames: all must be exactly 10 bytes. Multiple frames are
//     concatenated; the payload length will be n×10.
//   - SID frame: exactly one 2-byte frame; must not be mixed with speech frames.
//   - Suppressed frames: caller must not call Pack for suppressed frames —
//     simply skip sending an RTP packet.
// HeaderOverheadIPv4RTP is the typical size in bytes of IPv4 (20B) + UDP (8B) + RTP (12B) headers.
const HeaderOverheadIPv4RTP = 40

// FramesPerPacketForMTU calculates the maximum number of G.729 speech frames (10 ms / 10 bytes each)
// that fit within the specified path MTU, accounting for standard IPv4/UDP/RTP headers (40 bytes).
// Returns 0 if mtu is too small to carry at least one 10-byte frame.
func FramesPerPacketForMTU(mtu int) int {
	available := mtu - HeaderOverheadIPv4RTP
	if available < FrameBytes {
		return 0
	}
	return available / FrameBytes
}

// PackInto bundles one or more G.729 encoded frames into dst without heap allocation.
// dst must have capacity >= the total packed payload size (2 bytes for SID, len(frames)*10 for speech).
//
// Returns the number of bytes written to dst, or an error if frames are invalid or dst is too small.
func PackInto(dst []byte, frames [][]byte) (int, error) {
	if len(frames) == 0 {
		return 0, ErrEmptyPayload
	}

	first := len(frames[0])
	if first != FrameBytes && first != SIDBytes {
		return 0, fmt.Errorf("%w: first frame has %d bytes (want %d or %d)",
			ErrInvalidPayload, first, FrameBytes, SIDBytes)
	}

	if first == SIDBytes {
		if len(frames) != 1 {
			return 0, fmt.Errorf("%w: SID payload must contain exactly 1 frame, got %d",
				ErrInvalidPayload, len(frames))
		}
		if len(dst) < SIDBytes {
			return 0, fmt.Errorf("%w: dst length %d < %d", ErrBufferTooSmall, len(dst), SIDBytes)
		}
		copy(dst[:SIDBytes], frames[0])
		return SIDBytes, nil
	}

	total := len(frames) * FrameBytes
	if len(dst) < total {
		return 0, fmt.Errorf("%w: dst length %d < %d", ErrBufferTooSmall, len(dst), total)
	}

	for i, f := range frames {
		if len(f) != FrameBytes {
			return 0, fmt.Errorf("%w: frame %d has %d bytes (want %d for speech)",
				ErrInvalidPayload, i, len(f), FrameBytes)
		}
		copy(dst[i*FrameBytes:(i+1)*FrameBytes], f)
	}

	return total, nil
}

// Pack bundles one or more G.729 encoded frames into a single allocated RTP payload.
//
// Rules:
//   - Speech frames: all must be exactly 10 bytes. Multiple frames are
//     concatenated; the payload length will be n×10.
//   - SID frame: exactly one 2-byte frame; must not be mixed with speech frames.
//   - Suppressed frames: caller must not call Pack for suppressed frames —
//     simply skip sending an RTP packet.
//
// For zero-allocation packing in high-throughput loops, use PackInto.
func Pack(frames [][]byte) ([]byte, error) {
	if len(frames) == 0 {
		return nil, ErrEmptyPayload
	}
	total := len(frames) * FrameBytes
	if len(frames[0]) == SIDBytes {
		total = SIDBytes
	}
	out := make([]byte, total)
	n, err := PackInto(out, frames)
	if err != nil {
		return nil, err
	}
	return out[:n], nil
}

// Unpack parses a G.729 RTP payload into individual frame byte slices.
//
// Valid payload lengths:
//   - 0 bytes: suppressed DTX frame — returns one nil slice (caller should call
//     Decoder.Decode with nil to trigger PLC / comfort noise).
//   - 2 bytes: SID frame — returns one 2-byte slice.
//   - n×10 bytes (n ≥ 1): n speech frames — returns n 10-byte slices.
//
// Returns ErrInvalidPayload for any other length.
func Unpack(payload []byte) ([][]byte, PayloadInfo, error) {
	switch {
	case len(payload) == 0:
		return [][]byte{nil}, PayloadInfo{Type: FrameSuppressed, DurationMs: FrameDurationMs}, nil

	case len(payload) == SIDBytes:
		frame := make([]byte, SIDBytes)
		copy(frame, payload)
		return [][]byte{frame}, PayloadInfo{Type: FrameSID, DurationMs: FrameDurationMs}, nil

	case len(payload)%FrameBytes == 0:
		n := len(payload) / FrameBytes
		frames := make([][]byte, n)
		for i := range frames {
			frame := make([]byte, FrameBytes)
			copy(frame, payload[i*FrameBytes:(i+1)*FrameBytes])
			frames[i] = frame
		}
		return frames, PayloadInfo{
			Type:       FrameSpeech,
			NumFrames:  n,
			DurationMs: n * FrameDurationMs,
		}, nil

	default:
		return nil, PayloadInfo{}, fmt.Errorf("%w: length %d is not 0, 2, or a multiple of 10",
			ErrInvalidPayload, len(payload))
	}
}

// UnpackInto extracts G.729 frames from payload into dst without heap allocation.
// dst must have length >= the number of frames contained in payload:
//   - 0 bytes (suppressed): requires len(dst) >= 1 (dst[0] set to nil)
//   - 2 bytes (SID):        requires len(dst) >= 1
//   - n×10 bytes (speech):  requires len(dst) >= n
//
// For each frame i, if dst[i] has capacity >= frameSize, payload bytes are copied
// into dst[i][:frameSize]. Otherwise, dst[i] is set directly to payload's sub-slice.
//
// Returns the number of frames populated in dst, PayloadInfo, and an error if dst
// is too short or payload length is invalid.
func UnpackInto(dst [][]byte, payload []byte) (int, PayloadInfo, error) {
	switch {
	case len(payload) == 0:
		if len(dst) < 1 {
			return 0, PayloadInfo{}, fmt.Errorf("%w: dst length %d < 1", ErrBufferTooSmall, len(dst))
		}
		dst[0] = nil
		return 1, PayloadInfo{Type: FrameSuppressed, DurationMs: FrameDurationMs}, nil

	case len(payload) == SIDBytes:
		if len(dst) < 1 {
			return 0, PayloadInfo{}, fmt.Errorf("%w: dst length %d < 1", ErrBufferTooSmall, len(dst))
		}
		if cap(dst[0]) >= SIDBytes {
			dst[0] = dst[0][:SIDBytes]
			copy(dst[0], payload)
		} else {
			dst[0] = payload[:SIDBytes]
		}
		return 1, PayloadInfo{Type: FrameSID, DurationMs: FrameDurationMs}, nil

	case len(payload)%FrameBytes == 0:
		n := len(payload) / FrameBytes
		if len(dst) < n {
			return 0, PayloadInfo{}, fmt.Errorf("%w: dst length %d < %d", ErrBufferTooSmall, len(dst), n)
		}
		for i := 0; i < n; i++ {
			sub := payload[i*FrameBytes : (i+1)*FrameBytes]
			if cap(dst[i]) >= FrameBytes {
				dst[i] = dst[i][:FrameBytes]
				copy(dst[i], sub)
			} else {
				dst[i] = sub
			}
		}
		return n, PayloadInfo{
			Type:       FrameSpeech,
			NumFrames:  n,
			DurationMs: n * FrameDurationMs,
		}, nil

	default:
		return 0, PayloadInfo{}, fmt.Errorf("%w: length %d is not 0, 2, or a multiple of 10",
			ErrInvalidPayload, len(payload))
	}
}

// TimestampForFrame returns the RTP timestamp for the n-th frame (0-indexed)
// in a stream, given a base timestamp and the clock rate of 8000 Hz.
//
//	ts := TimestampForFrame(baseTS, frameIndex)
func TimestampForFrame(baseTimestamp uint32, frameIndex int) uint32 {
	return baseTimestamp + uint32(frameIndex)*TimestampIncrement
}

// FrameCount returns the number of G.729 speech frames that fit in a payload
// of the given length, or 0 if the length represents a SID or suppressed frame.
func FrameCount(payloadLen int) int {
	if payloadLen%FrameBytes == 0 {
		return payloadLen / FrameBytes
	}
	return 0
}

// PacketizationInterval returns the packetization interval in milliseconds
// for a given number of frames per RTP packet.
func PacketizationInterval(framesPerPacket int) int {
	return framesPerPacket * FrameDurationMs
}
