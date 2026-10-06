# Changelog

All notable changes to this project will be documented in this file.

## [v0.3.0] — 2026-10-06

### Breaking Changes

- **`jitter.Buffer.Push` signature updated to accept RTP Marker bit (`marker bool`)**:
  Per RFC 3550 §5.1 dan RFC 3551 §4.5.6, parameter `marker bool` ditambahkan ke `b.Push(seq, ts, marker, payload)` untuk menandai paket awal dari talkspurt baru setelah periode hening (DTX silence). Pemanggil method ini perlu menambahkan argumen boolean `marker` (set `true` bila bit M pada RTP header aktif, atau `false` untuk paket berikutnya).

### Added

- **RTP Marker Bit Talkspurt Resynchronization (`jitter.Buffer`)**:
  Saat `marker=true`, jitter buffer secara otomatis me-resync `playoutSeq`, membersihkan slot lama dari talkspurt sebelumnya, me-reset `bufferedCount`, dan memulai fase pre-buffering baru. Mencegah packet rejection palsu (*false LatePackets*) dan semburan frame PLC palsu saat transisi dari hening ke bicara.
- **Canonical ITU-T Test Vectors committed (`testdata/itu/`)**:
  Vektor kanonikal ITU-T (`TEST.IN`, `TEST.BIT`, `TEST.pst` — total 85 KB) kini diikutsertakan dalam repositori. Conformance test di CI workflow (`TestDecoderOfficialTestVector` & `TestEncoderConformance`) kini berstatus **PASS** secara otomatis di semua runner tanpa perlu download manual.
- **ARM64 Golden Regression Files (`testdata/golden/arm64/`)**:
  Golden files khusus arsitektur ARM64 (`g729a_encoder.bit`, `g729_full_encoder.bit`, `g729a_decoder.pcm`) telah di-commit ke repositori. CI workflow kini memberlakukan proteksi regresi ketat (*strict bit-exact check*) pada Apple Silicon / ARM64 tanpa re-generasi otomatis.
- **WAN Network Load & Stress Tests (`jitter/load_test.go`)**:
  Suite pengujian beban jaringan realistis mencakup simulasi WAN dengan random loss (5%), burst loss (5%), network jitter & packet reordering (0–35 ms), packet duplication (2%), degradasi ekstrem (loss 30% dengan progressive muting), serta stress-test konkurensi 50 stream VoIP simultan.

### Fixed

- **RTCP Interarrival Jitter 32-bit Timestamp Wrap-Around (`rtp.RTCPTracker`)**:
  Perhitungan transit time sebelumnya menggunakan aritmatika `int64` yang menyebabkan lonjakan jitter fiktif sebesar $\approx 4{,}3 \times 10^9$ sampel setiap kali timestamp RTP 32-bit membungkus (setiap ~6.2 hari pada clock 8000 Hz). Diperbaiki dengan modular arithmetic 32-bit `uint32` $\to$ `int32(arrivalTS32 - ts)` sesuai spesifikasi resmi RFC 3550 Appendix A.8.
- **Jitter Buffer: Deadlock & False Packet Rejections Pasca Periode Hening DTX**:
  Jeda hening lebih dari `maxSlots` (misal >200 ms) sebelumnya menyebabkan paket baru masa depan salah diidentifikasi sebagai paket terlambat (*LatePackets*) dan dibuang permanen. Kini ditangani dengan benar via sinkronisasi Marker Bit dan reset slot.

---

## [v0.2.1] — 2026-10-03

### Added

- **CI/CD Pipeline (`.github/workflows/ci.yml`)**: Multi-OS test matrix (Ubuntu, macOS, Windows), race detector check (`go test -race`), static analysis (`go vet`), security vulnerability scan (`govulncheck`), and automated smoke fuzzing.
- **`g729.EncoderPool` & `g729.DecoderPool`**: Concurrent-safe zero-allocation object pools based on `sync.Pool` with automatic state `.Reset()`, designed for high-turnover VoIP sessions (245 ns Get/Put, 0 B/op, 0 allocs).
- **RFC 3550 RTCP Telemetry & E-Model MOS Estimator (`rtp.RTCPTracker`)**: Real-time statistical tracking of interarrival jitter $J$, cumulative packet loss, interval fraction lost, and estimated conversational speech quality (MOS-CQO / R-factor per ITU-T G.107).
- **RFC 4733 DTMF Telephony Guidance & SDP Helpers (`sdp.TelephoneEvent*`)**: Complete architectural guide (`docs/telephony_dtmf.md`) explaining why CELP codecs cannot encode in-band DTMF and detailing SDP negotiation + RTP demultiplexing patterns for out-of-band `telephone-event`.
- **Pion WebRTC / RTP Integration Guide (`docs/pion_integration.md`)**: End-to-end integration guide and tested examples for building scalable WebRTC media gateways using Pion and `go-g729`.

### Fixed

- **Decoder: postfilter residue filter kehilangan history tiap subframe.**
  Filter A(z/γ₂_pst) (tahap pertama adaptive postfilter) dipanggil dengan
  `nil` memory setiap subframe, sehingga M=10 sampel history di-reset. Referensi
  ITU-T Annex C mempertahankan history ini via pointer arithmetic `syn[-M..-1]`;
  implementasi Go harus membawanya secara eksplisit di `PostFilterState.memRes`.
  Dampak: SNR decoder vs TEST.pst meningkat dari **20.67 dB → 32.81 dB (+12.14 dB)**.
  Gate `TestDecoderOfficialTestVector` dinaikkan dari 15 dB ke 28 dB.
