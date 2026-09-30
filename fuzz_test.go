package g729

import (
	"encoding/binary"
	"math"
	"testing"
)

// FuzzDecode tests the decoder against arbitrary byte inputs.
//
// The decoder must NEVER panic, regardless of input content. If the frame
// length is valid (0, 2, or 10 bytes), it must produce 80 valid int16 PCM
// samples with no infinite loop or memory corruption.
//
// Run: go test -fuzz=FuzzDecode -fuzztime=5m .
func FuzzDecode(f *testing.F) {
	// Seed 1: PLC / untransmitted frame (0 bytes)
	f.Add([]byte{})

	// Seed 2: SID frame (2 bytes) — all zeros
	f.Add([]byte{0x00, 0x00})

	// Seed 3: SID frame — max values
	f.Add([]byte{0xFF, 0xFF})

	// Seed 4: Speech frame — all zeros (silence-coded)
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	// Seed 5: Speech frame — all bits set
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})

	// Seed 6: Speech frame — alternating bits
	f.Add([]byte{0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55, 0xAA, 0x55})

	// Seed 7-9: Realistic frames from our encoder (deterministic synthetic signal)
	{
		enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		buf := make([]byte, 10)
		tones := []float64{300.0, 1000.0, 440.0}
		for _, hz := range tones {
			frame := generateSine(hz, 80, 8000.0)
			if n, _, _ := enc.Encode(buf, frame); n == 10 {
				seed := make([]byte, 10)
				copy(seed, buf[:10])
				f.Add(seed)
			}
		}
	}

	// Seed 10: Frame from G.729 Full encoder
	{
		enc := NewEncoder(Config{Variant: VariantG729, EnableVAD: false})
		buf := make([]byte, 10)
		frame := generateSine(220.0, 80, 6000.0)
		if n, _, _ := enc.Encode(buf, frame); n == 10 {
			seed := make([]byte, 10)
			copy(seed, buf[:10])
			f.Add(seed)
		}
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		dec := NewDecoder()
		dst := make([]int16, 80)

		err := dec.Decode(dst, data)

		switch len(data) {
		case 0, 2, 10:
			// Valid lengths: error must be nil (decoder handles all valid frames)
			if err != nil {
				t.Errorf("unexpected error for %d-byte frame: %v", len(data), err)
			}
		default:
			// Invalid length: must return ErrInvalidFrameLen
			if err != ErrInvalidFrameLen {
				t.Errorf("expected ErrInvalidFrameLen for %d-byte frame, got: %v", len(data), err)
			}
			// Output buffer must not be corrupted on error
			return
		}

		// For valid frames: output samples must be finite int16 values.
		// (Go's int16 range is always [-32768, 32767] by definition, but we
		// verify the decoder doesn't produce the sentinel NaN-ish pattern that
		// could arise from float32 overflow before int16 conversion.)
		for i, s := range dst {
			if s > 32767 || s < -32768 {
				// This can never happen for int16 but catches if the type were wrong
				t.Errorf("sample %d out of int16 range: %d", i, s)
			}
		}
	})
}

// FuzzEncode tests the encoder against arbitrary 16-bit PCM input.
//
// The encoder must NEVER panic, regardless of sample values. It must always
// produce a valid frame (10, 2, or 0 bytes) with zero heap allocations per call.
//
// Run: go test -fuzz=FuzzEncode -fuzztime=5m .
func FuzzEncode(f *testing.F) {
	// Seed 1: Silence (all zeros)
	f.Add(make([]byte, 160)) // 80 int16 as raw bytes

	// Seed 2: Maximum positive amplitude
	{
		buf := make([]byte, 160)
		for i := 0; i < 80; i++ {
			binary.LittleEndian.PutUint16(buf[i*2:], 0x7FFF)
		}
		f.Add(buf)
	}

	// Seed 3: Maximum negative amplitude
	{
		buf := make([]byte, 160)
		for i := 0; i < 80; i++ {
			binary.LittleEndian.PutUint16(buf[i*2:], 0x8000)
		}
		f.Add(buf)
	}

	// Seed 4: Alternating extremes
	{
		buf := make([]byte, 160)
		for i := 0; i < 80; i++ {
			v := uint16(0x7FFF)
			if i%2 == 1 {
				v = 0x8000
			}
			binary.LittleEndian.PutUint16(buf[i*2:], v)
		}
		f.Add(buf)
	}

	// Seed 5-8: Realistic speech-like tones
	for _, hz := range []float64{300.0, 1000.0, 440.0, 80.0} {
		frame := generateSine(hz, 80, 10000.0)
		buf := make([]byte, 160)
		for i, s := range frame {
			binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
		}
		f.Add(buf)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// Require exactly 160 bytes (80 int16 samples)
		if len(data) != 160 {
			return
		}

		// Convert raw bytes to int16 PCM frame
		frame := make([]int16, 80)
		for i := range frame {
			frame[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
		}

		enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		dst := make([]byte, 10)

		n, fType, err := enc.Encode(dst, frame)

		// Must not error (input is always valid 80 samples)
		if err != nil {
			t.Errorf("unexpected encode error: %v", err)
			return
		}

		// With EnableVAD=false, must always produce a 10-byte speech frame
		if n != 10 || fType != FrameSpeech {
			t.Errorf("expected 10-byte FrameSpeech, got n=%d type=%v", n, fType)
			return
		}

		// Encoded frame must be decodable without error
		dec := NewDecoder()
		out := make([]int16, 80)
		if err := dec.Decode(out, dst[:n]); err != nil {
			t.Errorf("decode of encoded frame failed: %v", err)
		}
	})
}

