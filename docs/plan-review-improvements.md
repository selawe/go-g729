# Plan: Review Improvements & Feature Roadmap

## Context

Hasil review menyeluruh terhadap project go-g729 (semua 11 fase codec selesai, semua test pass,
0 goroutine di production code). Dokumen ini mencatat perbaikan kualitas/keamanan/performa
serta fitur baru yang layak dipertimbangkan.

Urutan pengerjaan: dari prioritas tinggi ke rendah, **satu commit per item**, dengan test
verifikasi sebelum commit.

---

## Prioritas Tinggi (Security & Correctness)

### 1. Opt-in `debug.Stack()` di error panic recovery
**File:** `encoder.go:154`, `decoder.go:127`

**Masalah:** Error `ErrInternalPanic` selalu berisi full `debug.Stack()` termasuk internal
package path. Jika error di-propagate ke response HTTP/API, informasi build path bocor
(information disclosure).

**Rencana:**
- Encoder: tambahkan `Config.IncludePanicStack bool` (default `false`). Saat `false`, error
  hanya berisi pesan singkat tanpa `debug.Stack()`. `DisablePanicRecovery` tetap ada untuk
  developer yang butuh crash langsung.
- Decoder: tambahkan struct `DecoderConfig` (baru) dengan field yang setara. Provide
  `NewDecoderWithConfig(cfg DecoderConfig)` supaya `NewDecoder()` yang existing tetap kompatibel.
  Default: recovery aktif, `IncludePanicStack=false`.
- Test: verifikasi pesan error tidak berisi "stack:" saat opt-out; tetap berisi saat opt-in.

**Status:** ✅ Selesai (commit `3b56537`)

---

### 2. `Reader.Read` menyimpan `pendingErr` untuk decode error
**File:** `stream.go:241-246`

**Masalah:** Jika `dec.Decode` error di tengah stream setelah beberapa byte sudah di-serve,
error dibuang (`return totalRead, nil`) dan caller tidak pernah tahu.

**Rencana:**
- Tambah field `pendingErr error` di struct `Reader`.
- Saat decode error dengan `totalRead > 0`: return `(totalRead, nil)` **tapi** simpan err di `pendingErr`.
- Pada call `Read` berikutnya, jika `pendingErr != nil` dan `bufHead >= bufTail`: return `(0, pendingErr)` dan clear.
- Test: encode 3 frames, corrupt frame ke-2, verify Read pertama return N>0 nil, Read kedua return decode error.

**Status:** ✅ Selesai (commit `aa71940`)

---

### 3. Batasi ukuran input `sdp.ParseFMTPParams` (DoS protection)
**File:** `sdp/sdp.go:131`

**Masalah:** `strings.Split(fmtp, ";")` tanpa cap. Attacker bisa kirim SDP fmtp 10 MB → alokasi
slice besar sebelum validasi.

**Rencana:**
- Tambah constant `MaxFMTPLength = 1024` (cukup untuk RFC use case realistic).
- Return `ErrInvalidValue` (dengan pesan "input too long") jika `len(fmtp) > MaxFMTPLength`.
- Test: input 2 KB harus ditolak.

**Status:** ✅ Selesai (commit `37e968a`)

---

### 4. `DecoderStats.ParityErrors` counter
**File:** `types.go`, `decoder.go:153`

**Masalah:** Decoder detect parity error di P0/P1 pitch delay tetapi tidak melaporkannya
ke telemetry. Berguna untuk deteksi bit-error rate di jaringan.

**Rencana:**
- Tambah field `ParityErrors int` di `DecoderStats`.
- Increment di jalur `parityErr != 0`.
- Test: buat frame dengan parity dirusak (flip bit P0), decode, verify counter naik.

**Status:** ✅ Selesai (commit `40bd585`)

---

## Prioritas Menengah (Performance & Hardening)

### 5. `jitter.PopInto` — buffered count incremental
**File:** `jitter/jitter.go:229-234`

**Masalah:** Scan seluruh 128 slot O(N) tiap Pop.

**Rencana:**
- Track `bufferedCount int` di struct `Buffer`.
- Increment saat Push mengisi slot valid baru, decrement saat Pop.
- Handle wrap: saat `DroppedByWrap` (overwrite slot valid dengan seq baru), count tetap.
- Test: verifikasi `Stats().CurrentBuffered` sama antara before dan after refactor.

**Status:** ✅ Selesai (commit `9dc37c9`)

---

### 6. Levinson NaN/pathological telemetry
**File:** `internal/dsp/levinson.go:56, 90`