- **Decoder: urutan ekstrapolasi pitch lag salah saat PLC dan parity error.**
  Sesuai ITU-T G.729 §4.4.1 / DEC_LD8A.C, nilai T0 harus dibaca *sebelum*
  `old_T0` di-increment (`T0 = old_T0; old_T0++`). Kode sebelumnya
  mengincrement terlebih dahulu, sehingga subframe 0 saat PLC/parity error
  menggunakan lag yang sudah digeser satu — tidak sesuai referensi.
- **RTP: paket transisi speech→SID (RFC 3551 §4.5.6) tidak di-unpack dengan
  benar.** Paket campuran speech+SID (misal 1 frame 10 byte + 1 frame 2 byte)
  gagal diurai karena `UnpackInto` menolak frame SID setelah frame speech.
  Sekarang transisi speech→SID diizinkan sesuai spesifikasi.
- **Jitter buffer: multi-frame packet collision.**
  Slot di-index hanya dengan `seq & slotMask`, tidak menyimpan semua N frame
  per paket. Paket dengan seq yang memetakan ke slot yang sama menimpa frame
  yang belum diputar. Sekarang setiap slot menyimpan hingga `MaxFramesPerPacket`
  frame lengkap; `playoutFrameIdx` melacak frame mana yang akan dibaca berikutnya.

### Performance

- **`dsp.AutocorrRaw`**: BCE hint + 8-way manual loop unrolling pada inner
  dot-product loop. AMD Ryzen 9 5900HX: ~8% lebih cepat pada level encoder frame.
- **`dsp.SynthesisFilter` dan `dsp.Residue`**: 10 koefisien LP di-unroll menjadi
  ekspresi scalar tunggal dengan BCE hint `_ = aCoeffs[M-1]` dan `_ = p[9]`.
- **`dsp.Convolution`**: eliminasi inner branch `if hIdx < lh` dengan
  menghitung `iMin = max(0, n+1-lh)` sebelum inner loop, ditambah BCE hint
  `_ = x[iMax]`. Isolasi: ~23% lebih cepat (690 ns → 528 ns).
- **`decoder.go` output loop**: `math.IsNaN(float64(val))` diganti `val != val`
  — menghilangkan konversi float32→float64 × 80 sampel per frame. Import
  `math` dihapus dari decoder.
- **`postfilter.go`**: hapus zero-init `zeroMem` yang mubazir di dalam subframe
  loop (Go sudah zero-init stack array; `SynthesisFilter` dengan `update=false`
  tidak menulis balik).
- Decoder keseluruhan: **~7% lebih cepat** (~6.0 µs → 5.55 µs/frame,
  1650× → 1800× realtime, AMD Ryzen 9 5900HX).

---

## [v0.2.0] — 2026-10-01

### Breaking Changes

- **`*Encoder` dan `*Decoder` sekarang concrete struct types, bukan interface.**
  Sebelumnya `NewEncoder` / `NewDecoder` mengembalikan tipe `Encoder` / `Decoder`
  (interface). Sekarang keduanya mengembalikan `*Encoder` / `*Decoder` (pointer ke
  struct). Kode yang melakukan type assertion (`.(*encoder)`) atau menyimpan nilai
  ke variabel bertipe interface perlu diperbarui.

  ```go
  // Sebelumnya (v0.1.0)
  var enc Encoder = NewEncoder(cfg)

  // Sekarang (v0.2.0)
  enc := NewEncoder(cfg)  // *Encoder
  ```

### Added

- Nil receiver guard di semua method publik `*Encoder` dan `*Decoder`.
  Memanggil method pada pointer nil kini mengembalikan `ErrNilEncoder` /
  `ErrNilDecoder` tanpa panic.
- `ErrNilEncoder` dan `ErrNilDecoder` sentinel errors di package root.
- `TestNilEncoderReturnsError` dan `TestNilDecoderReturnsError` memvalidasi
  perilaku nil receiver.
- `TestEncoderConformance`: mengukur SNR pipeline kita (TEST.IN → encoder →
  decoder) terhadap TEST.pst resmi ITU-T. Gate 8 dB, hasil ~12 dB.
- Tiga test robustness baru: extreme PCM boundary (all-min/max/Nyquist),
  repeated pathological bitstream (50 frame berturut-turut), dan random
  bitstream dengan interleaved PLC (5000 frame).

### Fixed

- `computeSNR`: sebelumnya melewati delay dengan noise = 0, sehingga pada
  output bit-exact helper salah memilih delay lain dan melaporkan ~6 dB
  (artefak). Sekarang noise = 0 langsung mengembalikan `+Inf` (perfect match).
- `TestEndToEndOfficialSpeechVector`: SNR gate sebelumnya aktif bahkan tanpa
  vektor ITU-T, menyebabkan test gagal di fresh clone. Sekarang gate hanya
  aktif bila `testdata/itu/TEST.IN` tersedia.
- README: klaim SNR dan deskripsi tipe publik diperbarui agar akurat.

### Changed

- Minimum Go version diturunkan dari `1.27.0` ke `1.26`.

---

## [v0.1.0] — 2026-10-01

Initial public release. G.729A + G.729 Full + Annex B (VAD/DTX/CNG), RTP,
SDP, jitter buffer, zero-alloc encoder, 238×/1054× realtime.
