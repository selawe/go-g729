package g729_test

import (
	"bytes"
	"testing"

	g729 "github.com/selawe/go-g729"
)

func TestEncoderPool(t *testing.T) {
	cfg := g729.Config{Variant: g729.VariantG729A, EnableVAD: false}
	pool := g729.NewEncoderPool(cfg)

	// Step 1: Get encoder and encode some dummy data
	enc1 := pool.Get()
	var dst [10]byte
	var pcm [80]int16
	for i := range pcm {
		pcm[i] = int16(i * 100)
	}
	_, _, err := enc1.Encode(dst[:], pcm[:])
	if err != nil {
		t.Fatalf("enc1 encode error: %v", err)
	}

	// Step 2: Return to pool
	pool.Put(enc1)

	// Step 3: Get encoder again (recycled instance)
	enc2 := pool.Get()
	if enc2 == nil {
		t.Fatal("pool returned nil encoder")
	}

	// Step 4: Verify enc2 behaves identically to a freshly allocated encoder
	freshEnc := g729.NewEncoder(cfg)

	var dstRecycled [10]byte
	var dstFresh [10]byte
	_, _, errRecycled := enc2.Encode(dstRecycled[:], pcm[:])
	_, _, errFresh := freshEnc.Encode(dstFresh[:], pcm[:])

	if errRecycled != nil || errFresh != nil {
		t.Fatalf("encode error: recycled=%v fresh=%v", errRecycled, errFresh)
	}

	if !bytes.Equal(dstRecycled[:], dstFresh[:]) {
		t.Errorf("recycled encoder produced divergent bitstream: got %x, want %x", dstRecycled, dstFresh)
	}

	// Step 5: Test nil safety
	pool.Put(nil)
}

func TestDecoderPool(t *testing.T) {
	pool := g729.NewDecoderPool()

	dec1 := pool.Get()
	var pcm [80]int16
	var frame [10]byte
	// Decode a frame
	_ = dec1.Decode(pcm[:], frame[:])

	// Return to pool
	pool.Put(dec1)

	// Retrieve recycled decoder
	dec2 := pool.Get()
	if dec2 == nil {
		t.Fatal("pool returned nil decoder")
	}

	// Compare with freshly created decoder on PLC decode
	freshDec := g729.NewDecoder()
	var pcmRecycled [80]int16
	var pcmFresh [80]int16

	errRecycled := dec2.Decode(pcmRecycled[:], nil)
	errFresh := freshDec.Decode(pcmFresh[:], nil)

	if errRecycled != nil || errFresh != nil {
		t.Fatalf("PLC decode error: recycled=%v fresh=%v", errRecycled, errFresh)
	}

	for i := 0; i < 80; i++ {
		if pcmRecycled[i] != pcmFresh[i] {
			t.Fatalf("sample %d mismatch: recycled=%d fresh=%d", i, pcmRecycled[i], pcmFresh[i])
		}
	}

	// Test nil safety
	pool.Put(nil)
}

func BenchmarkEncoderPool(b *testing.B) {
	cfg := g729.Config{Variant: g729.VariantG729A, EnableVAD: false}
	pool := g729.NewEncoderPool(cfg)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		enc := pool.Get()
		pool.Put(enc)
	}
}

func BenchmarkDecoderPool(b *testing.B) {
	pool := g729.NewDecoderPool()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dec := pool.Get()
		pool.Put(dec)
	}
}
