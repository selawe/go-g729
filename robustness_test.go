package g729

import (
	"bytes"
	"math/rand"
	"testing"
)

// TestEncoder_ExtremeInputRegression verifies that extreme PCM boundary values
// never cause panics or errors across repeated frames on the same encoder instance.
// Covers: all-min (-32768), all-max (+32767), Nyquist rail-to-rail alternating.
func TestEncoder_ExtremeInputRegression(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	var dst [10]byte
	var pcm [80]int16

	for rep := 0; rep < 20; rep++ {
		for i := range pcm {
			pcm[i] = -32768
		}
		if _, _, err := enc.Encode(dst[:], pcm[:]); err != nil {
			t.Fatalf("rep %d all-min: %v", rep, err)
		}

		for i := range pcm {
			pcm[i] = 32767
		}
		if _, _, err := enc.Encode(dst[:], pcm[:]); err != nil {
			t.Fatalf("rep %d all-max: %v", rep, err)
		}

		for i := range pcm {
			if i%2 == 0 {
				pcm[i] = -32768
			} else {
				pcm[i] = 32767
			}
		}
		if _, _, err := enc.Encode(dst[:], pcm[:]); err != nil {
			t.Fatalf("rep %d nyquist rail-to-rail: %v", rep, err)
		}
	}
}

// TestDecoder_RepeatedPathologicalBitstreams verifies that running 50 consecutive
// frames of the same extreme bit pattern on a single decoder instance never panics
// or errors. Tests state accumulation from repeated extreme inputs, not just a
// single frame in isolation.
func TestDecoder_RepeatedPathologicalBitstreams(t *testing.T) {
	patterns := []struct {
		name string
		b    byte
	}{
		{"all-zero", 0x00},
		{"all-ones", 0xFF},
		{"0xAA", 0xAA},
		{"0x55", 0x55},
		{"0x01", 0x01},
		{"0x80", 0x80},
	}

	for _, p := range patterns {
		t.Run(p.name, func(t *testing.T) {
			dec := NewDecoder()
			var out [80]int16
			pat := bytes.Repeat([]byte{p.b}, 10)
			for rep := 0; rep < 50; rep++ {
				if err := dec.Decode(out[:], pat); err != nil {
					t.Fatalf("rep %d: %v", rep, err)
				}
			}
		})
	}
}

// TestNilEncoderReturnsError verifies that all public Encoder methods return
// ErrNilEncoder (or zero values) when called on a nil *Encoder, without panicking.
func TestNilEncoderReturnsError(t *testing.T) {
	var e *Encoder

	var dst [10]byte
	var src [80]int16
	if _, _, err := e.Encode(dst[:], src[:]); err != ErrNilEncoder {
		t.Errorf("Encode on nil Encoder: want ErrNilEncoder, got %v", err)
	}
	if _, _, err := e.EncodeBatch(dst[:], src[:]); err != ErrNilEncoder {
		t.Errorf("EncodeBatch on nil Encoder: want ErrNilEncoder, got %v", err)
	}
	ft := make([]FrameType, 1)
	if _, _, err := e.EncodeBatchInto(dst[:], src[:], ft); err != ErrNilEncoder {
		t.Errorf("EncodeBatchInto on nil Encoder: want ErrNilEncoder, got %v", err)
	}
	if got := e.Stats(); got != (EncoderStats{}) {
		t.Errorf("Stats on nil Encoder: want zero, got %+v", got)
	}
	e.Reset() // must not panic
}

// TestNilDecoderReturnsError verifies that all public Decoder methods return
// ErrNilDecoder (or zero values) when called on a nil *Decoder, without panicking.
func TestNilDecoderReturnsError(t *testing.T) {
	var d *Decoder

	var out [80]int16
	var frame [10]byte
	if err := d.Decode(out[:], frame[:]); err != ErrNilDecoder {
		t.Errorf("Decode on nil Decoder: want ErrNilDecoder, got %v", err)
	}
	if err := d.DecodeBatch(make([]int16, 80), [][]byte{frame[:]}); err != ErrNilDecoder {
		t.Errorf("DecodeBatch on nil Decoder: want ErrNilDecoder, got %v", err)
	}
	if got := d.Stats(); got != (DecoderStats{}) {
		t.Errorf("Stats on nil Decoder: want zero, got %+v", got)
	}
	d.Reset() // must not panic
}

// TestDecoder_RandomBitstreamWithInterleavedPLC verifies decoder robustness
// under the most realistic packet-loss scenario: a stream of corrupt/random
// 10-byte frames with PLC injected every 20 frames.
//
// This exercises the interaction between corrupted adaptive-excitation state
// and PLC recovery, which single-frame fuzz tests do not cover.
func TestDecoder_RandomBitstreamWithInterleavedPLC(t *testing.T) {
	dec := NewDecoder()
	rng := rand.New(rand.NewSource(1337))
	var bits [10]byte
	var out [80]int16

	for i := 0; i < 5000; i++ {
		rng.Read(bits[:])
		if err := dec.Decode(out[:], bits[:]); err != nil {
			t.Fatalf("frame %d random: %v", i, err)
		}
		if i%20 == 0 {
			if err := dec.Decode(out[:], nil); err != nil {
				t.Fatalf("frame %d PLC: %v", i, err)
			}
		}
	}
}
