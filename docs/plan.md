# Plan: G.729 Codec — Pure Go Implementation

## Context

Membangun G.729 speech codec dari nol dalam pure Go di direktori `go-g729`.
G.729 adalah standar ITU-T CS-ACELP yang banyak dipakai di VoIP (SIP/RTP).
Project ini mendukung **G.729A** (Annex A, fast-search) dan **G.729 Full** (full nested search) secara bersamaan
dengan shared abstraction dari awal, menggunakan **float32** untuk aritmetika internal,
dan menyertakan **Annex B** (VAD/DTX/CNG).

> **Catatan penting:** G.729 dan G.729A **100% kompatibel bitstream**. Keduanya menggunakan
> codebook inovasi **4 pulsa** (17 bit: 13 bit posisi + 4 bit tanda), satu pulsa per track.
> Perbedaan G.729A bukan pada jumlah pulsa, melainkan **algoritma pencarian** yang lebih cepat:
> `D4i40_17_fast` (pair-wise heuristic dengan threshold korelasi Dₙ), bukan 4-loop nested penuh.

Karakteristik teknis codec:
- Sampling rate: 8 kHz
- Frame: 10 ms = 80 sample → 80 bit (10 byte) output
- 2 subframe per frame, masing-masing 40 sample
- Bitrate: 8 kbps (core), 6.4 kbps (dengan DTX aktif)

---

## Keputusan Desain

| Aspek | Keputusan |
|---|---|
| Variant | G.729A (fast heuristic search) + G.729 Full (nested search) — bitstream identik, shared codebook 4-pulse |
| Arithmetic | `float32` — performa optimal di amd64/ARM64; **tidak** bit-exact vs. fixed-point ITU-T vectors (lihat catatan compliance encoder di bawah) |
| Annex B | Termasuk VAD/DTX/CNG dalam desain |
| Alokasi | Zero-alloc di hot path (semua buffer pre-allocated di struct) |
| Thread safety | Per-instance; caller buat satu Encoder/Decoder per goroutine |

---

## Bitstream Layout (80 bit = 10 byte per frame)

```
┌─────── LSP (18 bit) ──────┬──────────── Subframe 1 (33 bit) ────────────┬──── Subframe 2 (29 bit) ────┐
│ L0(1) L1(7) L2(5) L3(5)  │ P1(8) P0(1) C1(13) S1(4) GA1(3) GB1(4)    │ P2(5) C2(13) S2(4) GA2(3) GB2(4) │
└───────────────────────────┴──────────────────────────────────────────────┴─────────────────────────────┘
```

Perhitungan alokasi bit (ITU-T Rec. G.729 Table 1):
- **LSP:** 18 bit (L0: 1, L1: 7, L2: 5, L3: 5)
- **Subframe 1:** 33 bit (P1: 8, P0: 1, C1: 13, S1: 4, GA1: 3, GB1: 4)
- **Subframe 2:** 29 bit (P2: 5, C2: 13, S2: 4, GA2: 3, GB2: 4)
- **Total per frame:** 18 + 33 + 29 = **80 bit (10 byte)**.

### Bit Mapping per Byte (Network Byte Order / MSB first)

```
Byte 0: [ L0(1b) | L1(7b) ]
Byte 1: [ L2(5b) | L3_msb(3b) ]
Byte 2: [ L3_lsb(2b) | P1_msb(6b) ]
Byte 3: [ P1_lsb(2b) | P0(1b) | C1_msb(5b) ]
Byte 4: [ C1_mid(8b) ]
Byte 5: [ S1(4b) | GA1(3b) | GB1_msb(1b) ]
Byte 6: [ GB1_lsb(3b) | P2(5b) ]
Byte 7: [ C2_msb(8b) ]
Byte 8: [ C2_lsb(5b) | S2_msb(3b) ]
Byte 9: [ S2_lsb(1b) | GA2(3b) | GB2(4b) ]
```

### Annex B Frame Types (RFC 3551 / ITU-T G.729B)

Codec Annex B menggunakan **panjang byte input/output** untuk mendeteksi tipe frame (bukan marker bit in-band):
- **Frame Suara (Active Speech):** **10 byte (80 bit)**.
- **Frame SID (Silence Insertion Descriptor):** **2 byte (16 bit)**:
  - Switched predictor index: 1 bit
  - LSF 1st-stage codebook: 5 bit
  - LSF 2nd-stage codebook: 4 bit
  - Energy (gain): 5 bit
  - Unused padding: 1 bit (selalu 0)
  - Total: 16 bit (2 octet).
- **Frame Untransmitted (DTX Silence Suppression):** **0 byte** (tidak ada paket dikirim selama hening berkelanjutan).
- **Packet Loss Erasure:** **0 byte / nil buffer** ke Decoder untuk memicu PLC (*Packet Loss Concealment*).

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
│   │   ├── stability.go            # LSP ordering + minimum distance separation (d_min = 0.005 rad)
│   │   ├── quantize.go             # MA prediction + two-stage split VQ (encode)
│   │   ├── dequantize.go           # MA prediction inverse + VQ lookup (decode)
│   │   └── interpolate.go          # Linear interpolation antar frame (0.5×prev + 0.5×curr)
│   │
│   ├── pitch/
│   │   ├── openloop.go             # Open-loop pitch: 3 kandidat terbaik, range [20,143]
│   │   ├── closedloop.go           # Closed-loop fractional pitch (1/3 resolusi untuk T<85)
│   │   ├── interp.go               # Sinc interpolation excitation buffer untuk fractional lag
│   │   └── parity.go               # Odd-parity bit P0 = XOR(6 MSB of P1, bit 2..7)
│   │
│   ├── codebook/
│   │   ├── algebraic.go            # BuildCodeVector + struktur track — shared G.729A dan G.729 Full
│   │   ├── algebraic_a.go          # SearchAlgebraicA — 4-pulse, fast pair-wise heuristic (G.729A, D4i40_17_fast)
│   │   ├── algebraic_full.go       # SearchAlgebraicFull — 4-pulse, full 4-loop nested search (G.729)
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
    ├── itu/                        # ITU-T test vectors resmi
    │   ├── speech.in               # Input PCM 16-bit 8kHz (dari G.729C package)
    │   ├── algthm.bit              # Output encoder referensi (10 byte/frame)
    │   ├── speech.ref              # Output decoder referensi (PCM)
    │   ├── tame.bit                # Frame tame (all-zero excitation)
    │   ├── tamede.ref              # Referensi decode tame frames
    │   └── itu_std.bit             # ITU standard compliance bitstream
    ├── golden/                     # Golden files untuk regression test
    │   ├── g729a_encoder.bit       # Output encoder G.729A yang sudah divalidasi
    │   └── g729_encoder.bit        # Output encoder G.729 Full yang sudah divalidasi
    └── synthetic/                  # Sinyal sintetis untuk unit test
        ├── tone_300hz.pcm          # Pure tone 300 Hz @ 8 kHz
        ├── tone_1000hz.pcm         # Pure tone 1 kHz
        ├── white_noise.pcm         # White noise
        └── silence.pcm             # Silence (semua nol)
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
    VariantG729A Variant = iota  // Annex A: reduced-complexity fast heuristic search (default)
    VariantG729                  // Full: 4-pulse ACELP nested search
)

type FrameType int

const (
    FrameSpeech        FrameType = iota // 10 byte (active voice frame)
    FrameSID                            // 2 byte (comfort noise update / Annex B)
    FrameUntransmitted                  // 0 byte (DTX silence suppression, no packet transmitted)
)

// Config mengatur parameter operasional encoder.
type Config struct {
    Variant   Variant // VariantG729A (default) atau VariantG729
    EnableVAD bool    // true: Annex B VAD/DTX aktif; false: CBR 8 kbps konstan (annexb=no di SDP)
}

// DefaultConfig mengembalikan konfigurasi standar G.729A dengan VAD aktif.
func DefaultConfig() Config {
    return Config{
        Variant:   VariantG729A,
        EnableVAD: true,
    }
}