**Masalah:** Silent fallback `err64 = 0.001` saat `err64 <= 0`. Bisa menutup bug numerical.

**Rencana:**
- Ekspose flag lewat return atau via package-level counter (via `sync/atomic`).
- Tambah di `DiagnosticStats.LPCFallback bool` untuk sinyal per-frame.
- Test: input yang triggers fallback → verify diagnostic bit set.

**Status:** ⏳ Pending

---

### 7. Dokumentasi eksplisit rounding `MaxDelay` di jitter
**File:** `jitter/jitter.go:107-108`

**Masalah:** `MaxDelay=25ms` → `maxSlots=2` (bukan 3). Silent truncation.

**Rencana:**
- Update doc comment jelas: "rounded down to whole 10 ms frames".
- Atau ubah ke `math.Ceil` (breaking untuk pengguna yang bergantung pada perilaku sekarang; pilih doc-only).

**Status:** ⏳ Pending

---

### 8. Dokumentasi `bufio.Writer` untuk `stream.Writer`
**File:** `stream.go` doc comments

**Masalah:** Tiap 10 byte langsung `w.w.Write(...)`. Kurang efisien untuk network.

**Rencana:**
- Update doc comment `NewWriter`: sarankan `bufio.NewWriterSize(w, 1024)` untuk network sinks.

**Status:** ⏳ Pending

---

## Prioritas Rendah (Polish)

### 9. Rename parameter `SearchAlgebraicFull(..., subframe int, ...)`
**File:** `internal/codebook/search_full.go:27`

Encoder passes `iSubfr` (0 or 40 samples). Nama "subframe" misleading — should be `subfrOffset` atau boolean.

**Status:** ⏳ Pending

---

### 10. `PopInto` doc: menyebutkan `Pop` allocates
**File:** `jitter/jitter.go:284`

**Status:** ⏳ Pending

---

## Fitur Baru (Roadmap)

### F1. RTP header build/parse penuh (RFC 3550)
Saat ini package `rtp/` hanya handle payload. Menambah RTP header build/parse
(V/P/X/CC/M/PT, sequence number, timestamp, SSRC, CSRC) membuat library standalone
tanpa perlu dependency eksternal.

**Nilai:** Adopsi mudah untuk SIP softswitch, gateway audio.

### F2. Adaptive playout delay estimator (Van Jacobson)
Nama package "adaptive jitter buffer" tapi target delay fixed. Implementasi
RFC 3550 Appendix A.8 (jitter estimator dengan low-pass filter) akan match nama.

**Nilai:** Deteksi otomatis network condition, auto-tune buffering.

### F3. RTCP Receiver Report integration hook
Ekspose `DecoderStats` sebagai RTCP RR field (fraction lost, cumulative lost,
interarrival jitter, LSR/DLSR).

**Nilai:** Bisa langsung feed ke RTCP feedback loop.

### F4. PLC quality selector
Saat ini satu PLC algoritma (extrapolated LSP + random pulse). Alternatif:
waveform-similarity overlap-add (WSOLA), atau ITU-T G.711.1 Appendix I-style
Speech Property-Based FEC.

**Nilai:** Higher quality untuk streaming dengan burst loss.

### F5. G.729D/E (6.4 / 11.8 kbps rate)
G.729D: 6.4 kbps reduced-complexity mode. G.729E: 11.8 kbps extended mode dengan
FCB 4/8 pulses selectable.

**Nilai:** Multi-rate flexibility untuk bandwidth-constrained atau quality-sensitive channels.
**Effort:** Besar (butuh implementasi codebook berbeda, bitstream layout berbeda).

### F6. SIMD assembly untuk hot loops (amd64/arm64)
`SearchAlgebraicA` inner correlation, `CorH`, `Autocorr` adalah bottleneck.
Assembly dot-product bisa 2-3× speedup.

**Nilai:** Push realtime factor dari 238× ke 500×+, lebih banyak concurrent stream per core.
**Effort:** Menengah (butuh dua arsitektur).

### F7. Batch decoder DecodeBatchInto (zero-alloc variant)
Sekarang ada `EncodeBatchInto`. Simetrikan dengan `DecodeBatchInto([][]byte, ...)`.

### F8. G.729 header stripping tools (SIP/RFC compliance)
CLI helper untuk konversi antara raw bitstream ↔ RTP dump ↔ Annex B framed file.

---

## Selesai / Skip

- **`ParityErrors` counter** → Done (item #4).
- **Info disclosure debug.Stack()** → Done (item #1).
- **Reader pendingErr** → Done (item #2).
- **SDP length cap** → Done (item #3).
- **Jitter incremental count** → Done (item #5).
