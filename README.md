# go-g729

High-performance, pure Go implementation of the **ITU-T G.729** speech codec, including **G.729 Annex A** (reduced-complexity ACELP), **Full G.729** (nested search and adaptive perceptual weighting), and **Annex B** (Voice Activity Detection / Discontinuous Transmission / Comfort Noise Generation).

[![Go Reference](https://pkg.go.dev/badge/github.com/selawe/go-g729.svg)](https://pkg.go.dev/github.com/selawe/go-g729)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Zero Allocations](https://img.shields.io/badge/Allocations-0%20allocs%2Fop-brightgreen.svg)]()
[![Pure Go](https://img.shields.io/badge/Pure%20Go-100%25-blue.svg)]()

---

## Fitur Utama

- **100% Pure Go (Zero CGO):** Kompilasi silang (*cross-compilation*) instan ke semua platform target: Windows, Linux, macOS, Android, iOS, ARM64 (Graviton, Apple Silicon), dan WebAssembly (WASM).
- **Zero Allocations pada Hot Paths (`0 B/op`, `0 allocs/op`):** Seluruh loop encoding dan decoding bekerja sepenuhnya menggunakan buffer statis internal tanpa memicu alokasi heap atau overhead *Garbage Collector* (GC).
- **Super Fast (> 200x Real-Time Encoding, > 1000x Real-Time Decoding):**
  - Encoding 1 frame (10 ms audio) hanya membutuhkan **~38 µs** (G.729A) atau **~45 µs** (Full G.729).
  - Decoding 1 frame hanya membutuhkan **~10 µs**.
- **Kepatuhan Spesifikasi Resmi ITU-T:**
  - **G.729A:** Algoritma pencarian ACELP berpasangan (*fast pair-wise heuristic search*) dan pembobotan perseptual teroptimasi.
  - **Full G.729:** Pencarian 4-loop bersarang (*nested exhaustive search*) dan pembobotan adaptif spektral/harmonik.
  - **Annex B (VAD / DTX / CNG):** Voice Activity Detection (energi, zero-crossing, spectral tilt, noise tracking), Discontinuous Transmission (SID frame 16-bit per RFC 3551), dan Comfort Noise Generator (Gaussian pseudo-random excitation).
  - **Terverifikasi Vektor Uji Resmi ITU-T:** Decoder teruji menghasilkan **SNR > 20 dB** terhadap file referensi resmi `TEST.pst`.
- **Packet Loss Concealment (PLC) Terintegrasi:** Rekonstruksi otomatis saat terjadi frame drop dengan ekstrapolasi lag pitch, pelemahan gain ($0.90$ pitch gain, $0.98$ codebook gain), eksitasi acak, dan muting bertahap pada kehilangan beruntun (> 60 ms).
- **Thread-Safe & Aman dari Race Condition:** Semua state codec disimpan dalam struct instance mandiri (`Encoder` dan `Decoder`), tanpa state mutabel tingkat paket. Aman dijalankan bersamaan di ribuan goroutine paralel.
- **Dukungan Format WAV & Raw PCM:** Terintegrasi deteksi dan parsing header RIFF/WAVE (16-bit linear PCM mono 8000 Hz) serta streaming raw byte.

---

## Arsitektur Paket

```
go-g729/
├── types.go                 # Interface publik: Encoder, Decoder, Config, FrameType, dan Sentinel Errors
├── encoder.go               # Implementasi pipeline assembly encoder G.729 / G.729A / Annex B
├── decoder.go               # Implementasi pipeline assembly decoder, PLC, dan Comfort Noise
├── encoder_test.go          # Unit & benchmark test suite untuk Encoder
├── decoder_test.go          # Unit & benchmark test suite untuk Decoder
├── g729_test.go             # End-to-end integration test & verifikasi vektor resmi ITU-T
├── cmd/
│   └── g729tool/            # Aplikasi CLI mandiri untuk encode/decode file audio WAV/PCM
└── internal/
    ├── bits/                # Serialisasi bitstream 80-bit speech frame & 16-bit SID frame (RFC 3551)
    ├── codebook/            # Algebraic codebook search (fast & full), gain quantization, taming
    ├── dsp/                 # High-pass filter 140Hz/100Hz, Levinson-Durbin, autocorrelation, konvolusi
    ├── filter/              # Perceptual weighting filter W(z) dan adaptive postfilter (formant+tilt+AGC)
    ├── lsp/                 # Konversi LPC↔LSP, stabilisasi LSF, kuantisasi vektor MA 2-tahap
    ├── params/              # Konstanta numerik dan parameter algoritma ITU-T G.729
    ├── pitch/               # Open-loop pitch, closed-loop fractional pitch (1/3), sinc interpolation, paritas
    ├── tables/              # Tabel konstan lookup resmi ITU-T (lspcb, grid, gain, sinc, vad)
    └── vad/                 # Annex B: VAD metric calculation, DTX state machine, dan CNG synthesis
```

---

## Performa & Benchmark

Hasil benchmark diukur pada prosesor **AMD Ryzen 9 5900HX** (16 thread, Windows 11, Go 1.26):

| Operasi | Waktu / Frame (10 ms) | Alokasi Memori | Kecepatan Real-Time |
|---|---|---|---|
| **Encode G.729A (Fast)** | **38.6 µs** | **0 B/op, 0 allocs/op** | **~259x Real-Time** |
| **Encode G.729A + Annex B VAD** | **37.4 µs** | **0 B/op, 0 allocs/op** | **~267x Real-Time** |
| **Encode Full G.729** | **45.6 µs** | **0 B/op, 0 allocs/op** | **~219x Real-Time** |
| **Decode Active Speech** | **10.5 µs** | **0 B/op, 0 allocs/op** | **~952x Real-Time** |
| **Decode Comfort Noise (SID)** | **11.1 µs** | **0 B/op, 0 allocs/op** | **~900x Real-Time** |
| **Decode Packet Loss (PLC)** | **8.3 µs** | **0 B/op, 0 allocs/op** | **~1204x Real-Time** |

> [!NOTE]
> Semua fungsi pemrosesan utama memiliki footprint memori konstan tanpa alokasi dinamis pada hot path, sehingga aman digunakan pada aplikasi throughput tinggi seperti VoIP server, SIP gateway, dan WebRTC media bridge.

---

## Instalasi

### Sebagai Library Go
```bash
go get github.com/selawe/go-g729
```

### Sebagai CLI Tool
```bash
go install github.com/selawe/go-g729/cmd/g729tool@latest
```

---

## Penggunaan Library

### 1. Encoding Audio PCM ke G.729
Input audio harus berupa **16-bit linear PCM mono dengan sample rate 8000 Hz** (80 sampel = 10 ms per frame).

```go
package main

import (
	"fmt"
	"log"

	"github.com/selawe/go-g729"
)

func main() {
	// Konfigurasi standar: G.729A dengan Annex B VAD aktif
	cfg := g729.DefaultConfig()
	enc := g729.NewEncoder(cfg)

	// Buffer input (80 int16) dan buffer output bitstream (minimal 10 byte)
	pcmFrame := make([]int16, 80)
	bitstream := make([]byte, 10)

	// Isi pcmFrame dari stream audio Anda...

	n, frameType, err := enc.Encode(bitstream, pcmFrame)
	if err != nil {
		log.Fatalf("encode error: %v", err)
	}

	switch frameType {
	case g729.FrameSpeech:
		// Active speech: kirim 10 byte bitstream
		fmt.Printf("Speech frame: %d bytes\n", n)
	case g729.FrameSID:
		// Comfort Noise SID: kirim 2 byte bitstream (Annex B)
		fmt.Printf("SID frame: %d bytes\n", n)
	case g729.FrameUntransmitted:
		// Periode hening: tidak ada byte yang perlu dikirim (0 byte)
		fmt.Println("Untransmitted silence frame")
	}
}
```

### 2. Decoding G.729 ke Audio PCM
Decoder secara otomatis mendeteksi ukuran frame yang diterima:
- `10 byte`: Frame percakapan normal
- `2 byte`: Frame SID (Comfort Noise)
- `0 byte` atau `nil`: Frame hilang (*packet loss*) atau hening untransmitted

```go
package main

import (
	"fmt"
	"log"

	"github.com/selawe/go-g729"
)

func main() {
	dec := g729.NewDecoder()

	decodedPCM := make([]int16, 80)

	// Contoh 1: Decode frame 10 byte normal
	var frameBytes [10]byte
	if err := dec.Decode(decodedPCM, frameBytes[:]); err != nil {
		log.Fatalf("decode speech error: %v", err)
	}

	// Contoh 2: Decode frame hilang (Packet Loss Concealment / PLC)
	// Masukkan nil atau slice kosong untuk memicu PLC
	if err := dec.Decode(decodedPCM, nil); err != nil {
		log.Fatalf("decode PLC error: %v", err)
	}

	fmt.Println("Berhasil merekonstruksi 80 sampel PCM.")
}
```

---

## Aplikasi CLI (`g729tool`)

`g729tool` adalah program baris perintah serbaguna untuk encoding dan decoding file audio PCM mentah maupun file WAV standar.

### Syntax
```text
g729tool [opsi] <input_file> <output_file>
```

### Opsi CLI
| Flag | Tipe | Default | Keterangan |
|---|---|---|---|
| `-e` | bool | false | Mode Encode (PCM/WAV $\to$ G.729 bitstream) |
| `-d` | bool | false | Mode Decode (G.729 bitstream $\to$ PCM/WAV) |
| `-full` | bool | false | Menggunakan Full G.729 (nested search) alih-alih G.729A |
| `-vad` | bool | false | Mengaktifkan Annex B VAD/DTX/CNG |
| `-loss` | float | 0.0 | Simulasi rasio packet loss saat decode (0.0 s/d 1.0) |
| `-wav` | bool | false | Memaksa format output berupa WAV saat decode |
| `-bench` | bool | false | Menampilkan statistik kecepatan dan Real-Time Factor (RTF) |

### Contoh Penggunaan CLI

```bash
# 1. Encode file WAV ke G.729 bitstream (8 kbps CBR)
g729tool -e input.wav output.g729

# 2. Encode dengan Annex B Voice Activity Detection (VBR dengan hening terkompresi)
g729tool -e -vad input.wav output.g729

# 3. Decode file G.729 kembali ke WAV 16-bit 8000 Hz
g729tool -d input.g729 output.wav

# 4. Decode dengan simulasi 5% packet loss concealment (PLC)
g729tool -d -loss 0.05 input.g729 reconstructed.wav

# 5. Mengukur benchmark performa encode dengan Full G.729
g729tool -bench -full -e speech.wav speech.g729
```

---

## Menjalankan Pengujian

Jalankan seluruh test suite unit dan integrasi:
```bash
go test -v ./...
```

Jalankan benchmark performa dan verifikasi nol alokasi heap:
```bash
go test -run=^$ -bench Benchmark -benchmem .
```

---

## Lisensi

Proyek ini dilisensikan di bawah lisensi MIT. Tabel konstanta algoritma diturunkan sesuai spesifikasi teknis rekomendasi ITU-T G.729.
