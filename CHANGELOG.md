# Changelog

All notable changes to this project will be documented in this file.

## [v0.2.1] — 2026-10-03

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
