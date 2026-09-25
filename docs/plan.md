# Plan: G.729 Codec — Pure Go Implementation

## Context

Membangun G.729 speech codec dari nol dalam pure Go di direktori `go-g729`.
G.729 adalah standar ITU-T CS-ACELP yang banyak dipakai di VoIP (SIP/RTP).
Project ini mendukung **G.729A** (Annex A, 2-pulse) dan **G.729 Full** (4-pulse) secara bersamaan
dengan shared abstraction dari awal, menggunakan **float32** untuk aritmetika internal,
dan menyertakan **Annex B** (VAD/DTX/CNG).

Karakteristik teknis codec:
- Sampling rate: 8 kHz
- Frame: 10 ms = 80 sample → 80 bit (10 byte) output
- 2 subframe per frame, masing-masing 40 sample
- Bitrate: 8 kbps (core), 6.4 kbps (dengan DTX aktif)

---

## Keputusan Desain

| Aspek | Keputusan |
|---|---|
| Variant | G.729A + G.729 Full — shared abstraction dari awal |
| Arithmetic | `float32` (performa optimal di amd64/ARM64, ±1 LSB toleransi vs. referensi ITU-T) |
| Annex B | Termasuk VAD/DTX/CNG dalam desain |
| Alokasi | Zero-alloc di hot path (semua buffer pre-allocated di struct) |
| Thread safety | Per-instance; caller buat satu Encoder/Decoder per goroutine |

---

## Bitstream Layout (80 bit = 10 byte per frame)

```
┌─────── LSP (18 bit) ──────┬──────────── Subframe 1 (31 bit) ────────────┬──── Subframe 2 (31 bit) ────┐
│ L0(1) L1(7) L2(5) L3(5)  │ P1(8) P0(1) C1(13) S1(4) GA1(3) GB1(4)    │ P2(5) C2(13) S2(4) GA2(3) GB2(4) │
└───────────────────────────┴──────────────────────────────────────────────┴─────────────────────────────┘
```

Annex B SID frame: 15 bit (L0=1, L1=5, L2=4, L3=5) + 65 bit padding, total 80 bit.

---

## Struktur File

```
go-g729/
├── go.mod                          # module github.com/user/go-g729, go 1.22
│
├── g729.go                         # Public API: Encoder/Decoder interface + NewEncoder/NewDecoder
├── encoder.go                      # encoder struct + Encode()
├── decoder.go                      # decoder struct + Decode()
├── g729_test.go                    # ITU-T compliance tests
│
├── internal/
│   ├── params/
│   │   └── params.go               # Konstanta: L_FRAME=80, M=10, PIT_MIN=20, PIT_MAX=143, dll
│   │
│   ├── tables/
│   │   ├── lsp_cb.go               # LSP codebook: L1(128×5), L2a(32×5), L2b(32×5) — dari tab_ld8a.c
│   │   ├── ma_pred.go              # MA prediction coefficients (4×10)
│   │   ├── gain_cb.go              # GA codebook (8 entries), GB codebook (16×2 entries)
│   │   ├── sinc.go                 # Sinc interpolation filter (UP_SAMP=3)
│   │   └── window.go               # 240-point asymmetric Hamming analysis window
│   │
│   ├── bits/
│   │   ├── bitstream.go            # Pack/Unpack: ParamSet ↔ []byte (10 bytes)
│   │   └── bitstream_test.go
│   │
│   ├── dsp/
│   │   ├── filter.go               # HighPassFilter (IIR 2nd-order), SynthesisFilter, Convolution
│   │   ├── autocorr.go             # Windowed autocorrelation (lag 0..10) + lag-windowing
│   │   └── levinson.go             # Levinson-Durbin recursion → LP coefficients
│   │
│   ├── lsp/
│   │   ├── lpc2lsp.go              # A(z) → LSP: Chebyshev 60-pt grid + bisection root finding
│   │   ├── lsp2lpc.go              # LSP → A(z): polynomial reconstruction
│   │   ├── quantize.go             # MA prediction + two-stage split VQ (encode)
│   │   ├── dequantize.go           # MA prediction inverse + VQ lookup (decode)
│   │   └── interpolate.go          # Linear interpolation antar frame (0.5×prev + 0.5×curr)
│   │
│   ├── pitch/
│   │   ├── openloop.go             # Open-loop pitch: 3 kandidat terbaik, range [20,143]
│   │   ├── closedloop.go           # Closed-loop fractional pitch (1/3 resolusi untuk T<85)
│   │   ├── interp.go               # Sinc interpolation excitation buffer untuk fractional lag
│   │   └── parity.go               # Odd-parity bit P0 = XOR(bit 1..7 of P1)
│   │
│   ├── codebook/
│   │   ├── algebraic.go            # BuildCodeVector — shared G.729A dan G.729 Full
│   │   ├── algebraic_a.go          # SearchAlgebraicA — 2-pulse ACELP (G.729A)
│   │   ├── algebraic_full.go       # SearchAlgebraicFull — 4-pulse ACELP (G.729)
│   │   └── gain.go                 # QuantizeGain / DequantizeGain (GA 3-bit + GB 4-bit)
│   │
│   ├── filter/
│   │   ├── perceptual.go           # W(z) = A(z/γ1)/A(z/γ2), γ1=0.9 γ2=0.6
│   │   └── postfilter.go           # Long-term + short-term + tilt compensation + AGC
│   │
│   └── vad/                        # Annex B
│       ├── vad.go                  # Voice Activity Detection
│       ├── dtx.go                  # Discontinuous Transmission state machine
│       └── cng.go                  # Comfort Noise Generation (SID frame decode)
│
├── cmd/
│   └── g729tool/
│       └── main.go                 # CLI: encode/decode PCM ↔ G.729 bitstream file
│
└── testdata/
    └── itu/                        # ITU-T test vectors (algthm.bit, speech.ref, dll)
```

