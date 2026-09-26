// Package bits handles bitstream packing and unpacking for ITU-T G.729, G.729A, and Annex B.
package bits

// ParamSet holds all 80-bit speech frame parameters for one 10 ms G.729 frame.
type ParamSet struct {
	// Line Spectral Pairs (18 bits total)
	L0 uint8 // Switched MA predictor index (1 bit: 0..1)
	L1 uint8 // Stage 1 LSP vector index (7 bits: 0..127)
	L2 uint8 // Stage 2 low LSP vector index (5 bits: 0..31)
	L3 uint8 // Stage 2 high LSP vector index (5 bits: 0..31)

	// Subframe 1 (33 bits total)
	P1  uint8  // Pitch delay (8 bits: 0..255)
	P0  uint8  // Pitch delay parity (1 bit: 0..1, odd parity over 6 MSBs of P1)
	C1  uint16 // Fixed algebraic codebook pulse positions (13 bits: 0..8191)
	S1  uint8  // Fixed algebraic codebook pulse signs (4 bits: 0..15)
	GA1 uint8  // Stage 1 gain codebook index (3 bits: 0..7)
	GB1 uint8  // Stage 2 gain codebook index (4 bits: 0..15)

	// Subframe 2 (29 bits total)
	P2  uint8  // Differential pitch delay (5 bits: 0..31)
	C2  uint16 // Fixed algebraic codebook pulse positions (13 bits: 0..8191)
	S2  uint8  // Fixed algebraic codebook pulse signs (4 bits: 0..15)
	GA2 uint8  // Stage 1 gain codebook index (3 bits: 0..7)
	GB2 uint8  // Stage 2 gain codebook index (4 bits: 0..15)
}

// SIDParamSet holds the parameters for an Annex B Silence Insertion Descriptor (15 bits + 1 bit pad = 16 bits = 2 bytes).
type SIDParamSet struct {
	Predictor uint8 // Switched MA predictor index for LSF quantizer (1 bit: 0..1)
	Stage1    uint8 // First stage vector index of LSF quantizer (5 bits: 0..31)
	Stage2    uint8 // Second stage vector index of LSF quantizer (4 bits: 0..15)
	Energy    uint8 // Quantized logarithmic frame energy (5 bits: 0..31)
}

// Pack encodes the parameters in p into 10 bytes (80 bits) in dst using MSB-first network byte order.
// dst must have len >= 10.
func Pack(dst []byte, p *ParamSet) {
	_ = dst[9] // bounds check elimination

	// Byte 0: L0(1b) | L1(7b)
	dst[0] = ((p.L0 & 0x01) << 7) | (p.L1 & 0x7F)

	// Byte 1: L2(5b) | L3_msb(3b)
	dst[1] = ((p.L2 & 0x1F) << 3) | ((p.L3 >> 2) & 0x07)

	// Byte 2: L3_lsb(2b) | P1_msb(6b)
	dst[2] = ((p.L3 & 0x03) << 6) | ((p.P1 >> 2) & 0x3F)

	// Byte 3: P1_lsb(2b) | P0(1b) | C1_msb(5b)
	dst[3] = ((p.P1 & 0x03) << 6) | ((p.P0 & 0x01) << 5) | uint8((p.C1>>8)&0x1F)

	// Byte 4: C1_lsb(8b)
	dst[4] = uint8(p.C1 & 0xFF)

	// Byte 5: S1(4b) | GA1(3b) | GB1_msb(1b)
	dst[5] = ((p.S1 & 0x0F) << 4) | ((p.GA1 & 0x07) << 1) | ((p.GB1 >> 3) & 0x01)

	// Byte 6: GB1_lsb(3b) | P2(5b)
	dst[6] = ((p.GB1 & 0x07) << 5) | (p.P2 & 0x1F)

	// Byte 7: C2_msb(8b)
	dst[7] = uint8((p.C2 >> 5) & 0xFF)

	// Byte 8: C2_lsb(5b) | S2_msb(3b)
	dst[8] = (uint8(p.C2&0x1F) << 3) | ((p.S2 >> 1) & 0x07)

	// Byte 9: S2_lsb(1b) | GA2(3b) | GB2(4b)
	dst[9] = ((p.S2 & 0x01) << 7) | ((p.GA2 & 0x07) << 4) | (p.GB2 & 0x0F)
}

// Unpack decodes 10 bytes (80 bits) in src into p.
// src must have len >= 10.
func Unpack(p *ParamSet, src []byte) {
	_ = src[9] // bounds check elimination

	// Byte 0
	p.L0 = (src[0] >> 7) & 0x01
	p.L1 = src[0] & 0x7F

	// Byte 1 & 2
	p.L2 = (src[1] >> 3) & 0x1F
	l3High := src[1] & 0x07
	p.L3 = (l3High << 2) | ((src[2] >> 6) & 0x03)

	// Byte 2 & 3
	p1High := src[2] & 0x3F
	p.P1 = (p1High << 2) | ((src[3] >> 6) & 0x03)
	p.P0 = (src[3] >> 5) & 0x01

	// Byte 3 & 4
	c1High := uint16(src[3] & 0x1F)
	p.C1 = (c1High << 8) | uint16(src[4])

	// Byte 5 & 6
	p.S1 = (src[5] >> 4) & 0x0F
	p.GA1 = (src[5] >> 1) & 0x07
	gb1High := src[5] & 0x01
	p.GB1 = (gb1High << 3) | ((src[6] >> 5) & 0x07)

	// Byte 6
	p.P2 = src[6] & 0x1F

	// Byte 7 & 8
	c2High := uint16(src[7])
	p.C2 = (c2High << 5) | uint16((src[8]>>3)&0x1F)

	// Byte 8 & 9
	s2High := src[8] & 0x07
	p.S2 = (s2High << 1) | ((src[9] >> 7) & 0x01)
	p.GA2 = (src[9] >> 4) & 0x07
	p.GB2 = src[9] & 0x0F
}

// PackSID encodes the Annex B SID parameters in s into 2 bytes (16 bits) in dst.
// dst must have len >= 2.
func PackSID(dst []byte, s *SIDParamSet) {
	_ = dst[1] // bounds check elimination

	// Byte 0: Predictor(1b) | Stage1(5b) | Stage2_msb(2b)
	dst[0] = ((s.Predictor & 0x01) << 7) | ((s.Stage1 & 0x1F) << 2) | ((s.Stage2 >> 2) & 0x03)

	// Byte 1: Stage2_lsb(2b) | Energy(5b) | Pad(1b=0)
	dst[1] = ((s.Stage2 & 0x03) << 6) | ((s.Energy & 0x1F) << 1)
}

// UnpackSID decodes 2 bytes (16 bits) in src into s.
// src must have len >= 2.
func UnpackSID(s *SIDParamSet, src []byte) {
	_ = src[1] // bounds check elimination

	s.Predictor = (src[0] >> 7) & 0x01
	s.Stage1 = (src[0] >> 2) & 0x1F
	st2High := src[0] & 0x03
	s.Stage2 = (st2High << 2) | ((src[1] >> 6) & 0x03)
	s.Energy = (src[1] >> 1) & 0x1F
}
