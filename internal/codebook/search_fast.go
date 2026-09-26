package codebook

import "github.com/selawe/go-g729/internal/params"

// SearchAlgebraicA performs the fast algebraic codebook search for G.729A (d4i40_17_fast).
// It searches 4 pulses across 4 tracks using a 2-phase depth-first pair search.
//
// Arguments:
//   - x: target vector (40 samples)
//   - h: impulse response of weighted synthesis filter (40 samples)
//   - t0: integer pitch lag from closed-loop search
//   - pitchSharp: pitch sharpening factor (clipped pitch gain)
//
// Returns:
//   - index: 13-bit codebook position index
//   - sign: 4-bit pulse sign index
//   - code: selected algebraic excitation codevector (with pitch sharpening if t0 < 40)
//   - y: filtered algebraic codeword (code convolved with h)
func SearchAlgebraicA(x, h []float32, t0 int, pitchSharp float32) (index int, sign int, code [params.L_SUBFR]float32, y [params.L_SUBFR]float32) {
	// 1. Copy impulse response and include pitch contribution if t0 < 40.
	var hBuf [params.L_SUBFR]float32
	copy(hBuf[:], h[:params.L_SUBFR])
	if t0 < params.L_SUBFR {
		for i := t0; i < params.L_SUBFR; i++ {
			hBuf[i] += pitchSharp * hBuf[i-t0]
		}
	}

	// 2. Precompute correlations of hBuf (size 616).
	var rr [DimRR]float32
	CorH(hBuf[:], &rr)

	// 3. Compute backward filtered target vector dn.
	var dn [params.L_SUBFR]float32
	ComputeDn(dn[:], hBuf[:], x[:])

	// 4. Select signs of impulses and take absolute values of dn.
	var signDn [params.L_SUBFR]float32
	var dnAbs [params.L_SUBFR]float32
	for i := 0; i < params.L_SUBFR; i++ {
		if dn[i] >= 0 {
			signDn[i] = 1.0
			dnAbs[i] = dn[i]
		} else {
			signDn[i] = -1.0
			dnAbs[i] = -dn[i]
		}
	}

	// 5. Pre-multiply rr cross-correlations by pulse signs.
	for i := 0; i < NbPos; i++ {
		s0 := signDn[i*Step]
		for j := 0; j < NbPos; j++ {
			rr[OffRri0i1+i*NbPos+j] *= s0 * signDn[j*Step+1]
			rr[OffRri0i2+i*NbPos+j] *= s0 * signDn[j*Step+2]
			rr[OffRri0i3+i*NbPos+j] *= s0 * signDn[j*Step+3]
			rr[OffRri0i4+i*NbPos+j] *= s0 * signDn[j*Step+4]
		}
	}
	for i := 0; i < NbPos; i++ {
		s1 := signDn[i*Step+1]
		for j := 0; j < NbPos; j++ {
			rr[OffRri1i2+i*NbPos+j] *= s1 * signDn[j*Step+2]
			rr[OffRri1i3+i*NbPos+j] *= s1 * signDn[j*Step+3]
			rr[OffRri1i4+i*NbPos+j] *= s1 * signDn[j*Step+4]
		}
	}
	for i := 0; i < NbPos; i++ {
		s2 := signDn[i*Step+2]
		for j := 0; j < NbPos; j++ {
			rr[OffRri2i3+i*NbPos+j] *= s2 * signDn[j*Step+3]
			rr[OffRri2i4+i*NbPos+j] *= s2 * signDn[j*Step+4]
		}
	}

	// 6. Search optimum pulse positions.
	psk := float32(-1.0)
	alpk := float32(1.0)
	var ip0, ip1, ip2, ip3 int

	// Search 2 times: track 3, then track 4.
	for track := 3; track < 5; track++ {
		var off03, off13, off23, off33 int
		if track == 3 {
			off03 = OffRri0i3
			off13 = OffRri1i3
			off23 = OffRri2i3
			off33 = OffRri3i3
		} else {
			off03 = OffRri0i4
			off13 = OffRri1i4
			off23 = OffRri2i4
			off33 = OffRri4i4
		}

		// -------------------------------------------------------------
		// Depth-first search 3, Phase A: Track 2 and Track 3/4
		// -------------------------------------------------------------
		sq := float32(-1.0)
		alp := float32(1.0)
		var ps float32
		var ix, iy int

		// Search top 2 positions in Track 2 (pos = 2, 7, 12, ...).
		prevI0 := -1
		for i := 0; i < 2; i++ {
			maxVal := float32(-1.0)
			i0Cand := -1
			for j := 2; j < params.L_SUBFR; j += Step {
				if dnAbs[j] > maxVal && prevI0 != j {
					maxVal = dnAbs[j]
					i0Cand = j
				}
			}
			prevI0 = i0Cand

			jIdx := i0Cand / 5
			ps1 := dnAbs[i0Cand]
			alp1 := float32(0.5) * rr[OffRri2i2+jIdx]

			p0Base := off23 + (jIdx << 3)
			p1Base := off33

			for i1 := track; i1 < params.L_SUBFR; i1 += Step {
				i1Idx := i1 / 5
				ps2 := ps1 + dnAbs[i1]
				alp2 := alp1 + rr[p0Base+i1Idx] + rr[p1Base+i1Idx]*0.5
				sq2 := ps2 * ps2
				s := alp*sq2 - sq*alp2
				if s > 0 {
					sq = sq2
					ps = ps2
					alp = alp2
					ix = i0Cand
					iy = i1
				}
			}
		}

		i0 := ix
		i1 := iy
		i1Offset := (i1 / 5) << 3

		// -------------------------------------------------------------
		// Depth-first search 3, Phase B: Track 0 and Track 1
		// -------------------------------------------------------------
		ps0 := ps
		alp0 := alp
		sq = -1.0
		alp = 1.0

		// Precompute vector for Track 1
		var tmpVect [NbPos]float32
		for i3Idx := 0; i3Idx < NbPos; i3Idx++ {
			tmpVect[i3Idx] = rr[OffRri1i2+i3Idx*NbPos+(i0/5)] +
				rr[off13+i3Idx*NbPos+(i1/5)] +
				rr[OffRri1i1+i3Idx]*0.5
		}

		p3Idx := 0
		for i2 := 0; i2 < params.L_SUBFR; i2 += Step {
			i2Idx := i2 / 5
			ps1 := ps0 + dnAbs[i2]
			alp1 := alp0 + rr[OffRri0i2+i2Idx*NbPos+(i0/5)] +
				rr[off03+i2Idx*NbPos+(i1/5)] +
				rr[OffRri0i0+i2Idx]*0.5

			for i3 := 1; i3 < params.L_SUBFR; i3 += Step {
				i3Idx := i3 / 5
				ps2 := ps1 + dnAbs[i3]
				alp2 := alp1 + rr[OffRri0i1+p3Idx] + tmpVect[i3Idx]
				p3Idx++

				sq2 := ps2 * ps2
				s := alp*sq2 - sq*alp2
				if s > 0 {
					sq = sq2
					alp = alp2
					ix = i2
					iy = i3
				}
			}
		}

		// Compare with global best codevector
		s := alpk*sq - psk*alp
		if s > 0 {
			psk = sq
			alpk = alp
			ip2 = i0
			ip3 = i1
			ip0 = ix
			ip1 = iy
		}

		// -------------------------------------------------------------
		// Depth-first search 4, Phase A: Track 3/4 and Track 0
		// -------------------------------------------------------------
		sq = -1.0
		alp = 1.0

		prevI0 = -1
		for i := 0; i < 2; i++ {
			maxVal := float32(-1.0)
			i0Cand := -1
			for j := track; j < params.L_SUBFR; j += Step {
				if dnAbs[j] > maxVal && prevI0 != j {
					maxVal = dnAbs[j]
					i0Cand = j
				}
			}
			prevI0 = i0Cand

			jIdx := i0Cand / 5
			ps1 := dnAbs[i0Cand]
			alp1 := float32(0.5) * rr[off33+jIdx]

			for i1 := 0; i1 < params.L_SUBFR; i1 += Step {
				i1Idx := i1 / 5
				ps2 := ps1 + dnAbs[i1]
				alp2 := alp1 + rr[off03+i1Idx*NbPos+jIdx] + rr[OffRri0i0+i1Idx]*0.5
				sq2 := ps2 * ps2
				s := alp*sq2 - sq*alp2
				if s > 0 {
					sq = sq2
					ps = ps2
					alp = alp2
					ix = i0Cand
					iy = i1
				}
			}
		}

		i0 = ix
		i1 = iy
		i1Offset = (i1 / 5) << 3

		// -------------------------------------------------------------
		// Depth-first search 4, Phase B: Track 1 and Track 2
		// -------------------------------------------------------------
		ps0 = ps
		alp0 = alp
		sq = -1.0
		alp = 1.0

		for i3Idx := 0; i3Idx < NbPos; i3Idx++ {
			tmpVect[i3Idx] = rr[off23+i3Idx*NbPos+(i0/5)] +
				rr[OffRri0i2+i1Offset+i3Idx] +
				rr[OffRri2i2+i3Idx]*0.5
		}

		for i2 := 1; i2 < params.L_SUBFR; i2 += Step {
			i2Idx := i2 / 5
			ps1 := ps0 + dnAbs[i2]
			alp1 := alp0 + rr[off13+i2Idx*NbPos+(i0/5)] +
				rr[OffRri0i1+i1Offset+i2Idx] +
				rr[OffRri1i1+i2Idx]*0.5

			for i3 := 2; i3 < params.L_SUBFR; i3 += Step {
				i3Idx := i3 / 5
				ps2 := ps1 + dnAbs[i3]
				alp2 := alp1 + rr[OffRri1i2+i2Idx*NbPos+i3Idx] + tmpVect[i3Idx]
				sq2 := ps2 * ps2
				s := alp*sq2 - sq*alp2
				if s > 0 {
					sq = sq2
					alp = alp2
					ix = i2
					iy = i3
				}
			}
		}

		s = alpk*sq - psk*alp
		if s > 0 {
			psk = sq
			alpk = alp
			ip3 = i0
			ip0 = i1
			ip1 = ix
			ip2 = iy
		}
	}

	// 7. Reconstruct codevector
	code[ip0] = signDn[ip0]
	code[ip1] = signDn[ip1]
	code[ip2] = signDn[ip2]
	code[ip3] = signDn[ip3]

	// 8. Filter codevector with hBuf to compute filtered codeword y
	for i, j := ip0, 0; i < params.L_SUBFR; i, j = i+1, j+1 {
		y[i] += code[ip0] * hBuf[j]
	}
	for i, j := ip1, 0; i < params.L_SUBFR; i, j = i+1, j+1 {
		y[i] += code[ip1] * hBuf[j]
	}
	for i, j := ip2, 0; i < params.L_SUBFR; i, j = i+1, j+1 {
		y[i] += code[ip2] * hBuf[j]
	}
	for i, j := ip3, 0; i < params.L_SUBFR; i, j = i+1, j+1 {
		y[i] += code[ip3] * hBuf[j]
	}

	// 9. Pack pulse position and sign indices
	index, sign = PackPulseIndex(ip0, ip1, ip2, ip3, int(signDn[ip0]), int(signDn[ip1]), int(signDn[ip2]), int(signDn[ip3]))

	// 10. Pitch sharpening on final codevector
	if t0 < params.L_SUBFR {
		for i := t0; i < params.L_SUBFR; i++ {
			code[i] += pitchSharp * code[i-t0]
		}
	}

	return index, sign, code, y
}
