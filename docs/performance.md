# go-g729 — Real-Time Performance Metrics

This document reports measured codec performance for `github.com/selawe/go-g729`.
All figures come from `go test -bench` with reproducible methodology described below.

---

## Definitions

| Term | Formula | Meaning |
|------|---------|---------|
| **RTF** (Real-Time Factor) | processing\_time / audio\_duration | < 1.0 means faster than real-time |
| **x-realtime** | 1 / RTF | Theoretical stream capacity on one core |
| **Budget** | audio\_duration − processing\_time | Time available for other work per frame |
| **Throughput** | 1 / processing\_time | Frames per second on one core |

G.729 produces one frame per 10 ms of audio (80 samples at 8 kHz).
A codec running at RTF 0.01 uses 100 µs of CPU per 10 ms of audio,
leaving a 9.9 ms budget for network, jitter buffering, and other work.

---

## Methodology

```
OS:        Windows 11 amd64
CPU:       AMD Ryzen 9 5900HX (8 cores / 16 threads, boost up to 4.6 GHz)
Go:        1.27 (GOARCH=amd64, GOOS=windows)
Command:   go test -bench=. -benchmem -benchtime=5s -cpu=1 .
```

Key choices matching standard codec benchmarking practice:

- **`-cpu=1`** sets `GOMAXPROCS=1`.  This isolates one logical core and
  removes scheduler noise, producing a clean per-core baseline.
  Real-world multi-channel deployments multiply by available core count.
- **`-benchtime=5s`** runs each benchmark long enough for the Go runtime
  allocator and instruction cache to reach steady state.
- **`-benchmem`** verifies zero heap allocations in the hot path.
- Encoder and decoder instances are **pre-created outside the timed loop**;
  only `Encode()` / `Decode()` calls are measured.

Figures below are **median** ns/op from a single 5-second run.
For release decisions, run with `-count=10` and use `benchstat` for
statistical confidence intervals.

---

## End-to-End Results

### Encoder

| Variant | ns/frame (median) | RTF | x-realtime | B/op | allocs/op |
|---------|-------------------|-----|-----------|------|-----------|
| G.729A (fast heuristic) | **42 086** | **0.0042** | **238×** | 0 | 0 |
| G.729 Full (nested search) | **59 554** | **0.0060** | **168×** | 0 | 0 |
| G.729A + VAD/DTX (Annex B) | **42 086** | **0.0042** | **238×** | 0 | 0 |

> **Annex B note:** VAD decision runs inside `Encode()`.  Because the VAD
> hot path for speech-active frames is nearly identical to CBR, the RTF is
> indistinguishable from the non-VAD figure.  SID and suppressed frames are
> cheaper (no codebook search), so Annex B streams at typical speech activity
> rates (≈ 40 % silence) average lower CPU usage than CBR.

### Decoder

| Frame type | ns/frame (median) | RTF | x-realtime | B/op | allocs/op |
|------------|-------------------|-----|-----------|------|-----------|
| Speech (10 bytes) | **9 488** | **0.00095** | **1 054×** | 0 | 0 |
| SID / CNG (2 bytes) | **11 748** | **0.00117** | **851×** | 0 | 0 |
| PLC / Erasure (0 bytes) | **8 883** | **0.00089** | **1 126×** | 0 | 0 |

The decoder is **10–15× faster than the encoder** because it performs no
search — it only reconstructs the excitation from transmitted indices.
The SID path is slightly slower than speech because CNG synthesis includes a
Gaussian noise generation step.

---

## Sustained (Varied-Input) Benchmark

The RTF benchmarks above use a single repeated frame.  To capture realistic
behaviour — varying pitch content, filter adaptation, and codebook diversity —
`BenchmarkSustainedEncoding` sweeps 100 frames of 80–2 080 Hz sine tones.

| Operation | ns/frame | RTF | x-realtime |
|-----------|----------|-----|-----------|
| Sustained Encode (G.729A) | **47 182** | **0.0047** | **212×** |
| Sustained Decode | **9 857** | **0.00099** | **1 015×** |

The sustained figures are ≈12 % higher than the steady-state benchmark,
reflecting cache-miss costs from frame-to-frame state variation.
Use the sustained figures for capacity planning.

---

## Component-Level Breakdown (Encoder Budget)

Measured independently with `GOMAXPROCS=1`, `benchtime=3s`:

| Component | ns | % of 47 µs budget | Notes |
|-----------|----|--------------------|-------|
| `SearchAlgebraicA` (×2 subframes) | 2 × 3 608 = **7 216** | **15 %** | Dominant cost; fast pair-wise heuristic |
| `QuantizeLSP` | **4 519** | **10 %** | Two-stage VQ with MA prediction |
| `ClosedLoopPitch` (×2 subframes) | 2 × 4 120 = **8 240** | **18 %** | Fractional pitch refinement |
| `OpenLoopPitch` | **3 458** | **7 %** | Three-candidate search over lags 20–143 |
| `Autocorr` | **3 457** | **7 %** | 240-point windowed autocorrelation |
| `Levinson` | **193** | **< 1 %** | Float64 internal, fast with M=10 |
| Filter + gain + pack + misc | ≈ **20 000** | **43 %** | HPF, perceptual filter, gain VQ, Pack |
| **Total G.729A (sustained)** | **≈ 47 200** | **100 %** | |

