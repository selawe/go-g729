package g729

import (
	"fmt"
	"sync"
	"testing"
	"unsafe"
)

// frameDuration is the duration of one G.729 frame in nanoseconds (10 ms).
const frameDuration = 10_000_000

// BenchmarkRTF measures encoder Real-Time Factor.
// RTF = processing_time / audio_duration. Must be well below 1.0 for real-time use.
// Target: RTF < 0.01 (100× headroom).
func BenchmarkRTF(b *testing.B) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	input := generateSine(1000.0, 80, 8000.0)
	dst := make([]byte, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = enc.Encode(dst, input)
	}

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	rtf := nsPerOp / frameDuration
	b.ReportMetric(rtf, "RTF")
	b.ReportMetric(nsPerOp/1000, "µs/frame")
}

// BenchmarkRTFDecode measures decoder Real-Time Factor.
func BenchmarkRTFDecode(b *testing.B) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	input := generateSine(1000.0, 80, 8000.0)
	bitstream := make([]byte, 10)
	_, _, _ = enc.Encode(bitstream, input)

	dec := NewDecoder()
	dst := make([]int16, 80)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dec.Decode(dst, bitstream)
	}

	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	rtf := nsPerOp / frameDuration
	b.ReportMetric(rtf, "RTF")
	b.ReportMetric(nsPerOp/1000, "µs/frame")
}

// BenchmarkReset measures encoder Reset() overhead.
// Reset() must be O(1) — just zeroing fixed-size struct fields.
func BenchmarkReset(b *testing.B) {
	enc := NewEncoder(DefaultConfig())
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		enc.Reset()
	}
}

// BenchmarkDecoderReset measures decoder Reset() overhead.
func BenchmarkDecoderReset(b *testing.B) {
	dec := NewDecoder()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dec.Reset()
	}
}

// BenchmarkConcurrentEncode measures encoding throughput with N goroutines,
// each with their own Encoder instance. Throughput should scale linearly
// since there is no shared mutable state between instances.
func BenchmarkConcurrentEncode(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 16} {
		b.Run(fmt.Sprintf("goroutines=%d", n), func(b *testing.B) {
			b.SetParallelism(n)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				// Each goroutine gets its own Encoder instance — no shared state.
				enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
				dst := make([]byte, 10)
				inp := generateSine(1000.0, 80, 8000.0)
				for pb.Next() {
					_, _, _ = enc.Encode(dst, inp)
				}
			})
		})
	}
}

// BenchmarkConcurrentDecode measures decoding throughput with N goroutines.
func BenchmarkConcurrentDecode(b *testing.B) {
	// Pre-encode one frame to use as input for all decoders.
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	inp := generateSine(1000.0, 80, 8000.0)
	bitstream := make([]byte, 10)
	_, _, _ = enc.Encode(bitstream, inp)

	for _, n := range []int{1, 2, 4, 8, 16} {
		bs := make([]byte, 10)
		copy(bs, bitstream)
		b.Run(fmt.Sprintf("goroutines=%d", n), func(b *testing.B) {
			b.SetParallelism(n)
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				dec := NewDecoder()
				dst := make([]int16, 80)
				for pb.Next() {
					_ = dec.Decode(dst, bs)
				}
			})
		})
	}
}

// BenchmarkNewEncoder measures the cost of creating a new Encoder instance.
// NewEncoder should do exactly one allocation (the encoder struct).
func BenchmarkNewEncoder(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		enc := NewEncoder(DefaultConfig())
		_ = enc
	}
}

// BenchmarkNewDecoder measures the cost of creating a new Decoder instance.
func BenchmarkNewDecoder(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dec := NewDecoder()
		_ = dec
	}
}

// BenchmarkSustainedEncoding benchmarks sustained encoding across 100 varied frames
// to capture realistic pipeline behavior (pitch adaptation, filter memory effects).
func BenchmarkSustainedEncoding(b *testing.B) {
	// Pre-generate 100 frames of varied speech-like signal.
	const numFrames = 100
	frames := make([][]int16, numFrames)
	for f := range frames {
		freq := 80.0 + float64(f)*20.0 // sweeping 80–2080 Hz
		frames[f] = generateSine(freq, 80, 8000.0)
	}

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	dst := make([]byte, 10)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = enc.Encode(dst, frames[i%numFrames])
	}
}

// BenchmarkSustainedDecoding benchmarks sustained decoding across 100 pre-encoded frames.
func BenchmarkSustainedDecoding(b *testing.B) {
	const numFrames = 100
	frames := make([][]int16, numFrames)
	for f := range frames {
		freq := 80.0 + float64(f)*20.0
		frames[f] = generateSine(freq, 80, 8000.0)
	}

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	bitstreams := make([][]byte, numFrames)
	for f := range bitstreams {
		bs := make([]byte, 10)
		_, _, _ = enc.Encode(bs, frames[f])
		bitstreams[f] = bs
	}

	dec := NewDecoder()
	dst := make([]int16, 80)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = dec.Decode(dst, bitstreams[i%numFrames])
	}
}

