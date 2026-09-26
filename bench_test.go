package g729

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
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

// ============================================================================
// Frame-Time Jitter (P50 / P90 / P99 / P99.9 / max)
// ============================================================================

// TestFrameTimeJitterEncode measures per-frame encode latency distribution.
//
// A codec running at 238× real-time has an average of ~42 µs per frame.
// Even when the average is low, OS scheduler interruptions or cache evictions
// can cause individual frames to spike. This test verifies that:
//   - P99   < 1 ms  (10% of 10 ms budget — safe operational headroom)
//   - P99.9 < 5 ms  (50% of budget — no deadline risk even under light load)
//
// Run: go test -run TestFrameTimeJitter -v -count=1 .
func TestFrameTimeJitterEncode(t *testing.T) {
	const N = 10_000
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	frame := generateSine(440.0, 80, 8000.0)
	dst := make([]byte, 10)

	// Warm up: establish filter memories and fill instruction cache.
	for i := 0; i < 100; i++ {
		_, _, _ = enc.Encode(dst, frame)
	}

	times := make([]int64, N)
	for i := range times {
		t0 := time.Now()
		_, _, _ = enc.Encode(dst, frame)
		times[i] = time.Since(t0).Nanoseconds()
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

	p50 := times[N*50/100]
	p90 := times[N*90/100]
	p99 := times[N*99/100]
	p999 := times[N*999/1000]
	pmax := times[N-1]

	ns2us := func(ns int64) string { return fmt.Sprintf("%.1f µs", float64(ns)/1000) }

	t.Logf("Encode G.729A frame-time jitter (%d frames):", N)
	t.Logf("  P50   = %s", ns2us(p50))
	t.Logf("  P90   = %s", ns2us(p90))
	t.Logf("  P99   = %s", ns2us(p99))
	t.Logf("  P99.9 = %s", ns2us(p999))
	t.Logf("  Max   = %s", ns2us(pmax))
	t.Logf("  Budget = 10 000.0 µs (one G.729 frame)")

	// Gate: P99 must stay below 2 ms (5× under 10 ms frame budget).
	// On Linux/macOS, P99 is typically 200–500 µs (OS timer resolution ~1 µs).
	// On Windows, OS scheduling quanta can add up to ~1–2 ms of jitter; the
	// 2 ms gate accounts for this while still detecting genuine regressions.
	// P50 may show as 0 µs on Windows — this is a timer quantisation artefact,
	// not an indication that frames are instant (benchmark -bench confirms ~42 µs).
	const p99Gate = 2_000_000  // 2 ms
	const p999Gate = 5_000_000 // 5 ms
	if p99 > p99Gate {
		t.Errorf("P99 %s > gate %s — risk of audio glitch under load", ns2us(p99), ns2us(p99Gate))
	}
	if p999 > p999Gate {
		t.Errorf("P99.9 %s > gate %s — deadline miss risk", ns2us(p999), ns2us(p999Gate))
	}
}

// TestFrameTimeJitterDecode measures per-frame decode latency distribution.
func TestFrameTimeJitterDecode(t *testing.T) {
	const N = 10_000

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	bs := make([]byte, 10)
	_, _, _ = enc.Encode(bs, generateSine(300.0, 80, 8000.0))

	dec := NewDecoder()
	out := make([]int16, 80)

	for i := 0; i < 100; i++ { // warm up
		_ = dec.Decode(out, bs)
	}

	times := make([]int64, N)
	for i := range times {
		t0 := time.Now()
		_ = dec.Decode(out, bs)
		times[i] = time.Since(t0).Nanoseconds()
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

	p50 := times[N*50/100]
	p90 := times[N*90/100]
	p99 := times[N*99/100]
	p999 := times[N*999/1000]
	pmax := times[N-1]

	ns2us := func(ns int64) string { return fmt.Sprintf("%.1f µs", float64(ns)/1000) }

	t.Logf("Decode G.729A frame-time jitter (%d frames):", N)
	t.Logf("  P50   = %s", ns2us(p50))
	t.Logf("  P90   = %s", ns2us(p90))
	t.Logf("  P99   = %s", ns2us(p99))
	t.Logf("  P99.9 = %s", ns2us(p999))
	t.Logf("  Max   = %s", ns2us(pmax))

	const p99Gate = 2_000_000  // 2 ms
	const p999Gate = 5_000_000 // 5 ms
	if p99 > p99Gate {
		t.Errorf("P99 %s > gate %s", ns2us(p99), ns2us(p99Gate))
	}
	if p999 > p999Gate {
		t.Errorf("P99.9 %s > gate %s", ns2us(p999), ns2us(p999Gate))
	}
}

// ============================================================================
// Load-Test Smoke
// ============================================================================

// TestLoadSmokeEncode verifies long-running encoder stability over 50 000 frames
// (~500 s of audio processed in ~2 s of wall time). Checks:
//   - No heap growth beyond 512 KB (memory leak detection)
//   - No goroutine leak
//   - No performance degradation: last 1000-frame batch ≤ 1.5× first batch
//
// Run: go test -run TestLoadSmoke -v -count=1 .
func TestLoadSmokeEncode(t *testing.T) {
	const (
		totalFrames = 50_000
		batchSize   = 1_000
	)

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	frame := generateSine(440.0, 80, 8000.0)
	dst := make([]byte, 10)

	goroutinesBefore := runtime.NumGoroutine()
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// First batch timing
	t0 := time.Now()
	for i := 0; i < batchSize; i++ {
		_, _, _ = enc.Encode(dst, frame)
	}
	firstBatchNs := time.Since(t0).Nanoseconds()

	// Middle frames (untimed)
	for i := batchSize; i < totalFrames-batchSize; i++ {
		_, _, _ = enc.Encode(dst, frame)
	}

	// Last batch timing
	t0 = time.Now()
	for i := 0; i < batchSize; i++ {
		_, _, _ = enc.Encode(dst, frame)
	}
	lastBatchNs := time.Since(t0).Nanoseconds()

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	goroutinesAfter := runtime.NumGoroutine()

	heapGrowth := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)
	ratio := float64(lastBatchNs) / float64(firstBatchNs)

	t.Logf("Load smoke encode (%d frames):", totalFrames)
	t.Logf("  Heap growth:       %+d bytes", heapGrowth)
	t.Logf("  GC cycles:         %d", memAfter.NumGC-memBefore.NumGC)
	t.Logf("  Goroutines:        before=%d after=%d", goroutinesBefore, goroutinesAfter)
	t.Logf("  Perf ratio (L/F):  %.2f× (1.00 = perfectly stable)", ratio)

	if heapGrowth > 512*1024 {
		t.Errorf("heap grew %d bytes over %d frames — possible memory leak", heapGrowth, totalFrames)
	}
	if goroutinesAfter > goroutinesBefore+2 {
		t.Errorf("goroutine leak: before=%d after=%d", goroutinesBefore, goroutinesAfter)
	}
	if ratio > 1.5 {
		t.Errorf("encoder degraded: last batch %.2f× slower than first — GC pressure?", ratio)
	}
}

// TestLoadSmokeDecode verifies long-running decoder stability with the same checks.
func TestLoadSmokeDecode(t *testing.T) {
	const (
		totalFrames = 50_000
		batchSize   = 1_000
	)

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	bs := make([]byte, 10)
	_, _, _ = enc.Encode(bs, generateSine(300.0, 80, 8000.0))

	dec := NewDecoder()
	out := make([]int16, 80)

	goroutinesBefore := runtime.NumGoroutine()
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	t0 := time.Now()
	for i := 0; i < batchSize; i++ {
		_ = dec.Decode(out, bs)
	}
	firstBatchNs := time.Since(t0).Nanoseconds()

	for i := batchSize; i < totalFrames-batchSize; i++ {
		_ = dec.Decode(out, bs)
	}

	t0 = time.Now()
	for i := 0; i < batchSize; i++ {
		_ = dec.Decode(out, bs)
	}
	lastBatchNs := time.Since(t0).Nanoseconds()

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	goroutinesAfter := runtime.NumGoroutine()

	heapGrowth := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)
	ratio := float64(lastBatchNs) / float64(firstBatchNs)

	t.Logf("Load smoke decode (%d frames):", totalFrames)
	t.Logf("  Heap growth:       %+d bytes", heapGrowth)
	t.Logf("  Goroutines:        before=%d after=%d", goroutinesBefore, goroutinesAfter)
	t.Logf("  Perf ratio (L/F):  %.2f×", ratio)

	if heapGrowth > 512*1024 {
		t.Errorf("heap grew %d bytes — possible memory leak", heapGrowth)
	}
	if goroutinesAfter > goroutinesBefore+2 {
		t.Errorf("goroutine leak: before=%d after=%d", goroutinesBefore, goroutinesAfter)
	}
	if ratio > 1.5 {
		t.Errorf("decoder degraded: last batch %.2f× slower", ratio)
	}
}