// Encoder mengonversi PCM int16 ke bitstream G.729, satu frame (80 sample = 10 ms) per panggilan.
// Tidak goroutine-safe — buat satu Encoder per stream/goroutine.
type Encoder interface {
    // Encode memproses 80 sampel PCM int16 dan menulis bitstream ke dst (kapasitas minimal 10 byte).
    // Mengembalikan:
    // - n: jumlah byte yang ditulis (10 untuk FrameSpeech, 2 untuk FrameSID, 0 untuk FrameUntransmitted)
    // - frameType: jenis frame yang dihasilkan
    // - err: nil jika sukses, atau error jika panjang buffer tidak valid
    Encode(dst []byte, src []int16) (n int, frameType FrameType, err error)
    Reset()
}

// Decoder mengonversi bitstream G.729 ke PCM int16, satu frame sekaligus.
// Tidak goroutine-safe — buat satu Decoder per stream/goroutine.
type Decoder interface {
    // Decode merekonstruksi 80 sampel PCM int16 ke dst (kapasitas minimal 80 int16).
    // Parameter src mendukung:
    // - len(src) == 10: frame speech normal
    // - len(src) == 2:  frame SID Annex B (sintesis comfort noise)
    // - len(src) == 0 atau src == nil: Packet Loss Concealment (PLC / frame erasure)
    Decode(dst []int16, src []byte) error
    Reset()
}