---

## Public API (`g729.go`)

```go
const (
    SampleRate      = 8000  // Hz
    FrameSamples    = 80    // 10 ms @ 8 kHz
    SubframeSamples = 40
    BytesPerFrame   = 10    // 80 bit packed
    LPOrder         = 10    // predictor order
)

type Variant int

const (
    VariantG729A Variant = iota  // Annex A: 2-pulse reduced complexity (default)
    VariantG729                  // Full: 4-pulse ACELP
)

// Encoder mengonversi PCM int16 ke G.729 bitstream, satu frame sekaligus.
// Tidak goroutine-safe — buat satu Encoder per goroutine untuk concurrent use.
type Encoder interface {
    Encode(dst []byte, src []int16) error  // len(src)==80, len(dst)>=10
    Reset()
}

// Decoder mengonversi G.729 bitstream ke PCM int16, satu frame sekaligus.
type Decoder interface {
    Decode(dst []int16, src []byte) error  // len(src)==10, len(dst)>=80
    Reset()
}

func NewEncoder(v Variant) Encoder
func NewDecoder(v Variant) Decoder
```

---

## Internal State Structs

### encoder
```go
type encoder struct {
    variant  Variant
    hpfState [2]float32                   // high-pass filter delay line
    prevLSP  [LPOrder]float32             // LSP kuantisasi frame sebelumnya
    lspMA    [4][LPOrder]float32          // MA predictor memory (4 frame)
    excBuf   [145 + FrameSamples]float32  // excitation history (max pitch=143 + subfr=40+guard)
    synthMem [LPOrder]float32             // LP synthesis filter memory
    wfiltMem [LPOrder]float32             // perceptual weighting filter memory
    gainPred [4]float32                   // MA gain prediction memory
    openPitch int                         // open-loop pitch estimate carry antar subframe
    // pre-allocated working buffers — zero alloc di Encode()
    speech   [FrameSamples]float32
    target1  [SubframeSamples]float32
    target2  [SubframeSamples]float32
    impulse  [SubframeSamples]float32
    phiMat   [SubframeSamples][SubframeSamples]float32  // correlation matrix Φ[i][j]
    vadState vad.State
}
```

### decoder
```go
type decoder struct {
    variant    Variant
    prevLSP    [LPOrder]float32
    lspMA      [4][LPOrder]float32
    excBuf     [145 + FrameSamples]float32
    synthMem   [LPOrder]float32
    hpfState   [2]float32
    pfSTMem    [LPOrder]float32             // post-filter short-term memory
    pfGain     float32                      // post-filter AGC state
    gainPred   [4]float32
    // Annex B / PLC (Packet Loss Concealment)
    lastPitch  int
    lastGain   float32
    badFrames  int
    cngState   vad.CNGState
}
```

---

## Fase Implementasi

```
Fase 1: Tables + Bitstream      ← tidak ada dependensi
Fase 2: DSP Primitives          ← dep: Fase 1
Fase 3: LSP Layer               ← dep: Fase 1, 2
Fase 4: Pitch                   ← dep: Fase 2, 3  ┐ paralel
Fase 5: Algebraic Codebook      ← dep: Fase 2     ┘
Fase 6: Gain Quantization       ← dep: Fase 4, 5
Fase 7: Perceptual + Post-filter← dep: Fase 3
Fase 8: Annex B VAD/DTX/CNG    ← dep: Fase 2, 3
Fase 9: Encoder Assembly        ← dep: semua di atas
Fase 10: Decoder Assembly       ← dep: semua di atas
Fase 11: Integration + CLI      ← dep: Fase 9, 10
```

