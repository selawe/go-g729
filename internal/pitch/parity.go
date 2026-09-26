package pitch

import "math/bits"

// ParityBit computes the odd-parity bit P0 from the 6 most significant bits
// of the 8-bit pitch delay index P1 (bits 2 through 7).
//
// In ITU-T G.729:
//
//	temp = pitch_index >> 1;
//	sum = 1;
//	for (i = 0; i <= 5; i++) {
//	    temp >>= 1;
//	    bit = temp & 1;
//	    sum += bit;
//	}
//	return sum & 1;
func ParityBit(pitchIndex uint8) uint8 {
	// Extract bits 2..7 (6 bits)
	msb6 := pitchIndex >> 2
	ones := bits.OnesCount8(msb6)
	// Odd parity: (1 + ones) & 1
	return uint8((1 + ones) & 1)
}

// CheckParity verifies the received parity bit against the pitch index.
// Returns true if parity is valid (no bit error detected), false otherwise.
func CheckParity(pitchIndex uint8, parity uint8) bool {
	return ParityBit(pitchIndex) == (parity & 1)
}
