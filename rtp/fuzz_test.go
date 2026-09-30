package rtp_test

import (
	"testing"

	"github.com/selawe/go-g729/rtp"
)

// FuzzUnpack tests the RTP Unpack function against arbitrary byte inputs.
//
// Invariants:
//   - Must never panic regardless of input.
//   - Valid lengths (0, 2, n×10 where n ≤ MaxFramesPerPacket) must not return an error.
//   - Any other length must return ErrInvalidPayload.
//
// Run: go test -fuzz=FuzzUnpack -fuzztime=2m ./rtp/
func FuzzUnpack(f *testing.F) {
	// Seed: suppressed frame
	f.Add([]byte{})
	// Seed: SID frame
	f.Add([]byte{0x00, 0x00})
	f.Add([]byte{0xFF, 0xFF})
	// Seed: single speech frame (all zeros)
	f.Add(make([]byte, 10))
	// Seed: single speech frame (all ones)
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	// Seed: two frames
	f.Add(make([]byte, 20))
	// Seed: max allowed frames
	f.Add(make([]byte, rtp.MaxFramesPerPacket*rtp.FrameBytes))
	// Seed: one frame over the limit — must be rejected
	f.Add(make([]byte, (rtp.MaxFramesPerPacket+1)*rtp.FrameBytes))
	// Seed: invalid length
	f.Add([]byte{0x01, 0x02, 0x03})

	f.Fuzz(func(t *testing.T, data []byte) {
		frames, info, err := rtp.Unpack(data)

		n := len(data)
		switch {
		case n == 0:
			if err != nil {
				t.Errorf("len=0: expected nil error, got %v", err)
			}
			if len(frames) != 1 || frames[0] != nil {
				t.Errorf("len=0: expected [nil], got %v", frames)
			}
			if info.Type != rtp.FrameSuppressed {
				t.Errorf("len=0: expected FrameSuppressed, got %v", info.Type)
			}

		case n == rtp.SIDBytes:
			if err != nil {
				t.Errorf("len=2: expected nil error, got %v", err)
			}
			if info.Type != rtp.FrameSID {
				t.Errorf("len=2: expected FrameSID, got %v", info.Type)
			}

		case n%rtp.FrameBytes == 0 && n/rtp.FrameBytes <= rtp.MaxFramesPerPacket:
			if err != nil {
				t.Errorf("len=%d (%d frames): expected nil error, got %v", n, n/rtp.FrameBytes, err)
			}
			if info.Type != rtp.FrameSpeech {
				t.Errorf("len=%d: expected FrameSpeech, got %v", n, info.Type)
			}
			want := n / rtp.FrameBytes
			if len(frames) != want {
				t.Errorf("len=%d: expected %d frames, got %d", n, want, len(frames))
			}

		default:
			// Any other length (invalid or over limit) must return an error.
			if err == nil {
				t.Errorf("len=%d: expected error, got nil", n)
			}
		}
	})
}
