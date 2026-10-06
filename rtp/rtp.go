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

// MaxFramesPerPacket is the maximum number of G.729 speech frames accepted per RTP
// packet by Unpack. Payloads exceeding this limit (> 1280 ms of audio) are rejected
// with ErrInvalidPayload to prevent excessive heap allocation on untrusted input.
// Use UnpackInto with a fixed-size destination buffer for tighter per-call bounds.
const MaxFramesPerPacket = 128

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
	// FrameMixed indicates a transition packet containing speech frame(s) followed by 1 SID frame (RFC 3551 §4.5.6).
	FrameMixed
)

func (f FrameType) String() string {
	switch f {
	case FrameSpeech:
		return "speech"
	case FrameSID:
		return "SID"
	case FrameSuppressed:
		return "suppressed"
	case FrameMixed:
		return "mixed"
	default:
		return fmt.Sprintf("FrameType(%d)", int(f))
	}
}

// PayloadInfo describes the content of a parsed RTP payload.
type PayloadInfo struct {
	// Type is the frame type present in the payload.
	Type FrameType
	// NumFrames is the total number of frames bundled in the payload.
	// Always 0 for pure SID and Suppressed.
	NumFrames int
	// DurationMs is the total audio duration covered by this payload in ms.
	DurationMs int
	// HasSID reports whether a 2-byte Annex B SID frame is present in the payload.
	HasSID bool
}

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
// Per RFC 3551 §4.5.6, a packet consists of zero or more 10-byte speech frames,
// optionally followed by at most one 2-byte Annex B SID frame.
// dst must have capacity >= the total packed payload size.
//
// Returns the number of bytes written to dst, or an error if frames are invalid or dst is too small.
func PackInto(dst []byte, frames [][]byte) (int, error) {
	if len(frames) == 0 {
		return 0, ErrEmptyPayload
	}

	total := 0
	hasSID := false
	for i, f := range frames {
		switch len(f) {
		case FrameBytes:
			if hasSID {
				// Per RFC 3551 §4.5.6, SID frame can only be the final frame in a packet
				return 0, fmt.Errorf("%w: speech frame %d follows SID frame", ErrInvalidPayload, i)
			}
			total += FrameBytes
		case SIDBytes:
			if hasSID {
				// At most one SID frame per packet per RFC 3551 §4.5.6
				return 0, fmt.Errorf("%w: multiple SID frames in packet (frame %d)", ErrInvalidPayload, i)
			}
			hasSID = true
			total += SIDBytes
		default:
			return 0, fmt.Errorf("%w: frame %d has %d bytes (want %d for speech or %d for SID)",
				ErrInvalidPayload, i, len(f), FrameBytes, SIDBytes)
		}
	}

	if len(dst) < total {
		return 0, fmt.Errorf("%w: dst length %d < %d", ErrBufferTooSmall, len(dst), total)
	}

	offset := 0
	for _, f := range frames {
		copy(dst[offset:offset+len(f)], f)
		offset += len(f)
	}

	return total, nil
}