### Fase 1 — Foundation
**File:** `go.mod`, `internal/params/params.go`, `internal/tables/*.go`, `internal/bits/bitstream.go`

- Init module `go mod init github.com/user/go-g729`
- Salin semua tabel dari referensi ITU-T `tab_ld8a.c` sebagai Go float32 literals
- `Pack`/`Unpack` bit-level untuk 80-bit frame

Test: round-trip `Pack(Unpack(rawBytes))` identik byte-for-byte.

### Fase 2 — DSP Primitives
**File:** `internal/dsp/filter.go`, `autocorr.go`, `levinson.go`

- `HighPassFilter` — IIR 2nd-order (koefisien dari ITU-T spec)
- `Autocorr` — window + lag 0..10 + lag-windowing (bandwidth expansion ~60 Hz)
- `Levinson` — float64 internal untuk stabilitas, output float32; guard divide-by-zero
- `SynthesisFilter`, `Convolution` — gunakan `*[40]float32` bukan slice di hot path

Test: Levinson pada AR(1) signal harus return koefisien eksak.

### Fase 3 — LSP Layer
**File:** `internal/lsp/*.go`

- `LPC2LSP` — Chebyshev polynomial, 60-point grid [-1,1] + bisection untuk root finding
- `LSP2LPC` — rekonstruksi A(z) dari pasangan LSP cosine
- `InterpolateLSP` — lsp_sf1 = 0.5·prev + 0.5·curr, lsp_sf2 = curr
- `QuantizeLSP` — L0 pilih MA predictor set → residual → L1 (128-entry) → L2/L3 (32-entry)
- `DequantizeLSP` — inverse lookup + MA update

Test: LPC→LSP→LPC round-trip error < 1e-5; quantize→dequantize match ITU reference.

### Fase 4 — Pitch (paralel dengan Fase 5)
**File:** `internal/pitch/*.go`

- `InterpExcitation` — sinc interpolation dari excBuf untuk fractional lag
- `OpenLoopPitch` — normalized autocorrelation, 3 kandidat terbaik di [20,143]
- `ClosedLoopPitch` — target/impulse-response cross-correlation; fractional 1/3 untuk T<85
- `ParityBit` — XOR bit 1..7 dari P1

### Fase 5 — Algebraic Codebook (paralel dengan Fase 4)
**File:** `internal/codebook/algebraic*.go`

- `BuildCodeVector` — shared: posisi + tanda → 40-sample excitation vector
- `SearchAlgebraicA` — 2-pulse: precompute backward-filtered target + correlation matrix Φ[i][j]; iterate track 0 dan track 1
- `SearchAlgebraicFull` — 4-pulse: same Φ matrix, depth-first search 4 tracks

Optimisasi kunci: hitung Φ[i][j] = Σ h[n-i]·h[n-j] sekali per subframe, reuse untuk semua kombinasi posisi.

### Fase 6 — Gain Quantization
**File:** `internal/codebook/gain.go`

- `QuantizeGain` — MA prediction energy di log domain; joint search GA(3-bit) × GB(4-bit)
- `DequantizeGain` — inverse lookup + MA memory update

### Fase 7 — Filter
**File:** `internal/filter/perceptual.go`, `postfilter.go`

- `PerceptualWeightCoeffs` — derive numerator/denominator W(z) dari LP a[]
- `ApplyPerceptualFilter` — filter speech untuk target signal computation
- `PostFilter` — long-term (pitch) + short-term (formant) + tilt compensation + AGC

### Fase 8 — Annex B (VAD/DTX/CNG)
**File:** `internal/vad/*.go`

- `VAD` — energy + zero-crossing + spectral measure → active/inactive
- `DTX` — kirim SID frame setiap 8 frame saat silence; hangfire 8 frame di awal silence
- `CNG` — bangkitkan comfort noise dari parameter SID di decoder

SID frame: marker di bit L0=1, parameter LSP 14 bit + log energy 1 bit.

### Fase 9 — Encoder Assembly
**File:** `encoder.go`