func NewEncoder(cfg Config) Encoder
func NewDecoder(v Variant) Decoder
```

---

## Internal State Structs

### encoder
```go
type encoder struct {
    variant   Variant
    enableVAD bool
    hpfState  [2]float32                   // high-pass filter delay line (2nd order IIR)
    prevLSP   [LPOrder]float32             // LSP terkuantisasi frame sebelumnya (untuk interpolasi)
    lspMA     [4][LPOrder]float32          // MA predictor memory (4 frame history)
    
    // History buffers untuk analisis DSP
    speechBuf [240]float32                 // 240-pt window: 120 past + 80 current + 40 lookahead
    oldWsp    [143]float32                 // past weighted speech history untuk open-loop pitch (lag 20..143)
    excBuf    [154 + FrameSamples]float32  // excitation history: PIT_MAX(143) + L_INTER(10) + guard + 80 sample
    
    // Filter memories
    synthMem  [LPOrder]float32             // 1/A(z) LP synthesis filter memory
    wSynthMem [LPOrder]float32             // weighted synthesis filter W(z)/A(z) zero-input memory
    gainPred  [4]float32                   // MA gain prediction memory (log domain energy)
    openPitch int                          // open-loop pitch carry-over
    
    // Pre-allocated working buffers — zero alloc di Encode()
    target1   [SubframeSamples]float32     // target signal subframe 1 (40 sample)
    target2   [SubframeSamples]float32     // target signal subframe 2 (40 sample)
    impulse   [SubframeSamples]float32     // impulse response h(n) (40 sample)
    phiMat    [SubframeSamples][SubframeSamples]float32  // correlation matrix Φ[i][j] (40×40)
    vadState  vad.State                    // Annex B VAD decision state
    dtxState  vad.DTXState                 // Annex B DTX state machine (hangover, SID interval)
}
```

### decoder
```go
type decoder struct {
    variant    Variant
    prevLSP    [LPOrder]float32             // LSP frame sebelumnya
    lspMA      [4][LPOrder]float32          // MA predictor memory
    excBuf     [154 + FrameSamples]float32  // excitation buffer (past history + current)
    synthMem   [LPOrder]float32             // 1/A(z) synthesis filter memory
    hpfState   [2]float32                   // output post-filter high-pass delay line
    
    // Post-filter memories (short-term formant + long-term pitch + tilt + AGC)
    pfSTMem    [LPOrder]float32             // post-filter short-term IIR memory
    pfLTMem    [143 + SubframeSamples]float32 // post-filter long-term past speech memory (pitch lag <= 143)
    pfTiltMem  float32                      // post-filter tilt compensation delay state (1st order FIR)
    pfGain     float32                      // post-filter AGC smoothed gain state
    
    // Annex B & PLC (Packet Loss Concealment / Frame Erasure)
    lastPitch  int                          // pitch lag frame terakhir (untuk PLC repetition)
    lastGain   float32                      // pitch gain terakhir (di-attenuate 0.98 tiap lost frame)
    badFrames  int                          // counter consecutive packet loss
    cngState   vad.CNGState                 // Comfort Noise Generator state (SID parameters)
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

- `LPC2LSP` — Chebyshev polynomial, 60-point grid [-1,1] + bisection untuk root finding (fallback ke `prevLSP` bila akar < 10)
- `StabilizeLSP` — Memastikan urutan monotonik naik $0 < \omega_1 < \dots < \omega_{10} < \pi$ dan jarak minimum $d_{min} = 0.005$ rad
- `LSP2LPC` — Rekonstruksi A(z) dari pasangan LSP cosine
- `InterpolateLSP` — lsp_sf1 = 0.5·prev + 0.5·curr, lsp_sf2 = curr (menggunakan quantized LSP untuk sintesis)
- `QuantizeLSP` — L0 pilih MA predictor set → residual → L1 (128-entry) → L2/L3 (32-entry)
- `DequantizeLSP` — Inverse lookup + MA update + `StabilizeLSP`

Test: LPC→LSP→LPC round-trip error < 1e-5; quantize→dequantize match ITU reference; stability check menstabilkan LSF tidak berurutan.

### Fase 4 — Pitch (paralel dengan Fase 5)
**File:** `internal/pitch/*.go`

- `InterpExcitation` — Sinc interpolation dari excBuf untuk fractional lag (UP_SAMP=3, filter length 10)
- `OpenLoopPitch` — Normalized autocorrelation pada sinyal weighted speech `oldWsp`, 3 kandidat terbaik di [20,143]
- `ClosedLoopPitch` — Cross-correlation target/impulse response; fractional 1/3 untuk delay < 85
- `ParityBit` — Odd-parity bit P0 = XOR(6 bit MSB dari P1, yaitu bit 2..7)

### Fase 5 — Algebraic Codebook (paralel dengan Fase 4)
**File:** `internal/codebook/algebraic*.go`

**Struktur 4 track codebook (sama untuk G.729A dan G.729 Full):**
```
Track 0: posisi {0, 5,10,15,20,25,30,35}         → 3 bit (8 posisi)
Track 1: posisi {1, 6,11,16,21,26,31,36}         → 3 bit (8 posisi)
Track 2: posisi {2, 7,12,17,22,27,32,37}         → 3 bit (8 posisi)
Track 3: posisi {3, 8,13,18,23,28,33,38,
                 4, 9,14,19,24,29,34,39}          → 4 bit (16 posisi)
Sign per track:  s0 s1 s2 s3                       → 4 bit
Total per subframe: 13 bit posisi + 4 bit tanda = 17 bit ✓ (sesuai ParamSet C1/S1)
```

- `BuildCodeVector` — shared: 17-bit index → 4 posisi + 4 tanda → 40-sample excitation vector
- `SearchAlgebraicA` — **4 pulsa**, fast pair-wise heuristic (`D4i40_17_fast`): cari track (0,1) dan track (2,3) secara berpasangan, gunakan threshold korelasi Dₙ untuk pruning; precompute Φ[i][j]
- `SearchAlgebraicFull` — **4 pulsa**, 4-loop nested exhaustive search: iterasi semua kombinasi (8×8×8×16 = 8192) dengan Φ matrix reuse

Optimisasi kunci (berlaku keduanya): hitung Φ[i][j] = Σ h[n-i]·h[n-j] sekali per subframe, reuse untuk semua kombinasi posisi.

### Fase 6 — Gain Quantization
**File:** `internal/codebook/gain.go`

- `QuantizeGain` — MA prediction energy di log domain; joint search GA(3-bit) × GB(4-bit)
- `DequantizeGain` — inverse lookup + MA memory update

### Fase 7 — Filter
**File:** `internal/filter/perceptual.go`, `postfilter.go`

- `PerceptualWeightCoeffs` — derive numerator/denominator W(z) dari unquantized LP a[]
- `ApplyPerceptualFilter` — filter speech untuk target signal computation
- `PostFilter` — long-term (pitch) + short-term (formant) + tilt compensation + AGC

### Fase 8 — Annex B (VAD/DTX/CNG)
**File:** `internal/vad/*.go`

- `VAD` — Energy + zero-crossing + spectral tilt (reflection coeff 1) + background noise estimate → active/inactive
- `DTX` — State machine: Active Speech → Hangover (8 frame) → Inactive. Transmit SID (2 byte) pada awal hening dan periodik setiap 8 frame; selain itu 0 byte (untransmitted)
- `CNG` — Bangkitkan comfort noise dari parameter SID (energi + LSF) di decoder menggunakan pseudo-random Gaussian excitation

Format SID Frame (RFC 3551): **2 byte (16 bit)** = 1 bit switched predictor + 5 bit LSF stage 1 + 4 bit LSF stage 2 + 5 bit energy + 1 bit unused pad.

### Fase 9 — Encoder Assembly
**File:** `encoder.go`

Urutan `Encode(dst []byte, src []int16) (int, FrameType, error)` per frame:
1. Validasi buffer input (`len(src) == 80`) dan output (`len(dst) >= 10`).
2. int16 → float32, `HighPassFilter` 2nd-order.
3. Update `speechBuf`: geser 160 sampel lama ke kiri, masukkan 80 sampel baru di `speechBuf[160:240]`. (Mendukung window analisis 240 sampel dengan lookahead 40 sampel).
4. Analysis window (240-pt asymmetric Hamming) → `Autocorr` → `Levinson` → LP `a[]`.
5. **Annex B path** (jika `enableVAD == true`):
   - Hitung VAD metric. Jika inactive: jalankan DTX state machine.
   - Jika DTX memutuskan kirim SID: pack 15 bit parameter SID ke 2 byte `dst`, return `(2, FrameSID, nil)`.
   - Jika DTX memutuskan untransmitted: return `(0, FrameUntransmitted, nil)`.
6. `LPC2LSP` → `StabilizeLSP` → `QuantizeLSP` → L0, L1, L2, L3.
7. `InterpolateLSP` (menggunakan quantized LSF) → 2 set LP $\hat{A}_1(z), \hat{A}_2(z)$ untuk sintesis. Hitung unquantized `PerceptualWeightCoeffs` $W(z)$ per subframe.
8. Filter weighted speech, update `oldWsp`, jalankan `OpenLoopPitch` (3 kandidat terbaik di [20,143]).
9. **Subframe 1:** Target signal → `ClosedLoopPitch` → P1 / P0 (parity 6 MSB) → `SearchAlgebraicA/Full` (4 pulsa) → C1/S1 → `QuantizeGain` → GA1/GB1; update `excBuf` + `synthMem`.
10. **Subframe 2:** Target signal → `ClosedLoopPitch` (delta 5-bit relatif P1) → `SearchAlgebraicA/Full` (4 pulsa) → C2/S2 → `QuantizeGain` → GA2/GB2; update `excBuf` + `synthMem`.
11. Geser excitation buffer: `copy(excBuf[0:154], excBuf[80:234])`.
12. `Pack` 80 bit ke `dst[:10]`, return `(10, FrameSpeech, nil)`.

### Fase 10 — Decoder Assembly
**File:** `decoder.go`

Urutan `Decode(dst []int16, src []byte) error` per frame:
1. Validasi buffer output (`len(dst) >= 80`).
2. **Deteksi Tipe Frame berdasarkan panjang byte `src`:**
   - **Case A: `len(src) == 0` atau `src == nil` (Packet Loss / PLC):**
     - Increment `badFrames`.
     - Ekstrapolasi pitch lag dari `lastPitch` (dengan random jitter bila consecutive loss).
     - Atenuasi pitch gain $g_p \leftarrow g_p \times 0.98$ dan codebook gain $g_c \leftarrow g_c \times 0.98$.
     - Jika `badFrames > 6`, mute secara bertahap menuju 0.
     - Sintesis eksitasi menggunakan gain teratenuasi $\to$ `SynthesisFilter` $\to$ `PostFilter`.
   - **Case B: `len(src) == 2` (Annex B SID Frame):**
     - Unpack 15-bit SID parameters (predictor bit, LSF stage 1 & 2, log energy).
     - Update `cngState`, interpolasi LSF noise, bangkitkan comfort noise eksitasi pseudo-random.
     - Sintesis $1/\hat{A}(z)$ tanpa post-filter formant keras $\to$ `HighPassFilter`.
   - **Case C: `len(src) == 10` (Normal Speech Frame):**
     - Reset `badFrames = 0`.
     - `Unpack` 80 bit $\to$ `ParamSet`.
     - Verifikasi parity bit P0 terhadap 6 MSB P1. (Jika parity mismatch $\to$ switch ke PLC concealment).
     - `DequantizeLSP` $\to$ `StabilizeLSP` $\to$ `InterpolateLSP` $\to$ `LSP2LPC`.
     - Per subframe (1 dan 2):
       - `InterpExcitation` (fractional adaptive codebook delay).
       - `BuildCodeVector` (4 pulsa dari 13 bit posisi + 4 bit tanda).
       - `DequantizeGain` (gain pitch $g_p$ + codebook $g_c$).
       - Rekonstruksi eksitasi total $e(n) = g_p v(n) + g_c c(n)$.
       - `SynthesisFilter` $1/\hat{A}(z)$ dengan memori filter `synthMem`.
       - `PostFilter` (long-term pitch + short-term formant + tilt compensation + AGC).
   - **Case Lain:** Return `ErrInvalidFrameLength`.
3. `HighPassFilter` output 2nd-order.
4. Saturasi / clipping float32 ke rentang $[-32768, 32767]$ dan konversi ke `dst` int16.

### Fase 11 — Integration & CLI
**File:** `g729_test.go`, `cmd/g729tool/main.go`

---

## Strategi Testing

Testing berlapis dari unit → integrasi → compliance → kualitas perseptual.
**Gate utama: ITU-T compliance harus lulus sebelum codec dinyatakan valid.**

---

### Layer 1 — Unit Tests (per modul)

Setiap paket internal ditest dengan input analitik yang hasilnya bisa diverifikasi secara matematis,
tanpa dependensi pada modul lain.

#### `internal/bits` — Bitstream Pack/Unpack
```go
// Round-trip ParamSet → bytes → ParamSet harus identik
func TestBitstreamRoundTrip(t *testing.T) {
    p := ParamSet{L0:1, L1:63, L2:15, L3:31, P1:100, P0:1,
                  C1:0x1FFF, S1:0xF, GA1:7, GB1:15,
                  P2:31, C2:0x1FFF, S2:0xF, GA2:7, GB2:15}
    buf := make([]byte, 10)
    Pack(buf, &p)
    var p2 ParamSet
    Unpack(&p2, buf)
    assert(t, p == p2)
}

// Verifikasi posisi bit masing-masing field tidak overlap
func TestBitstreamFieldBoundaries(t *testing.T)

// SID frame Annex B: 2 byte (16 bit: 15 bit parameter + 1 bit unused pad)
func TestBitstreamSIDFrame(t *testing.T) {
    sid := SIDParamSet{Predictor: 1, Stage1: 25, Stage2: 12, Energy: 18}
    buf := make([]byte, 2)
    PackSID(buf, &sid)
    var sid2 SIDParamSet
    UnpackSID(&sid2, buf)
    assert(t, sid == sid2)
}
```

#### `internal/dsp` — DSP Primitives
```go
// Levinson pada AR(1): r[k]=ρ^k → a[1]=-ρ, error=1-ρ²
func TestLevinsonAR1(t *testing.T) {
    rho := float32(0.9)
    r := [11]float32{}
    for k := range r { r[k] = float32(math.Pow(float64(rho), float64(k))) }
    a, _, _ := Levinson(r[:], 10)
    assertClose(t, a[1], -rho, 1e-5)
}

// Levinson harus stabil: tidak NaN/Inf pada input edge case
func TestLevinsonStability(t *testing.T)    // r[0]=0, near-singular matrix

// HighPassFilter: input DC murni (nilai konstan) harus ter-attenuasi ke ~0
func TestHighPassFilterDC(t *testing.T) {
    state := [2]float32{}
    dc := make([]float32, 800) // 100 ms DC
    for i := range dc { dc[i] = 1000.0 }
    HighPassFilter(dc, &state)
    // setelah settling: |output| < 1.0
    assertClose(t, dc[799], 0.0, 1.0)
}

// Autocorr: sinyal konstan → r[0]=N*A², r[k]=N*A² untuk semua k
func TestAutocorrConstantSignal(t *testing.T)

// Autocorr lag-0 harus selalu >= lag lainnya
func TestAutocorrLag0Dominant(t *testing.T)
```

#### `internal/lsp` — LSP Conversion & Quantization
```go
// LPC2LSP harus menghasilkan tepat 10 root terurut naik pada [0, π] dari filter stabil
func TestLPC2LSPRootCount(t *testing.T) {
    // Koefisien filter LP stabil representatif (bandwidth expanded)
    a := [10]float32{-1.25, 0.78, -0.42, 0.25, -0.15, 0.10, -0.06, 0.04, -0.02, 0.01}
    lsp, ok := LPC2LSP(&a)
    assert(t, ok, "root finding gagal")
    assert(t, len(lsp) == 10)
    for i := 1; i < 10; i++ {
        assert(t, lsp[i] > lsp[i-1], "LSP tidak terurut naik")
    }
}

// StabilizeLSP memastikan jarak antar LSF minimal d_min = 0.005 rad
func TestLSPStabilize(t *testing.T) {
    // Input LSF buatan dengan jarak terlalu rapat (< 0.005)
    lsf := [10]float32{0.20, 0.201, 0.50, 0.80, 1.10, 1.40, 1.70, 2.00, 2.30, 2.60}
    StabilizeLSP(&lsf, 0.005)
    for i := 1; i < 10; i++ {
        assert(t, lsf[i]-lsf[i-1] >= 0.005, "LSF terlalu dekat")
    }
}

// Round-trip LPC→LSP→LPC: error < 1e-5
func TestLPCLSPRoundTrip(t *testing.T) {
    a := randomStableLP(10)
    lsp, _ := LPC2LSP(&a)
    aBack := LSP2LPC(lsp)
    for i := range a {
        assertClose(t, a[i], aBack[i], 1e-5)
    }
}

// Quantize→Dequantize: rekonstruksi LSP harus mendekati input (distorsi terbatas)
func TestLSPQuantizeDequantize(t *testing.T) {
    lsp, _ := LPC2LSP(&typicalLP)
    L0, L1, L2, L3, lspQ := QuantizeLSP(lsp, prevLSP, maMemory)
    lspBack := DequantizeLSP(L0, L1, L2, L3, prevLSP, maMemory)
    // Distorsi kuantisasi: MSE < 0.01 (dalam domain cosine)
    assertMSE(t, lspQ[:], lspBack[:], 0.01)
}
```

#### `internal/pitch` — Pitch Estimation
```go
// Tone 300 Hz @ 8kHz → periode = 8000/300 ≈ 26.67 → P1 ∈ [24, 30]
func TestOpenLoopPitchTone300Hz(t *testing.T) {
    pcm := generateTone(300.0, 8000, 240)
    candidates := OpenLoopPitch(pcm)
    closest := minDistance(candidates[:], 27)
    assert(t, closest <= 3, "estimasi pitch jauh dari referensi")
}

// Tone 100 Hz → P1 ≈ 80; menguji batas atas pitch range
func TestOpenLoopPitchLowTone(t *testing.T)

// Parity bit: odd parity dari 6 bit paling signifikan (MSB) dari P1 (bit 2..7)
func TestParityBit(t *testing.T) {
    for p1 := 0; p1 < 256; p1++ {
        p0 := ParityBit(uint8(p1))
        popcount := bits.OnesCount8(uint8(p1)>>2) + int(p0)
        assert(t, popcount%2 == 1, "paritas bukan ganjil")
    }
}

// Fractional pitch interpolation: error interpolasi vs. sinyal referensi < 1%
func TestSincInterpolationAccuracy(t *testing.T)
```

#### `internal/codebook` — Algebraic Codebook & Gain
```go
// Codebook yang dipilih SearchAlgebraicA harus menurunkan WMSE vs. zero
func TestCodebookSearchImprovement(t *testing.T) {
    target, h := generateRandomTargetAndImpulse(40)
    idx, signs := SearchAlgebraicA(target, h)
    cv := BuildCodeVector(idx, signs) // tidak perlu Variant: format 4-pulse identik untuk G.729A dan Full
    assert(t, WMSE(target, h, cv) < WMSE(target, h, zeros40))
}

// Setiap indeks valid harus menghasilkan vektor dengan tepat 4 non-zero sample
// G.729A dan G.729 Full menggunakan bitstream format yang identik: 4 pulsa, 1 per track
func TestCodeVectorPulseCount(t *testing.T) {
    for i0 := 0; i0 < 8; i0++ {      // track 0: 8 posisi
    for i1 := 0; i1 < 8; i1++ {      // track 1: 8 posisi
    for i2 := 0; i2 < 8; i2++ {      // track 2: 8 posisi
    for i3 := 0; i3 < 16; i3++ {     // track 3: 16 posisi
        idx := packIndex(i0, i1, i2, i3) // 13 bit
        cv  := BuildCodeVector(idx, 0xF) // 4-bit sign = all positive
        nonzero := countNonZero(cv[:])
        assert(t, nonzero == 4, "harus tepat 4 pulsa, got %d (idx=%013b)", nonzero, idx)
    }}}}
}

// Verifikasi posisi track tidak overlap (constraint penting)
func TestCodeVectorTracksNoOverlap(t *testing.T) {
    for _, idx := range sampleIndices {
        pos := extractPositions(BuildCodeVector(idx, 0xF))
        // Track 0: pos mod 5 == 0 (kecuali track 3 yang mencakup mod 5 == 3 dan 4)
        assertTrack0(t, pos[0]) // ∈ {0,5,10,...35}
        assertTrack1(t, pos[1]) // ∈ {1,6,11,...36}
        assertTrack2(t, pos[2]) // ∈ {2,7,12,...37}
        assertTrack3(t, pos[3]) // ∈ {3,8,...38, 4,9,...39}
    }
}

// Gain round-trip: QuantizeGain→DequantizeGain selisih < 5%
func TestGainQuantizeRoundTrip(t *testing.T)

// Gain prediction MA memory harus diupdate dengan benar setelah setiap frame
func TestGainPredictionMemoryUpdate(t *testing.T)
```

#### `internal/filter` — Perceptual & Post-filter
```go
// W(z) = A(z/γ1)/A(z/γ2): pastikan koefisien terkalkulasi benar
func TestPerceptualWeightCoeffs(t *testing.T)

// PostFilter: energi output ≤ energi input (gain scaling AGC)
func TestPostFilterEnergyReduction(t *testing.T) {
    synthSpeech := generateSpeech(40)
    out := make([]float32, 40)
    PostFilter(out, synthSpeech, &lpCoeff, pitch, excBuf, &ltMem, &stMem, &gainState)
    assert(t, energy(out) <= energy(synthSpeech)*1.05) // 5% toleransi AGC
}
```

#### `internal/vad` — Annex B
```go
// Silence berkepanjangan harus trigger VAD inactive setelah hangfire 8 frame
func TestVADSilenceDetection(t *testing.T)

// Speech aktif harus selalu VAD active
func TestVADSpeechDetection(t *testing.T)

// SID frame harus dikirim setiap 8 frame saat DTX aktif
func TestDTXSIDInterval(t *testing.T)

// CNG output harus dalam rentang energi yang sesuai SID parameter
func TestCNGEnergyMatch(t *testing.T)
```

---

### Layer 2 — Synthetic Signal Tests

Test dengan sinyal yang karakteristiknya diketahui secara pasti.
File sinyal ada di `testdata/synthetic/`.

| Sinyal | Kondisi | Assertion |
|---|---|---|
| Pure tone 300 Hz | Normal encode/decode | Pitch P1 ∈ [24,30]; SNR > 20 dB setelah decode |
| Pure tone 1 kHz | Normal encode/decode | Output tidak clipping; SNR > 20 dB |
| White noise | Encode/decode 10 frame | Encoder tidak crash; output dalam range int16 |
| Silence (nol) | Dengan Annex B aktif | Encoder kirim SID frame (2 byte) atau untransmitted (0 byte) |
| Full-scale clip (+32767) | Stress test | Levinson tidak diverge; encoder tidak NaN/panic |
| Ramp signal | Stress test | Autocorr tidak Inf; LP coefficients stabil |
| Chirp (20→4000 Hz) | Transient | Encoder/decoder tidak crash sepanjang sweep |

```go
func TestSyntheticTone300Hz(t *testing.T) {
    pcm := loadPCM("testdata/synthetic/tone_300hz.pcm")
    enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
    dec := NewDecoder(VariantG729A)

    var signalE, noiseE float64
    bits := make([]byte, 10)
    out  := make([]int16, 80)

    for i := 0; i < len(pcm)/80; i++ {
        frame := pcm[i*80 : (i+1)*80]
        n, ft, err := enc.Encode(bits, frame)
        assert(t, err == nil && n == 10 && ft == FrameSpeech)
        dec.Decode(out, bits[:n])
        for j, s := range frame {
            signalE += float64(s) * float64(s)
            e := float64(out[j]) - float64(s)
            noiseE  += e * e
        }
    }
    snr := 10 * math.Log10(signalE/noiseE)
    if snr < 20.0 {
        t.Errorf("SNR tone 300Hz: %.2f dB (want >= 20)", snr)
    }
}
```

---

### Layer 3 — ITU-T Compliance Tests (Gate Utama)

**File:** `g729_test.go`

> **Penting — Implikasi float32 pada encoder compliance:**
> Test vector resmi ITU-T (`speech.in` → `algthm.bit`) digenerate oleh encoder **fixed-point**
> (menggunakan `basic_op.c`, saturasi integer, tabel lookup fixed-point).
> Encoder float32 — bahkan implementasi referensi resmi G.729 **Annex C** (floating-point) — **tidak
> pernah menghasilkan bitstream bit-exact** terhadap `algthm.bit`. Perbedaan pembulatan float pada
> pitch search atau codebook pulse selection akan memilih indeks berbeda di satu frame, yang
> kemudian mendivergensikan seluruh filter memory frame berikutnya.
>
> Konsekuensi:
> - **Decoder:** Bisa dan harus diuji bit-exact (±1 LSB) terhadap `algthm.bit` → `speech.ref`,
>   karena decoder hanya merekonstruksi dari bitstream yang diberikan.
> - **Encoder float32:** Gate compliance bukan bit-exact, melainkan **kualitas objektif**:
>   PESQ MOS-LQO ≥ 3.8, segmental SNR ≥ 20 dB, dan spectral distortion LSP < 1 dB.
>   Atau, gunakan output encoder **G.729 Annex C** (floating-point reference binary) sebagai
>   referensi komparasi pengganti `algthm.bit`.

#### 3a. Encoder Quality Compliance (menggantikan bit-exact yang tidak mungkin dicapai float32)

```go
// TestEncoderRoundTripQuality memvalidasi kualitas encoder float32 secara objektif.
// Tidak menggunakan bytes.Equal terhadap algthm.bit (yang digenerate fixed-point).
func TestEncoderRoundTripQuality(t *testing.T) {
    pcm := readPCM16("testdata/itu/speech.in")
    enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false}) // VAD off: selalu hasilkan speech frame 10 byte
    dec := NewDecoder(VariantG729A)

    var sigE, noiseE float64
    var segSNRSum float64
    segCount := 0
    bits := make([]byte, 10)
    out  := make([]int16, 80)

    for i := 0; i < len(pcm)/80; i++ {
        frame := pcm[i*80 : (i+1)*80]
        n, _, _ := enc.Encode(bits, frame) // EnableVAD=false: n selalu 10
        dec.Decode(out, bits[:n])

        var fSig, fNoise float64
        for j, s := range frame {
            fSig   += float64(s) * float64(s)
            e      := float64(out[j]) - float64(s)
            fNoise += e * e
            sigE   += float64(s) * float64(s)
            noiseE += e * e
        }
        if fSig > 0 {
            segSNRSum += 10 * math.Log10(fSig/fNoise)
            segCount++
        }
    }

    overallSNR := 10 * math.Log10(sigE / noiseE)
    segSNR     := segSNRSum / float64(segCount)

    t.Logf("Overall SNR:    %.2f dB (want >= 25)", overallSNR)
    t.Logf("Segmental SNR:  %.2f dB (want >= 20)", segSNR)

    if overallSNR < 25.0 {
        t.Errorf("overall SNR terlalu rendah: %.2f dB", overallSNR)
    }
    if segSNR < 20.0 {
        t.Errorf("segmental SNR terlalu rendah: %.2f dB", segSNR)
    }
}

// TestEncoderSpectralDistortion memvalidasi distorsi LSP quantization < 1 dB.
// Spectral distortion = rata-rata selisih log spectral antara LP original dan quantized.
func TestEncoderSpectralDistortion(t *testing.T) {
    pcm    := readPCM16("testdata/itu/speech.in")
    enc    := newEncoderInternal(VariantG729A) // akses internal untuk inspeksi LSP

    var sdSum float64
    count := 0
    for i := 0; i < len(pcm)/80; i++ {
        lspOrig, lspQuant := enc.encodeFrameWithLSP(pcm[i*80:(i+1)*80])
        sd := spectralDistortion(lspOrig, lspQuant) // dB
        sdSum += sd
        count++
    }
    avgSD := sdSum / float64(count)
    t.Logf("Average spectral distortion: %.3f dB (want < 1.0)", avgSD)
    if avgSD >= 1.0 {
        t.Errorf("spectral distortion terlalu tinggi: %.3f dB", avgSD)
    }
}

// TestEncoderVsAnnexCReference membandingkan encoder float32 kita terhadap
// output binary referensi G.729 Annex C (floating-point), bukan algthm.bit.
// Dijalankan hanya jika binary Annex C tersedia.
func TestEncoderVsAnnexCReference(t *testing.T) {
    if annexCBin == "" {
        t.Skip("G.729 Annex C binary tidak tersedia; set -annexc=/path/to/coder")
    }
    // Jalankan binary Annex C pada speech.in → annexc.bit
    // Bandingkan output kita terhadap annexc.bit (bisa bit-exact atau ±frame-level)
    // Karena keduanya float, kemungkinan match jauh lebih tinggi dari algthm.bit
}
```

#### 3b. Decoder ±1 LSB Test (tetap berlaku — decoder bisa diuji bit-exact)

```go
// TestITUDecoderPlusMinusOneLSB: decoder harus merekonstruksi output ±1 LSB
// dari speech.ref ketika diberi algthm.bit sebagai input.
// Ini valid karena decoder tidak bergantung pada algoritma encoder (fixed vs. float).
func TestITUDecoderPlusMinusOneLSB(t *testing.T) {
    frames := readBitstream("testdata/itu/algthm.bit")
    ref    := readPCM16("testdata/itu/speech.ref")
    dec    := NewDecoder(VariantG729A)

    maxDiff := 0
    for i, frame := range frames {
        got := make([]int16, 80)
        dec.Decode(got, frame)
        for j := range got {
            diff := abs(int(got[j]) - int(ref[i*80+j]))
            if diff > maxDiff { maxDiff = diff }
            if diff > 1 {
                t.Errorf("frame %d sample %d: diff=%d (got=%d want=%d)",
                    i, j, diff, got[j], ref[i*80+j])
            }
        }
    }
    t.Logf("max LSB diff: %d", maxDiff)
}
```

#### 3c. Tame Frame Test (decoder)
```go
// Tame frame = excitation nol; menguji decoder pada kondisi degenerate
func TestITUTameFrames(t *testing.T) {
    frames := readBitstream("testdata/itu/tame.bit")
    ref    := readPCM16("testdata/itu/tamede.ref")
    dec    := NewDecoder(VariantG729A)
    // Toleransi sama: ±1 LSB
}
```

#### 3d. G.729 Full Compliance
```go
// Encoder Full: quality gate sama (SNR ≥ 25 dB, segSNR ≥ 20 dB)
func TestEncoderFullRoundTripQuality(t *testing.T)
// Decoder Full: bit-exact ±1 LSB terhadap referensi decoder G.729 Full
func TestITUDecoderFullPlusMinusOneLSB(t *testing.T)
```

---

### Layer 4 — Round-Trip Quality Metrics

Setelah compliance lulus, ukur kualitas perseptual secara objektif.

#### SNR (Signal-to-Noise Ratio)
```go
func TestRoundTripSNR(t *testing.T) {
    pcm := readPCM16("testdata/itu/speech.in")
    enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
    dec := NewDecoder(VariantG729A)

    var sigE, noiseE float64
    bits := make([]byte, 10)
    out  := make([]int16, 80)

    for i := 0; i < len(pcm)/80; i++ {
        frame := pcm[i*80 : (i+1)*80]
        n, _, _ := enc.Encode(bits, frame)
        dec.Decode(out, bits[:n])
        for j, s := range frame {
            sigE   += float64(s) * float64(s)
            e      := float64(out[j]) - float64(s)
            noiseE += e * e
        }
    }
    snr := 10 * math.Log10(sigE / noiseE)
    t.Logf("Round-trip SNR: %.2f dB")
    // G.729A clean speech: typical SNR ~25-30 dB
    if snr < 25.0 {
        t.Errorf("SNR terlalu rendah: %.2f dB (want >= 25)", snr)
    }
}
```

#### PESQ via External Binary
```go
// TestPESQ menjalankan PESQ binary ITU-T P.862 dan memvalidasi MOS-LQO score.
// Dieksekusi hanya jika binary tersedia: go test -run TestPESQ -pesq=/path/to/pesq
func TestPESQ(t *testing.T) {
    if pesqBin == "" { t.Skip("pesq binary tidak tersedia") }

    // 1. Encode speech.in → decoded.pcm (encode lalu decode)
    // 2. exec: pesq +8000 speech.in decoded.pcm
    // 3. Parse output: "MOS-LQO: 3.92"
    // G.729A referensi ITU-T: MOS-LQO ≈ 3.9
    if score < 3.7 {
        t.Errorf("PESQ MOS-LQO: %.2f (want >= 3.7)", score)
    }
}
```

---

### Layer 5 — Fuzz Testing (Decoder Robustness)

Decoder harus tahan semua input arbitrer — tidak boleh panic, crash, atau loop tak terbatas.

```go
// go test -fuzz=FuzzDecode -fuzztime=5m
func FuzzDecode(f *testing.F) {
    // Seed corpus: frame valid speech (10b), SID (2b), dan PLC (0b)
    f.Add([]byte{})
    f.Add([]byte{0x00, 0x00})
    for _, frame := range readBitstream("testdata/itu/algthm.bit")[:10] {
        f.Add(frame)
    }

    f.Fuzz(func(t *testing.T, data []byte) {
        dec := NewDecoder(VariantG729A)
        out := make([]int16, 80)

        // Tidak boleh panic apapun input-nya (len 0 PLC, len 2 SID, len 10 speech, atau corrupted length)
        _ = dec.Decode(out, data)

        // Output harus dalam range int16 yang valid
        for _, s := range out {
            if int(s) > 32767 || int(s) < -32768 {
                t.Errorf("output out of range: %d", s)
            }
        }
    })
}

// Fuzz encoder juga: input PCM arbitrer tidak boleh crash
func FuzzEncode(f *testing.F) {
    f.Fuzz(func(t *testing.T, data []byte) {
        if len(data) < 160 { return } // 80 sample × 2 byte
        pcm := make([]int16, 80)
        for i := range pcm { pcm[i] = int16(binary.LittleEndian.Uint16(data[i*2:])) }
        enc := NewEncoder(DefaultConfig())
        dst := make([]byte, 10)
        _, _, _ = enc.Encode(dst, pcm)
    })
}
```

---

### Layer 6 — Regression Tests (Golden Files)

Setelah compliance ITU tercapai, simpan output sebagai **golden files** untuk mendeteksi regresi saat refactor.

```go
var updateGolden = flag.Bool("update-golden", false, "regenerasi golden files")

func TestGoldenEncoderG729A(t *testing.T) {
    pcm := readPCM16("testdata/itu/speech.in")
    enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})

    var got []byte
    bits := make([]byte, 10)
    for i := 0; i < len(pcm)/80; i++ {
        n, _, _ := enc.Encode(bits, pcm[i*80:(i+1)*80])
        got = append(got, bits[:n]...)
    }

    if *updateGolden {
        os.WriteFile("testdata/golden/g729a_encoder.bit", got, 0644)
        return
    }
    want, _ := os.ReadFile("testdata/golden/g729a_encoder.bit")
    if !bytes.Equal(got, want) {
        t.Error("output berbeda dari golden — ada regresi")
    }
}
```

Golden file diupdate dengan: `go test -run TestGolden -update-golden`

---

### Layer 7 — Performance Benchmarks & Profiling

**File:** `bench_test.go` (terpisah dari `g729_test.go` agar tidak lambatkan `go test ./...`)

---

#### 7a. Metrik Utama: Real-Time Factor (RTF)

RTF adalah metrik terpenting untuk codec real-time:

```
RTF = waktu_proses / durasi_audio
    = T_encode / 10 ms

RTF < 1.0  → real-time capable
RTF < 0.01 → 100× headroom (target kita)
```

Pada G.729A, frame = 10 ms audio. Target encode < 100 µs → RTF = 0.01 (100× headroom).
Ini memberi ruang untuk overhead jaringan, jitter buffer, dan pemrosesan lain di stack VoIP.

```go
func BenchmarkRTF(b *testing.B) {
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        enc   := NewEncoder(DefaultConfig())
        input := make([]int16, 80)
        dst   := make([]byte, 10)
        for pb.Next() {
            _, _, _ = enc.Encode(dst, input)
        }
    })
    // b.Elapsed() / b.N = ns/op; RTF = ns/op / 10_000_000 ns
    nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
    rtf := nsPerOp / 10_000_000 // 10ms = 10,000,000 ns
    b.ReportMetric(rtf, "RTF")
    b.ReportMetric(nsPerOp/1000, "µs/frame")
}
```

---

#### 7b. End-to-End Benchmarks

```go
// bench_test.go

var (
    benchInput = makeSpeechFrames(100) // 100 frame pre-generated dari speech.in
    benchFrame = loadValidFrame()      // 1 frame valid dari algthm.bit
)

// --- Encoder ---

func BenchmarkEncodeG729A(b *testing.B) {
    enc := NewEncoder(DefaultConfig())
    dst := make([]byte, 10)
    b.ReportAllocs()
    b.SetBytes(160) // 80 sample × 2 byte input
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _, _ = enc.Encode(dst, benchInput[i%100])
    }
}
// Target: < 50 µs/op, 0 allocs/op, throughput > 3 MB/s

func BenchmarkEncodeG729Full(b *testing.B) {
    enc := NewEncoder(Config{Variant: VariantG729, EnableVAD: false})
    dst := make([]byte, 10)
    b.ReportAllocs()
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _, _ = enc.Encode(dst, benchInput[i%100])
    }
}
// Target: < 80 µs/op (4-pulse search lebih berat)

// --- Decoder ---

func BenchmarkDecodeG729A(b *testing.B) {
    dec := NewDecoder(VariantG729A)
    dst := make([]int16, 80)
    b.ReportAllocs()
    b.SetBytes(10) // 10 byte input
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        dec.Decode(dst, benchFrame)
    }
}
// Target: < 30 µs/op, 0 allocs/op

// --- Reset overhead ---

func BenchmarkReset(b *testing.B) {
    enc := NewEncoder(DefaultConfig())
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        enc.Reset()
    }
}
// Reset harus O(1) — hanya zeroing struct fields
```

---

#### 7c. Micro-Benchmarks per Komponen DSP

Setiap komponen diukur secara independen untuk mengidentifikasi bottleneck.
**File:** `internal/dsp/bench_test.go`, `internal/codebook/bench_test.go`, dst.

```go
// --- DSP ---

func BenchmarkAutocorr(b *testing.B) {
    speech := make([]float32, 240)
    r      := make([]float32, 11)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        Autocorr(r, speech, tables.Window240, 10)
    }
}
// Operasi: 240×11 = 2640 multiply-add; target < 2 µs

func BenchmarkLevinson(b *testing.B) {
    r := [11]float32{1, 0.9, 0.81, 0.729, 0.656, 0.59, 0.531, 0.478, 0.430, 0.387, 0.349}
    a := [10]float32{}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        LevinsonInPlace(&a, r[:], 10)
    }
}
// 10 iterasi × 10 inner ops = 100 ops; target < 500 ns

func BenchmarkSynthesisFilter(b *testing.B) {
    excitation := [40]float32{}
    a          := [10]float32{}
    mem        := [10]float32{}
    out        := [40]float32{}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        SynthesisFilter(out[:], excitation[:], &a, &mem)
    }
}
// 40×10 = 400 multiply-add; target < 1 µs

func BenchmarkConvolution(b *testing.B) {
    h   := [40]float32{}
    x   := [40]float32{}
    out := [40]float32{}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        Convolution(out[:], h[:], x[:])
    }
}
// 40×40/2 = 800 multiply-add (triangular); target < 2 µs

// --- LSP ---

func BenchmarkLPC2LSP(b *testing.B) {
    a := [10]float32{0.1,-0.2,0.15,-0.1,0.05,-0.03,0.02,-0.01,0.005,-0.002}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        LPC2LSP(&a)
    }
}
// 60-pt grid + bisection; target < 5 µs

func BenchmarkQuantizeLSP(b *testing.B) {
    // VQ search 128 + 32 + 32 entries
    // target < 3 µs
}

// --- Pitch ---

func BenchmarkOpenLoopPitch(b *testing.B) {
    residual := make([]float32, 240)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        OpenLoopPitch(residual)
    }
}
// Search 124 lags × 240 ops = 29760 ops; target < 15 µs

func BenchmarkClosedLoopPitch(b *testing.B) {
    target  := [40]float32{}
    impulse := [40]float32{}
    excBuf  := make([]float32, 185)
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        ClosedLoopPitch(target[:], impulse[:], excBuf, 60, nil, nil)
    }
}
// ±3 integer atau ±1 frac search; target < 8 µs

// --- Codebook (bottleneck utama encoder) ---

func BenchmarkSearchAlgebraicA(b *testing.B) {
    target  := [40]float32{}
    impulse := [40]float32{}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        SearchAlgebraicA(target[:], impulse[:], nil, nil)
    }
}
// G.729A fast heuristic: 4 pulsa, pair-wise search (8×8 + 8×16) dengan Dₙ pruning
// Jauh lebih cepat dari 8×8×8×16=8192 kombinasi exhaustive; target < 20 µs

func BenchmarkSearchAlgebraicFull(b *testing.B) {
    // 4-pulse: jauh lebih berat; target < 60 µs
}

func BenchmarkBuildPhiMatrix(b *testing.B) {
    h   := [40]float32{}
    phi := [40][40]float32{}
    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        buildPhiMatrix(&phi, h[:])
    }
}
// 40×40/2 = 800 ops; dihitung sekali per subframe
```

**Tabel target per komponen:**

| Komponen | Target | Persentase dari 50 µs budget |
|---|---|---|
| `Autocorr` | < 2 µs | 4% |
| `Levinson` | < 0.5 µs | 1% |
| `LPC2LSP` | < 5 µs | 10% |
| `QuantizeLSP` | < 3 µs | 6% |
| `OpenLoopPitch` | < 15 µs | 30% |
| `ClosedLoopPitch` (×2 subfr) | < 8 µs | 16% |
| `SearchAlgebraicA` (×2 subfr) | < 20 µs | 40% |
| `QuantizeGain` (×2 subfr) | < 1 µs | 2% |
| Filter + pack/unpack | < 1 µs | 2% |
| **Total G.729A** | **< 50 µs** | 100% |

---

#### 7d. Concurrency Benchmark

Mengukur throughput saat N goroutine encode/decode secara paralel (tiap goroutine punya instance sendiri).

```go
func BenchmarkConcurrentEncode(b *testing.B) {
    for _, n := range []int{1, 2, 4, 8, 16} {
        b.Run(fmt.Sprintf("goroutines=%d", n), func(b *testing.B) {
            b.SetParallelism(n)
            b.RunParallel(func(pb *testing.PB) {
                enc := NewEncoder(DefaultConfig()) // satu enc per goroutine
                dst := make([]byte, 10)
                inp := make([]int16, 80)
                for pb.Next() {
                    _, _, _ = enc.Encode(dst, inp)
                }
            })
        })
    }
}
// Ekspektasi: throughput mendekati linear dengan jumlah goroutine
// (tidak ada shared state antar instance)
```

---

#### 7e. Memory Footprint Benchmark

```go
func BenchmarkMemoryFootprint(b *testing.B) {
    b.ReportAllocs()
    for i := 0; i < b.N; i++ {
        enc := NewEncoder(DefaultConfig())
        dec := NewDecoder(VariantG729A)
        _ = enc
        _ = dec
    }
}
// NewEncoder/NewDecoder boleh alloc (konstruktor), tapi hanya 1 alloc per instance
// Ukuran encoder struct target: < 8 KB (masuk L1 cache)

func TestEncoderStructSize(t *testing.T) {
    enc := &encoder{}
    size := unsafe.Sizeof(*enc)
    t.Logf("encoder struct size: %d bytes", size)
    if size > 8192 {
        t.Errorf("encoder terlalu besar: %d bytes (want < 8192)", size)
    }
}
```

---

#### 7f. CPU Profiling Workflow

Gunakan `pprof` untuk mengidentifikasi fungsi bottleneck setelah implementasi selesai.

```bash
# 1. Generate CPU profile
go test -bench=BenchmarkEncodeG729A -benchtime=10s \
    -cpuprofile=cpu.prof ./...

# 2. Analisis interaktif
go tool pprof cpu.prof
(pprof) top10          # 10 fungsi terberat
(pprof) list SearchAlgebraicA  # line-by-line breakdown
(pprof) web            # buka flame graph di browser

# 3. Generate flame graph (butuh graphviz)
go tool pprof -pdf cpu.prof > flame.pdf
```

**Ekspektasi hot path** berdasarkan algoritma:
```
SearchAlgebraicA / SearchAlgebraicFull   ~40-50% CPU
OpenLoopPitch                            ~20-25% CPU
Autocorr + Levinson                      ~10% CPU
ClosedLoopPitch                          ~10-15% CPU
Sisanya (LSP, filter, gain, pack)        ~10% CPU
```

Jika profil tidak sesuai ekspektasi ini, ada implementasi yang perlu dioptimasi.

---

#### 7g. Memory Profiling

```bash
# Generate heap profile saat encoding 1000 frame
go test -bench=BenchmarkEncodeG729A -benchtime=1000x \
    -memprofile=mem.prof ./...

go tool pprof mem.prof
(pprof) top            # alokasi terbesar
(pprof) list Encode    # harus 0 allocs di Encode()
```

Target: `0 allocs/op` pada `Encode()` dan `Decode()`. Setiap alloc yang muncul adalah bug.

---

#### 7h. Regression Benchmark dengan `benchstat`

Gunakan `benchstat` untuk membandingkan performa sebelum dan sesudah perubahan kode.

```bash
# Install benchstat
go install golang.org/x/perf/cmd/benchstat@latest

# Sebelum refactor: simpan baseline
go test -bench=. -benchmem -count=10 ./... > before.txt

# Setelah refactor: ukur lagi
go test -bench=. -benchmem -count=10 ./... > after.txt

# Bandingkan: tampilkan delta statistik
benchstat before.txt after.txt
```

Output contoh:
```
name               old time/op    new time/op    delta
EncodeG729A-8        48.2µs ± 2%    31.5µs ± 1%  -34.6%  (p=0.000 n=10+10)
DecodeG729A-8        28.7µs ± 3%    27.1µs ± 2%   -5.6%  (p=0.003 n=10+10)
SearchAlgebraicA-8   19.8µs ± 1%    12.3µs ± 2%  -37.9%  (p=0.000 n=10+10)

name               old allocs/op  new allocs/op  delta
EncodeG729A-8          0.00           0.00         ~     (all equal)
```

`benchstat` menggunakan Mann-Whitney U-test untuk memastikan delta signifikan secara statistik (`-count=10` minimum untuk hasil valid).

---

#### 7i. Benchmark Gate di CI

Tambahkan benchmark gate ke CI pipeline agar regresi performa terdeteksi otomatis:

```yaml
# .github/workflows/bench.yml
- name: Run benchmarks
  run: |
    go test -bench=. -benchmem -count=5 ./... > bench_result.txt
    
- name: Check performance gate
  run: |
    # Parse ns/op dari output benchmark, assert < threshold
    go run ./cmd/benchcheck \
      --file=bench_result.txt \
      --gate="BenchmarkEncodeG729A=50000"  \  # 50000 ns = 50 µs
      --gate="BenchmarkDecodeG729A=30000"  \
      --gate="BenchmarkSearchAlgebraicA=20000"
```

`cmd/benchcheck` adalah tool kecil dalam repo yang mem-parse output `go test -bench` dan exit non-zero jika ada gate yang terlampaui.

---

#### 7j. Platform-Specific Targets

| Platform | Encode G.729A | Decode G.729A | Catatan |
|---|---|---|---|
| amd64 (modern) | < 50 µs | < 30 µs | Target utama |
| ARM64 (Apple M-series) | < 60 µs | < 35 µs | Float32 NEON efficient |
| ARM64 (server, Graviton) | < 80 µs | < 45 µs | |
| WASM (browser) | < 200 µs | < 100 µs | Acceptable untuk WebRTC |

Test lintas platform menggunakan `GOARCH=arm64 GOOS=linux go test -bench=.` atau GitHub Actions matrix.

---

#### Ringkasan Perintah Benchmark

```bash
# End-to-end benchmark + alokasi
go test -bench=. -benchmem -benchtime=5s ./...

# Benchmark spesifik dengan count statistik
go test -bench=BenchmarkEncodeG729A -count=10 -benchmem . > result.txt

# CPU profiling
go test -bench=BenchmarkEncodeG729A -cpuprofile=cpu.prof .
go tool pprof -http=:6060 cpu.prof

# Memory profiling
go test -bench=BenchmarkEncodeG729A -memprofile=mem.prof .
go tool pprof -http=:6060 mem.prof

# Bandingkan dua versi
benchstat before.txt after.txt

# RTF report
go test -bench=BenchmarkRTF -v . | grep RTF
```

---

### Urutan Eksekusi Testing per Fase

| Fase Implementasi | Test yang Dijalankan | Gate |
|---|---|---|
| Fase 1 (Bitstream) | `TestBitstreamRoundTrip`, `TestBitstreamFieldBoundaries` | Wajib lulus |
| Fase 2 (DSP) | `TestLevinson*`, `TestHighPass*`, `TestAutocorr*` | Wajib lulus |
| Fase 3 (LSP) | `TestLPCLSP*`, `TestLSPQuantize*` | Wajib lulus |
| Fase 4 (Pitch) | `TestOpenLoopPitch*`, `TestParityBit`, `TestSincInterp*` | Wajib lulus |
| Fase 5 (Codebook) | `TestCodebookSearch*`, `TestCodeVector*` | Wajib lulus |
| Fase 6 (Gain) | `TestGainQuantize*` | Wajib lulus |
| Fase 7-8 (Filter+VAD) | `TestPostFilter*`, `TestVAD*`, `TestDTX*` | Wajib lulus |
| Fase 9-10 (Assembly) | Synthetic signal tests, SNR round-trip | Wajib lulus |
| Fase 11 (Integration) | **ITU-T Compliance (Layer 3):** decoder ±1 LSB (bit-exact) + encoder SNR/PESQ/spectral distortion | **Hard gate** |
| Post-compliance | PESQ MOS-LQO ≥ 3.8, Fuzz 5 menit, Golden file snapshot | Wajib lulus |
| Pre-release | Benchmark gate: RTF < 0.01, 0 allocs/op; `benchstat` vs. baseline | Wajib lulus |

### Perintah Lengkap Testing & Benchmarking

```bash
# Unit + compliance (semua paket)
go test ./...

# Dengan race detector
go test -race ./...

# Benchmark end-to-end + alokasi
go test -bench=. -benchmem -benchtime=5s ./...

# Benchmark spesifik dengan sampling statistik (butuh benchstat)
go test -bench=BenchmarkEncodeG729A -count=10 -benchmem . > result.txt

# Bandingkan dua versi (regresi performa)
benchstat before.txt after.txt

# CPU profiling → flame graph
go test -bench=BenchmarkEncodeG729A -cpuprofile=cpu.prof .
go tool pprof -http=:6060 cpu.prof

# Memory profiling → validasi zero-alloc
go test -bench=BenchmarkEncodeG729A -memprofile=mem.prof .
go tool pprof -http=:6060 mem.prof

# RTF report
go test -bench=BenchmarkRTF -v . | grep -E "RTF|µs/frame"

# Fuzz decoder (CI: 1 menit; local: 5 menit+)
go test -fuzz=FuzzDecode -fuzztime=1m .

# Update golden files setelah compliance lulus
go test -run TestGolden -update-golden .

# PESQ (jika binary tersedia)
go test -run TestPESQ -pesq=/usr/local/bin/pesq .
```

---

## Optimisasi Go-Specific

| Teknik | Detail |
|---|---|
| Zero allocation | Semua buffer di-pre-alloc di struct encoder/decoder; validasi dengan `go test -benchmem` |
| Fixed-size array params | `*[40]float32` bukan `[]float32` di hot path untuk BCE (bounds-check elimination) |
| Φ matrix reuse | Hitung satu kali per subframe, pakai untuk semua kandidat pulse position |
| excBuf shift | `copy(excBuf[0:154], excBuf[80:234])` setelah tiap frame (80 sample), hindari modulo index |
| speechBuf shift | `copy(speechBuf[0:160], speechBuf[80:240])` setelah tiap frame untuk jendela analisis 240 sampel + lookahead |
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