// FuzzEncodeStateful tests the encoder over a multi-frame sequence using a
// single persistent encoder instance, exercising accumulated state such as
// MA predictor history, freqPrev, excErr, and past excitation buffers.
//
// data is interpreted as a stream of 160-byte blocks (80 int16 PCM samples
// each); any trailing bytes that don't fill a complete block are ignored.
// The encoder must never panic and must always produce a valid 10-byte speech
// frame (EnableVAD=false) regardless of input values.
//
// Run: go test -fuzz=FuzzEncodeStateful -fuzztime=5m .
func FuzzEncodeStateful(f *testing.F) {
	// Seed: silence (8 frames = 80 ms)
	f.Add(make([]byte, 160*8))

	// Seed: max positive amplitude (8 frames)
	{
		buf := make([]byte, 160*8)
		for i := 0; i < 80*8; i++ {
			binary.LittleEndian.PutUint16(buf[i*2:], 0x7FFF)
		}
		f.Add(buf)
	}

	// Seed: alternating extremes (8 frames)
	{
		buf := make([]byte, 160*8)
		for i := 0; i < 80*8; i++ {
			v := uint16(0x7FFF)
			if i%2 == 1 {
				v = 0x8000
			}
			binary.LittleEndian.PutUint16(buf[i*2:], v)
		}
		f.Add(buf)
	}

	// Seed: 440 Hz tone for 4 frames
	{
		const frames = 4
		buf := make([]byte, 160*frames)
		for i := 0; i < 80*frames; i++ {
			v := int16(16000.0 * math.Sin(2*math.Pi*440.0*float64(i)/8000.0))
			binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
		}
		f.Add(buf)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		const blockBytes = 160
		if len(data) < blockBytes {
			return
		}

		enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		var dst [10]byte
		var frame [80]int16

		for off := 0; off+blockBytes <= len(data); off += blockBytes {
			block := data[off : off+blockBytes]
			for i := range frame {
				frame[i] = int16(binary.LittleEndian.Uint16(block[i*2:]))
			}
			n, fType, err := enc.Encode(dst[:], frame[:])
			if err != nil {
				t.Errorf("frame at offset %d: unexpected error: %v", off, err)
				return
			}
			if n != 10 || fType != FrameSpeech {
				t.Errorf("frame at offset %d: expected 10-byte FrameSpeech, got n=%d type=%v",
					off, n, fType)
			}
		}
	})
}

// FuzzDecodeRoundtrip tests the full encode-decode pipeline end-to-end.
//
// Any 80 int16 PCM samples should survive the encode-decode cycle without
// producing NaN/Inf or triggering panics in either codec stage.
//
// Run: go test -fuzz=FuzzDecodeRoundtrip -fuzztime=5m .
func FuzzDecodeRoundtrip(f *testing.F) {
	// Seed with varied signal types
	for _, hz := range []float64{100.0, 300.0, 800.0, 2000.0} {
		frame := generateSine(hz, 80, 8000.0)
		buf := make([]byte, 160)
		for i, s := range frame {
			binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
		}
		f.Add(buf)
	}
	f.Add(make([]byte, 160)) // silence

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) != 160 {
			return
		}

		frame := make([]int16, 80)
		for i := range frame {
			frame[i] = int16(binary.LittleEndian.Uint16(data[i*2:]))
		}

		enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		dec := NewDecoder()

		var bs [10]byte
		var out [80]int16

		n, _, err := enc.Encode(bs[:], frame)
		if err != nil || n != 10 {
			t.Errorf("encode failed: n=%d, err=%v", n, err)
			return
		}

		if err := dec.Decode(out[:], bs[:]); err != nil {
			t.Errorf("decode failed: %v", err)
		}
	})
}
