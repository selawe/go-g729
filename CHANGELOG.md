# Changelog

All notable changes to this project will be documented in this file.

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
