# go-g729

Implementasi **ITU-T G.729** speech codec dalam pure Go — mencakup **G.729 Annex A** (ACELP fast search), **Full G.729** (nested search + adaptive weighting), dan **Annex B** (VAD / DTX / CNG).

[![Go Reference](https://pkg.go.dev/badge/github.com/selawe/go-g729.svg)](https://pkg.go.dev/github.com/selawe/go-g729)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Zero Allocations](https://img.shields.io/badge/Allocations-0%20allocs%2Fop-brightgreen.svg)]()
[![Pure Go](https://img.shields.io/badge/Pure%20Go-100%25-blue.svg)]()

---

## Fitur Utama

- **100% Pure Go, zero CGO** — cross-compile ke Windows, Linux, macOS, Android, ARM64, WASM tanpa toolchain tambahan.
- **Zero allocation pada hot path** — semua buffer pre-allocated di struct; `Encode()` dan `Decode()` tidak menyentuh heap.
- **G.729A (default):** ACELP fast pair-wise heuristic search — **42 µs/frame, 238× real-time**.
- **Full G.729:** 4-loop nested exhaustive search — **60 µs/frame, 168× real-time**.
- **Decoder:** **9 µs/frame, 1054× real-time** — diverifikasi **SNR > 20 dB** terhadap `TEST.pst` resmi ITU-T.
- **Annex B lengkap:** VAD (energi + zero-crossing + spectral tilt + noise tracking), DTX (SID frame 2-byte per RFC 3551), CNG (Gaussian pseudo-random excitation + pitch + ACELP).
- **Packet Loss Concealment (PLC):** Ekstrapolasi pitch, pelemahan gain bertahap, muting setelah 6+ frame beruntun hilang.
- **Multiple Encoder Profiles:** `ProfileCore`, `ProfileQuality`, `ProfileFast`, `ProfileClipRepair` (soft-knee declipping saturation protection), dan `ProfileDiagnostic` (per-frame DSP telemetry).
- **Streaming I/O (`io.Writer` & `io.Reader`):** `g729.NewWriter` dan `g729.NewReader` untuk memproses aliran byte audio arbitrer secara kontinu.
- **RTP packetization** (RFC 3551) dan **SDP annexb negotiation** (RFC 4566) tersedia sebagai package terpisah dengan uji black-box peer interop.

> Lihat [`docs/performance.md`](docs/performance.md) untuk angka benchmark lengkap beserta metodologi dan estimasi kapasitas.

---

## Struktur Paket

```
go-g729/
├── types.go            # Interface publik: Encoder, Decoder, Config, Profiles, sentinel errors
├── encoder.go          # Pipeline encoder G.729 / G.729A / Annex B
├── decoder.go          # Pipeline decoder, PLC, CNG
├── stream.go           # Streaming I/O adapters: Writer (io.WriteCloser) & Reader (io.Reader)
├── rtp/                # Packetization G.729 per RFC 3551 (Pack, Unpack, Peer Interop Tests)
├── sdp/                # SDP annexb helpers per RFC 4566 (FMTPLine, ParseFMTP, NegotiateAnnexB)
├── cmd/
│   ├── g729tool/       # CLI encode/decode file WAV/PCM
│   └── benchcheck/     # CI gate: fail jika benchmark ns/op melebihi threshold
├── docs/
│   ├── performance.md  # Benchmark terukur: RTF, x-realtime, component breakdown
│   └── plan.md         # Dokumen desain arsitektur lengkap
├── testdata/
│   ├── golden/         # Golden files regression test (bit-exact encoder/decoder output)
│   └── itu/            # Letakkan ITU-T test vectors di sini (TEST.IN, TEST.BIT, TEST.pst)
└── internal/
    ├── bits/           # Bitstream pack/unpack: 80-bit speech + 16-bit SID (RFC 3551)
    ├── codebook/       # Algebraic codebook search (fast & full), gain quantization, taming
    ├── dsp/            # HPF 140/100 Hz, Levinson-Durbin, autocorrelation, convolution
    ├── filter/         # Perceptual weighting W(z), adaptive postfilter (formant+tilt+AGC)
    ├── lsp/            # LPC↔LSP, stabilisasi LSF, kuantisasi vektor MA 2-tahap
    ├── params/         # Konstanta ITU-T G.729
    ├── pitch/          # Open-loop pitch, closed-loop fractional 1/3, sinc interpolation, paritas
    ├── tables/         # Lookup tables: LSP codebook, gain, grid, sinc, VAD
    └── vad/            # Annex B: VAD, DTX state machine, CNG synthesis
```

---

## Performa

Diukur pada AMD Ryzen 9 5900HX, `GOMAXPROCS=1`, `-benchtime=5s`:

| Operasi | ns/frame | RTF | x-realtime | allocs/op |
|---|---|---|---|---|
| Encode G.729A | 42 086 | 0.0042 | **238×** | **0** |
| Encode G.729A + Annex B | 42 086 | 0.0042 | **238×** | **0** |
| Encode Full G.729 | 59 554 | 0.0060 | **168×** | **0** |
| Decode (speech) | 9 488 | 0.00095 | **1 054×** | **0** |
| Decode (SID/CNG) | 11 748 | 0.0012 | **851×** | **0** |
| Decode (PLC) | 8 883 | 0.00089 | **1 126×** | **0** |

RTF < 0.01 berarti >100× headroom dari kebutuhan real-time. Satu core dapat menangani **>100 stream G.729A serentak** dengan safety margin 2×.

---

## Instalasi

```bash
# Sebagai library
go get github.com/selawe/go-g729

# Sebagai CLI tool
go install github.com/selawe/go-g729/cmd/g729tool@latest
```

---

## Penggunaan

### Encoding

```go
package main

import (
    "fmt"
    "log"
    g729 "github.com/selawe/go-g729"
)

func main() {
    // DefaultConfig: G.729A + Annex B VAD aktif
    enc := g729.NewEncoder(g729.DefaultConfig())

    pcm := make([]int16, 80) // 80 sampel = 10 ms @ 8 kHz
    dst := make([]byte, 10)

    // Isi pcm dari sumber audio Anda...

    n, frameType, err := enc.Encode(dst, pcm)
    if err != nil {
        log.Fatal(err)
    }

    switch frameType {
    case g729.FrameSpeech:
        fmt.Printf("Speech: kirim %d byte\n", n) // 10 byte
    case g729.FrameSID:
        fmt.Printf("SID: kirim %d byte\n", n)    // 2 byte (Annex B)
    case g729.FrameUntransmitted:
        fmt.Println("Silence: jangan kirim paket") // 0 byte
    }
}
```

### Decoding

```go
dec := g729.NewDecoder()
pcm := make([]int16, 80)

// Frame speech normal (10 byte)
dec.Decode(pcm, frame10bytes)

// SID / comfort noise (2 byte)
dec.Decode(pcm, frame2bytes)

// Packet loss → Packet Loss Concealment otomatis
dec.Decode(pcm, nil)
```

### Profil Encoder

Tersedia 5 preset profil encoder untuk berbagai kebutuhan:

```go
// 1. Core: Standar VoIP (G.729A + VAD/DTX Annex B aktif)
enc := g729.NewEncoder(g729.ProfileCore())

// 2. Quality: Fidelitas maksimal (Full G.729 nested search, CBR 8 kbps)
enc := g729.NewEncoder(g729.ProfileQuality())

// 3. Fast: Throughput maksimal (G.729A CBR 8 kbps, 250× real-time)
enc := g729.NewEncoder(g729.ProfileFast())

// 4. ClipRepair: Proteksi saturasi audio (+/-32767) dengan soft-knee declipping
enc := g729.NewEncoder(g729.ProfileClipRepair())

// 5. Diagnostic: Monitoring telemetri DSP per frame
enc := g729.NewEncoder(g729.ProfileDiagnostic(func(stats g729.DiagnosticStats) {
    fmt.Printf("Frame %d: Energy=%.1f dB, PitchLag=%d, Clipped=%d\n",
        stats.FrameIndex, stats.EnergyDB, stats.PitchLag, stats.ClippedCount)
}))
```

### Streaming I/O (`io.Writer` & `io.Reader`)

Untuk memproses aliran byte audio PCM linear 16-bit secara kontinu (misalnya `io.Copy`, pipe, file, socket):

```go
// Streaming Encode (PCM bytes → G.729 bitstream)
// Catatan: Writer hanya mendukung CBR (EnableVAD=false). Gunakan package rtp untuk Annex B.
writer, err := g729.NewWriter(bitstreamOut, g729.ProfileFast())
if err != nil {
    log.Fatal(err)
}
_, err = io.Copy(writer, pcmReader)
writer.Close() // Flush padding sisa frame

// Streaming Decode (G.729 bitstream → PCM bytes)
reader := g729.NewReader(bitstreamIn)
_, err = io.Copy(pcmOut, reader)
```

### RTP Packetization

```go
import "github.com/selawe/go-g729/rtp"

// Pack 2 frame menjadi satu RTP payload 20 ms
payload, err := rtp.Pack([][]byte{frame1, frame2})

// Unpack
frames, info, err := rtp.Unpack(payload)
// info.Type: FrameSpeech / FrameSID / FrameSuppressed
// info.NumFrames, info.DurationMs

// Timestamp RTP
ts := rtp.TimestampForFrame(baseTimestamp, frameIndex) // +80 per frame @ 8000 Hz
```

### SDP Negotiation (Annex B)

```go
import (
    g729 "github.com/selawe/go-g729"
    "github.com/selawe/go-g729/sdp"
)

// Build SDP offer
cfg := g729.DefaultConfig() // EnableVAD: true
line := sdp.FMTPLine(18, cfg)       // "a=fmtp:18 annexb=yes"
rmap := sdp.RTPMapLine(18)          // "a=rtpmap:18 G729/8000"

// Parse SDP answer dari remote peer
cfg, err := sdp.ConfigFromFMTP("annexb=no")
enc := g729.NewEncoder(cfg) // CBR 8 kbps sesuai negosiasi

// Negotiate offer/answer: "no" wins
agreed, _ := sdp.NegotiateAnnexB("annexb=yes", "annexb=no") // → false
```

---

## CLI Tool (`g729tool`)

```bash
# Encode WAV ke G.729 (CBR 8 kbps)
g729tool -e input.wav output.g729

# Encode dengan Annex B VAD (DTX/CNG aktif)
g729tool -e -vad input.wav output.g729

# Decode ke WAV
g729tool -d input.g729 output.wav

# Decode dengan simulasi 5% packet loss
g729tool -d -loss 0.05 input.g729 output.wav

# Benchmark dengan Full G.729
g729tool -bench -full -e speech.wav speech.g729
```

| Flag | Default | Keterangan |
|---|---|---|
| `-e` | false | Mode encode (PCM/WAV → bitstream) |
| `-d` | false | Mode decode (bitstream → PCM/WAV) |
| `-full` | false | Gunakan Full G.729 (nested search) |
| `-vad` | false | Aktifkan Annex B VAD/DTX/CNG |
| `-loss` | 0.0 | Simulasi packet loss ratio saat decode (0.0–1.0) |
| `-bench` | false | Tampilkan RTF dan statistik performa |

---

## Testing

```bash
# Semua unit test dan integration test
go test ./...

# Dengan race detector
go test -race ./...

# Fuzz testing (decoder robustness terhadap input arbitrer)
go test -fuzz=FuzzDecode$ -fuzztime=5m .
go test -fuzz=FuzzEncode -fuzztime=5m .

# Benchmark + validasi zero alloc
go test -bench=. -benchmem -benchtime=5s -cpu=1 .

# Regenerate golden files setelah perubahan encoder yang intentional
go test -run TestGolden -update-golden .

# CI performance gate
go test -bench=. -benchtime=3s -cpu=1 . > bench.txt
go run ./cmd/benchcheck \
    --file=bench.txt \
    --gate="BenchmarkEncodeG729A=55000" \
    --gate="BenchmarkDecodeSpeech=15000"

# Verifikasi ITU-T compliance (butuh test vectors di testdata/itu/)
# Download dari: https://www.itu.int/net/itu-t/sigdb/genaudio/
go test -run TestDecoderOfficialTestVector -v .

# Distribusi latensi dan P99 frame jitter test (10 000 frame)
go test -run TestFrameTimeJitter -v .

# Endurance load-test smoke (50 000 frame / 500 detik simulasi)
go test -run TestLoadSmoke -v .
```

---

## Catatan Thread Safety

Setiap `Encoder` dan `Decoder` adalah **tidak goroutine-safe**. Untuk memproses banyak stream audio secara paralel, buat satu instance per stream:

```go
// BENAR: satu encoder per goroutine/stream
for _, stream := range streams {
    go func(s Stream) {
        enc := g729.NewEncoder(g729.DefaultConfig())
        // proses stream s...
    }(stream)
}

// SALAH: berbagi satu encoder antar goroutine
enc := g729.NewEncoder(g729.DefaultConfig())
go func() { enc.Encode(...) }() // DATA RACE
go func() { enc.Encode(...) }()
```

Constructor `NewEncoder` dan `NewDecoder` ringan (< 1 µs, 0 alloc) sehingga tidak perlu pooling.

---

## Lisensi

MIT. Tabel konstanta algoritma diturunkan dari spesifikasi teknis ITU-T G.729.