// Pack bundles one or more G.729 encoded frames into a single allocated RTP payload.
//
// Per RFC 3551 §4.5.6, a packet may contain:
//   - Speech frames: all must be exactly 10 bytes (payload length = n×10).
//   - SID frame: a single 2-byte comfort noise frame (payload length = 2).
//   - Mixed transition packet: n speech frames followed by 1 SID frame (payload length = n×10 + 2).
//
// Suppressed frames: caller must not call Pack for suppressed frames —
// simply skip sending an RTP packet.
//
// For zero-allocation packing in high-throughput loops, use PackInto.
func Pack(frames [][]byte) ([]byte, error) {
	if len(frames) == 0 {
		return nil, ErrEmptyPayload
	}
	total := 0
	for _, f := range frames {
		total += len(f)
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
// Valid payload lengths per RFC 3551 §4.5.6:
//   - 0 bytes: suppressed DTX frame — returns one nil slice (caller should call
//     Decoder.Decode with nil to trigger PLC / comfort noise).
//   - 2 bytes: SID frame — returns one 2-byte slice.
//   - n×10 bytes (n ≥ 1): n speech frames — returns n 10-byte slices.
//   - n×10 + 2 bytes (n ≥ 1): n speech frames followed by 1 SID frame — returns (n+1) slices.
//
// Returns ErrInvalidPayload for any other length.
func Unpack(payload []byte) ([][]byte, PayloadInfo, error) {
	rem := len(payload) % FrameBytes
	switch {
	case len(payload) == 0:
		return [][]byte{nil}, PayloadInfo{Type: FrameSuppressed, DurationMs: FrameDurationMs}, nil

	case len(payload) == SIDBytes:
		frame := make([]byte, SIDBytes)
		copy(frame, payload)
		return [][]byte{frame}, PayloadInfo{
			Type:       FrameSID,
			NumFrames:  1,
			DurationMs: FrameDurationMs,
			HasSID:     true,
		}, nil

	case rem == 0:
		n := len(payload) / FrameBytes
		if n > MaxFramesPerPacket {
			return nil, PayloadInfo{}, fmt.Errorf("%w: %d frames exceeds MaxFramesPerPacket (%d)",
				ErrInvalidPayload, n, MaxFramesPerPacket)
		}
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

	case rem == SIDBytes && len(payload) > SIDBytes:
		// RFC 3551 §4.5.6 transition packet: N speech frames followed by 1 SID frame
		nSpeech := len(payload) / FrameBytes
		nTotal := nSpeech + 1
		if nTotal > MaxFramesPerPacket {
			return nil, PayloadInfo{}, fmt.Errorf("%w: %d frames exceeds MaxFramesPerPacket (%d)",
				ErrInvalidPayload, nTotal, MaxFramesPerPacket)
		}
		frames := make([][]byte, nTotal)
		for i := range nSpeech {
			frame := make([]byte, FrameBytes)
			copy(frame, payload[i*FrameBytes:(i+1)*FrameBytes])
			frames[i] = frame
		}
		sidFrame := make([]byte, SIDBytes)
		copy(sidFrame, payload[nSpeech*FrameBytes:])
		frames[nSpeech] = sidFrame

		return frames, PayloadInfo{
			Type:       FrameMixed,
			NumFrames:  nTotal,
			DurationMs: nTotal * FrameDurationMs,
			HasSID:     true,
		}, nil

	default:
		return nil, PayloadInfo{}, fmt.Errorf("%w: length %d is not 0, 2, a multiple of 10, or 10N+2",
			ErrInvalidPayload, len(payload))
	}
}

// UnpackInto extracts G.729 frames from payload into dst without heap allocation.
// dst must have length >= the number of frames contained in payload:
//   - 0 bytes (suppressed): requires len(dst) >= 1 (dst[0] set to nil)
//   - 2 bytes (SID):        requires len(dst) >= 1
//   - n×10 bytes (speech):  requires len(dst) >= n
//   - n×10 + 2 bytes:       requires len(dst) >= n+1
//
// Two-mode aliasing behaviour, chosen per-slot by capacity:
//
//   - COPY mode (dst[i] has cap >= FrameBytes): payload is copied into dst[i][:frameSize].
//     dst[i] is fully independent of payload afterwards. Safe to mutate or reuse
//     payload immediately. This is the recommended mode — pre-allocate a fixed
//     [n][10]byte and slice into it (see jitter.Buffer for reference usage).
//
//   - ZERO-COPY mode (dst[i] cap < FrameBytes): dst[i] is set to a sub-slice of
//     payload directly. NO COPY IS PERFORMED. This is dangerous — dst[i]'s
//     backing array IS payload. If the caller later reuses the payload buffer
//     (e.g. for the next network read), dst[i] will silently observe the new
//     bytes. Only use zero-copy mode if payload has a stable lifetime that
//     outlives dst[i] usage.
//
// Returns the number of frames populated in dst, PayloadInfo, and an error if dst
// is too short or payload length is invalid.
func UnpackInto(dst [][]byte, payload []byte) (int, PayloadInfo, error) {
	rem := len(payload) % FrameBytes
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
		return 1, PayloadInfo{Type: FrameSID, NumFrames: 1, DurationMs: FrameDurationMs, HasSID: true}, nil

	case rem == 0:
		n := len(payload) / FrameBytes
		if len(dst) < n {
			return 0, PayloadInfo{}, fmt.Errorf("%w: dst length %d < %d", ErrBufferTooSmall, len(dst), n)
		}
		for i := range n {
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

	case rem == SIDBytes && len(payload) > SIDBytes:
		nSpeech := len(payload) / FrameBytes
		nTotal := nSpeech + 1
		if len(dst) < nTotal {
			return 0, PayloadInfo{}, fmt.Errorf("%w: dst length %d < %d", ErrBufferTooSmall, len(dst), nTotal)
		}
		for i := range nSpeech {
			sub := payload[i*FrameBytes : (i+1)*FrameBytes]
			if cap(dst[i]) >= FrameBytes {
				dst[i] = dst[i][:FrameBytes]
				copy(dst[i], sub)
			} else {
				dst[i] = sub
			}
		}
		subSID := payload[nSpeech*FrameBytes:]
		if cap(dst[nSpeech]) >= SIDBytes {
			dst[nSpeech] = dst[nSpeech][:SIDBytes]
			copy(dst[nSpeech], subSID)
		} else {
			dst[nSpeech] = subSID
		}
		return nTotal, PayloadInfo{
			Type:       FrameMixed,
			NumFrames:  nTotal,
			DurationMs: nTotal * FrameDurationMs,
			HasSID:     true,
		}, nil

	default:
		return 0, PayloadInfo{}, fmt.Errorf("%w: length %d is not 0, 2, a multiple of 10, or 10N+2",
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

// FrameCount returns the total number of G.729 frames (speech and SID) that fit in a payload
// of the given length, or 0 if the length represents a suppressed frame or invalid payload.
func FrameCount(payloadLen int) int {
	if payloadLen <= 0 {
		return 0
	}
	rem := payloadLen % FrameBytes
	if rem == 0 {
		return payloadLen / FrameBytes
	}
	if rem == SIDBytes {
		return (payloadLen / FrameBytes) + 1
	}
	return 0
}

// PacketizationInterval returns the packetization interval in milliseconds
// for a given number of frames per RTP packet.
func PacketizationInterval(framesPerPacket int) int {
	return framesPerPacket * FrameDurationMs
}