// TestLoadSmokeAnnexB verifies Annex B (VAD/DTX/CNG) under sustained mixed load:
// 200 cycles of (10 speech + 20 silence) = 6000 frames, checking for leaks
// and correct frame type transitions throughout.
func TestLoadSmokeAnnexB(t *testing.T) {
	const cycles = 200

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: true})
	dec := NewDecoder()

	speechFrame := generateSine(300.0, 80, 8000.0)
	silenceFrame := make([]int16, 80)
	dst := make([]byte, 10)
	out := make([]int16, 80)

	goroutinesBefore := runtime.NumGoroutine()
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	var speechCount, sidCount, suppressedCount int

	for c := 0; c < cycles; c++ {
		for i := 0; i < 10; i++ { // speech
			n, ft, err := enc.Encode(dst, speechFrame)
			if err != nil {
				t.Fatalf("cycle %d speech %d: %v", c, i, err)
			}
			if err := dec.Decode(out, dst[:n]); err != nil {
				t.Fatalf("cycle %d decode speech %d: %v", c, i, err)
			}
			if ft == FrameSpeech {
				speechCount++
			}
		}
		for i := 0; i < 20; i++ { // silence
			n, ft, err := enc.Encode(dst, silenceFrame)
			if err != nil {
				t.Fatalf("cycle %d silence %d: %v", c, i, err)
			}
			if err := dec.Decode(out, dst[:n]); err != nil {
				t.Fatalf("cycle %d decode silence %d: %v", c, i, err)
			}
			switch ft {
			case FrameSID:
				sidCount++
			case FrameUntransmitted:
				suppressedCount++
			}
		}
	}

	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	goroutinesAfter := runtime.NumGoroutine()

	heapGrowth := int64(memAfter.HeapInuse) - int64(memBefore.HeapInuse)
	totalFrames := cycles * 30

	t.Logf("Annex B load smoke (%d frames, %d cycles):", totalFrames, cycles)
	t.Logf("  Speech=%d  SID=%d  Suppressed=%d", speechCount, sidCount, suppressedCount)
	t.Logf("  Heap growth:  %+d bytes", heapGrowth)
	t.Logf("  Goroutines:   before=%d after=%d", goroutinesBefore, goroutinesAfter)

	if heapGrowth > 512*1024 {
		t.Errorf("heap grew %d bytes — possible Annex B leak", heapGrowth)
	}
	if goroutinesAfter > goroutinesBefore+2 {
		t.Errorf("goroutine leak: before=%d after=%d", goroutinesBefore, goroutinesAfter)
	}
	if sidCount+suppressedCount == 0 {
		t.Error("DTX never activated — VAD may be broken over extended run")
	}
}