Urutan `Encode()` per frame:
1. int16 → float32, `HighPassFilter`
2. Analysis window → `Autocorr` → `Levinson` → LP `a[]`
3. **Annex B path**: jika VAD inactive → encode SID frame, return
4. `LPC2LSP` → `QuantizeLSP` → L0,L1,L2,L3
5. `InterpolateLSP` → 2 set LP → `LSP2LPC`; `PerceptualWeightCoeffs` per subframe
6. `OpenLoopPitch` pada weighted residual
7. **Subframe 1:** target signal → `ClosedLoopPitch` → P1/P0 → `SearchAlgebraicA/Full` → C1/S1 → `QuantizeGain` → GA1/GB1; update excBuf + synthMem
8. **Subframe 2:** sama, P2 = delta (5-bit dari P1)
9. `Pack` → 10 byte output

### Fase 10 — Decoder Assembly
**File:** `decoder.go`

Urutan `Decode()` per frame:
1. `Unpack` → ParamSet; deteksi SID frame → `CNG` path
2. `DequantizeLSP` → `InterpolateLSP` → `LSP2LPC`
3. Per subframe: `InterpExcitation` + `BuildCodeVector` + `DequantizeGain` → excitation → `SynthesisFilter` → `PostFilter`
4. `HighPassFilter`; clip + float32 → int16

### Fase 11 — Integration & CLI
**File:** `g729_test.go`, `cmd/g729tool/main.go`

---

## Strategi Testing

### Unit Tests
| Package | Approach |
|---|---|
| `bits` | Round-trip Pack/Unpack dengan hand-crafted ParamSet |
| `dsp` | Autocorr vs. analytic formula; Levinson pada AR(1) process |
| `lsp` | Root count = 10; LPC2LSP→LSP2LPC round-trip error < 1e-5 |
| `pitch` | Sinusoidal input → correct lag; parity formula |
| `codebook` | BuildCodeVector spot-check; gain round-trip |
| `filter` | DC attenuation HPF; PostFilter output energy ≤ input energy |

### ITU-T Compliance Tests (`g729_test.go`)
```go
// Encoder: setiap 80-sample frame dari speech.in harus menghasilkan
// frame bytes identik dengan algthm.bit (bit-exact)
func TestITUEncoder(t *testing.T)

// Decoder: output tiap frame ±1 LSB dari speech.ref
func TestITUDecoder(t *testing.T)
```

### Benchmarks
```go
func BenchmarkEncode(b *testing.B)  // target: < 50 µs/frame di amd64
func BenchmarkDecode(b *testing.B)  // target: < 30 µs/frame di amd64
```

---

## Optimisasi Go-Specific

| Teknik | Detail |
|---|---|
| Zero allocation | Semua buffer di-pre-alloc di struct encoder/decoder; validasi dengan `go test -benchmem` |
| Fixed-size array params | `*[40]float32` bukan `[]float32` di hot path untuk BCE (bounds-check elimination) |
| Φ matrix reuse | Hitung satu kali per subframe, pakai untuk semua kandidat pulse position |
| excBuf shift | `copy(excBuf[0:145], excBuf[40:185])` setelah tiap frame, hindari modulo index |
| math32 pattern | Cast eksplisit `float32(math.Sqrt(float64(x)))` — hanya 2× per frame di gain normalization |
| Tabel di data segment | Semua table = `var` array literal (bukan `init()`), thread-safe otomatis, no lock |

---

## File Kritis

| File | Alasan |
|---|---|
| `internal/tables/lsp_cb.go` | Titik awal — salin dari ITU `tab_ld8a.c` |
| `internal/bits/bitstream.go` | Integrasi pusat encode↔decode |
| `internal/lsp/quantize.go` | Paling kompleks, paling berdampak pada kualitas |
| `internal/codebook/algebraic_a.go` | Hot path encoder G.729A |
| `internal/codebook/algebraic_full.go` | Hot path encoder G.729 Full |
| `internal/vad/vad.go` | Annex B decision logic |
| `encoder.go` | Assembly semua modul encoder |
| `decoder.go` | Assembly semua modul decoder |
| `g729_test.go` | Compliance gate sebelum release |

---

## Referensi Implementasi

Dari **G.729C reference code** ITU-T (domain publik via ITU Software Tools Library):

| File C | Dipakai untuk |
|---|---|
| `tab_ld8a.c` / `.h` | Salin semua tabel sebagai Go float32 literals |
| `lpcfunc.c` | LPC2LSP, LSP2LPC |
| `qua_lsp.c` | QuantizeLSP, DequantizeLSP |
| `pitch_a.c` / `pitch.c` | Open/closed loop pitch |
| `acelp_ca.c` / `acelp_co.c` | Algebraic codebook search |
| `qua_gain.c` | Gain quantization |
| `filter.c` | Post-filter |
| `vad.c` / `dtx.c` | Annex B VAD/DTX |