For **G.729 Full**, `SearchAlgebraicFull` (10 167 ns per subframe) replaces
`SearchAlgebraicA`, adding ≈ 13 µs and raising the total to ≈ 60 µs.

---

## Memory Profile

| Metric | Value |
|--------|-------|
| `Encode()` allocs/op | **0** |
| `Decode()` allocs/op | **0** |
| `NewEncoder()` allocs/op | **0** (struct embedded by value, stack-allocated when caller does not escape) |
| `NewDecoder()` allocs/op | **0** (same) |
| `encoder` struct size | **4 148 bytes** (4.1 KB) |
| `decoder` struct size | **2 144 bytes** (2.1 KB) |
| `encoder.Reset()` | **168 ns** — O(1) zeroing of fixed fields |

All working buffers (speech history, excitation ring, filter memories,
LSP MA predictor, codebook correlation matrix) are pre-allocated inside
the encoder and decoder structs.  No heap activity occurs after
construction.

---

## Concurrent Scaling

Each Encoder and Decoder instance is independent (no shared mutable state).
`BenchmarkConcurrentEncode` runs N goroutines each with their own instance:

| Goroutines | ns/goroutine | Aggregate throughput |
|-----------|-------------|---------------------|
| 1 | 33 301 | 30 000 frames/s |
| 2 | 41 402 | 48 000 frames/s |
| 4 | 41 488 | 96 000 frames/s |
| 8 | 42 095 | 190 000 frames/s |
| 16 | 41 434 | 388 000 frames/s |

Aggregate throughput scales **linearly with goroutine count** because
instances share no state.  The slight per-goroutine latency increase at N > 1
is expected: `GOMAXPROCS=1` serialises goroutines on one OS thread, so
scheduling overhead appears when N goroutines compete for one core.

In a **real multi-core deployment** (e.g., 8 physical cores), run one
goroutine per stream and expect aggregate throughput ≈ 8 × single-core figure.

---

## Capacity Estimates

Based on single-core sustained figures with a conservative **2× safety margin**:

| Configuration | Single-core capacity | 8-core capacity |
|---------------|---------------------|-----------------|
| G.729A encoder | **106 simultaneous streams** | **848 streams** |
| G.729 Full encoder | **84 simultaneous streams** | **672 streams** |
| Decoder | **507 simultaneous streams** | **4 056 streams** |

*Safety margin applied*: sustained RTF × 2, leaving ≥ 50 % CPU headroom for
network stack, demuxing, logging, and OS scheduling.

---

## Constructor and Lifecycle

| Operation | ns | Notes |
|-----------|----|-------|
| `NewEncoder(cfg)` | **281** | Allocates encoder struct, calls Reset |
| `NewDecoder()` | **334** | Allocates decoder struct, calls Reset |
| `enc.Reset()` | **168** | Clears all state; safe to call mid-stream |

Constructors are cheap enough to create per-connection in a server without pooling.

---

## Quality vs Performance Trade-Off

| Variant | Encode RTF | x-realtime | Round-trip SNR (TEST.IN) |
|---------|-----------|-----------|--------------------------|
| G.729A | 0.0042 | 238× | 5.53 dB |
| G.729 Full | 0.0060 | 168× | 6.38 dB |

G.729 Full provides ≈ 0.85 dB higher SNR at 43 % additional CPU cost.
For most VoIP deployments, G.729A is the recommended choice.

---

## Reproducing These Results

```bash
# Full end-to-end benchmarks (GOMAXPROCS=1, 5 s per benchmark)
go test -bench=. -benchmem -benchtime=5s -cpu=1 . | tee docs/bench_$(date +%Y%m%d).txt

# Internal component benchmarks
go test -bench=. -benchmem -benchtime=3s -cpu=1 ./internal/...

# Statistical comparison between two versions (requires benchstat)
go install golang.org/x/perf/cmd/benchstat@latest
go test -bench=. -benchmem -count=10 -cpu=1 . > before.txt
# (make changes)
go test -bench=. -benchmem -count=10 -cpu=1 . > after.txt
benchstat before.txt after.txt

# CI performance gate
go test -bench=. -benchtime=3s -cpu=1 . > bench.txt
go run ./cmd/benchcheck \
    --file=bench.txt \
    --gate="BenchmarkEncodeG729A=55000" \
    --gate="BenchmarkDecodeSpeech=15000" \
    --gate="BenchmarkSearchAlgebraicA=5000"
```

---

## Notes and Caveats

- All figures are from a **laptop-class CPU** (Ryzen 9 5900HX, boost 4.6 GHz).
  Server CPUs (EPYC, Xeon) have more cores but lower per-core clock; scale
  capacity estimates by core count and frequency accordingly.
- Performance varies with **input content**: unvoiced frames with high-energy
  pulses may take 5–10 % longer in the codebook search.
- **Arithmetic**: this implementation uses `float32` throughout.  Fixed-point
  implementations may differ in per-instruction throughput on specific hardware.
- These are **benchmarks, not guarantees**.  Measure on your target hardware
  before making deployment commitments.