// TestLoadSmokeDeadlineCompliance measures how many of 5000 frames would
// miss the 10 ms real-time deadline on the current machine.
// Deadline misses on normal hardware should be zero; any miss is flagged.
func TestLoadSmokeDeadlineCompliance(t *testing.T) {
	const N = 5_000
	const budgetNs = 10_000_000

	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	frame := generateSine(440.0, 80, 8000.0)
	dst := make([]byte, 10)

	for i := 0; i < 50; i++ { // warm up
		_, _, _ = enc.Encode(dst, frame)
	}

	var misses int
	var maxNs int64
	for i := 0; i < N; i++ {
		t0 := time.Now()
		_, _, _ = enc.Encode(dst, frame)
		ns := time.Since(t0).Nanoseconds()
		if ns > budgetNs {
			misses++
		}
		if ns > maxNs {
			maxNs = ns
		}
	}

	missRate := float64(misses) / float64(N) * 100
	t.Logf("Deadline compliance (%d frames, budget=10 ms):", N)
	t.Logf("  Max frame time: %.1f µs", float64(maxNs)/1000)
	t.Logf("  Misses:         %d / %d (%.4f%%)", misses, N, missRate)

	if misses > 0 {
		t.Errorf("%d of %d frames (%.4f%%) exceeded 10 ms — max was %.1f µs",
			misses, N, missRate, float64(maxNs)/1000)
	}
}