// ----------------------------------------------------------------------------
// Struct size and memory footprint assertions (run as tests, not benchmarks)
// ----------------------------------------------------------------------------

// TestEncoderStructSize asserts that the encoder struct fits within L1 cache
// budget (< 32 KB). Larger structs cause cache pressure in hot encoding loops.
func TestEncoderStructSize(t *testing.T) {
	enc := NewEncoder(DefaultConfig()).(*encoder)
	size := unsafe.Sizeof(*enc)
	t.Logf("encoder struct size: %d bytes (%.1f KB)", size, float64(size)/1024)
	const maxBytes = 32 * 1024
	if size > maxBytes {
		t.Errorf("encoder struct too large: %d bytes (limit %d bytes / 32 KB)", size, maxBytes)
	}
}

// TestDecoderStructSize asserts decoder struct size.
func TestDecoderStructSize(t *testing.T) {
	dec := NewDecoder().(*decoder)
	size := unsafe.Sizeof(*dec)
	t.Logf("decoder struct size: %d bytes (%.1f KB)", size, float64(size)/1024)
	const maxBytes = 32 * 1024
	if size > maxBytes {
		t.Errorf("decoder struct too large: %d bytes (limit %d bytes / 32 KB)", size, maxBytes)
	}
}

// TestZeroAllocHotPath asserts that Encode and Decode make zero heap
// allocations per call (critical for real-time audio processing).
func TestZeroAllocHotPath(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	dec := NewDecoder()

	frame := generateSine(440.0, 80, 8000.0)
	dst := make([]byte, 10)
	out := make([]int16, 80)

	// Warm up (first few frames establish filter memories)
	for i := 0; i < 10; i++ {
		_, _, _ = enc.Encode(dst, frame)
		_ = dec.Decode(out, dst)
	}

	// Measure allocations
	var encAllocs, decAllocs int64

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		enc2 := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		d2 := make([]byte, 10)
		for i := 0; i < b.N; i++ {
			_, _, _ = enc2.Encode(d2, frame)
		}
	})
	encAllocs = result.AllocsPerOp()

	result = testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		dec2 := NewDecoder()
		o2 := make([]int16, 80)
		for i := 0; i < b.N; i++ {
			_ = dec2.Decode(o2, dst)
		}
	})
	decAllocs = result.AllocsPerOp()

	if encAllocs != 0 {
		t.Errorf("Encode() has %d allocs/op, want 0", encAllocs)
	}
	if decAllocs != 0 {
		t.Errorf("Decode() has %d allocs/op, want 0", decAllocs)
	}
}

// TestRTFMeetsTarget verifies that single-goroutine encode+decode stays well
// below real-time (RTF < 0.1 = 10× safety margin as a hard test gate).
func TestRTFMeetsTarget(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	dec := NewDecoder()

	frame := generateSine(440.0, 80, 8000.0)
	dst := make([]byte, 10)
	out := make([]int16, 80)

	// Warm up
	for i := 0; i < 5; i++ {
		_, _, _ = enc.Encode(dst, frame)
	}

	// Measure 1000 frame encode+decode round-trips
	const numFrames = 1000
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _, _ = enc.Encode(dst, frame)
			_ = dec.Decode(out, dst)
		}
	})

	nsPerOp := float64(result.NsPerOp())
	rtf := nsPerOp / frameDuration
	t.Logf("Round-trip (encode+decode): %.1f µs/frame, RTF=%.4f", nsPerOp/1000, rtf)

	// Hard gate: must be at least 10× real-time
	if rtf >= 0.1 {
		t.Errorf("RTF %.4f >= 0.1 (round-trip too slow for real-time use)", rtf)
	}
}

// TestConcurrentEncoderIsolation verifies that N concurrent encoders produce
// identical output to sequential encoders — no shared state contamination.
func TestConcurrentEncoderIsolation(t *testing.T) {
	const numStreams = 8
	const numFrames = 20

	frames := make([][]int16, numFrames)
	for f := range frames {
		frames[f] = generateSine(200.0+float64(f)*100, 80, 8000.0)
	}

	// Sequential reference: one encoder, one stream
	refOut := make([][]byte, numFrames)
	{
		enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
		for f := range frames {
			bs := make([]byte, 10)
			_, _, _ = enc.Encode(bs, frames[f])
			refOut[f] = bs
		}
	}

	// Concurrent: numStreams goroutines, each with its own encoder and identical input
	type result struct {
		stream int
		out    [][]byte
	}
	ch := make(chan result, numStreams)
	var wg sync.WaitGroup

	for s := 0; s < numStreams; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
			out := make([][]byte, numFrames)
			for f := range frames {
				bs := make([]byte, 10)
				_, _, _ = enc.Encode(bs, frames[f])
				out[f] = bs
			}
			ch <- result{s, out}
		}(s)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for res := range ch {
		for f := range refOut {
			for b := range refOut[f] {
				if res.out[f][b] != refOut[f][b] {
					t.Errorf("stream %d frame %d byte %d: got %02x, want %02x",
						res.stream, f, b, res.out[f][b], refOut[f][b])
				}
			}
		}
	}
}

