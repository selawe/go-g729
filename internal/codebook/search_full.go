package codebook

import "github.com/selawe/go-g729/internal/params"

const (
	MaxTime   = 75
	ThreshFCB = float32(0.40)
)

// SearchAlgebraicFull performs the exhaustive nested algebraic codebook search for Full G.729 (d4i40_17).
// It searches 4 pulses across 4 tracks using 4 nested loops with correlation threshold pruning
// and a time budget carried between subframes.
//
// Arguments:
//   - x: target vector (40 samples)
//   - h: impulse response of weighted synthesis filter (40 samples)
//   - t0: integer pitch lag from closed-loop search
//   - pitchSharp: pitch sharpening factor (clipped pitch gain)
//   - subfrOffset: sample offset of the current subframe within the frame
//     (0 for 1st subframe, params.L_SUBFR=40 for 2nd subframe)
//   - extra: pointer to time budget state carried across subframes (optional, can be nil)
//
// Returns:
//   - index: 13-bit codebook position index
//   - sign: 4-bit pulse sign index
//   - code: selected algebraic excitation codevector (with pitch sharpening if t0 < 40)
//   - y: filtered algebraic codeword (code convolved with h)
func SearchAlgebraicFull(x, h []float32, t0 int, pitchSharp float32, subfrOffset int, extra *int) (index int, sign int, code [params.L_SUBFR]float32, y [params.L_SUBFR]float32) {
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
	var pSign [params.L_SUBFR]float32
	var dnAbs [params.L_SUBFR]float32
	for i := 0; i < params.L_SUBFR; i++ {
		if dn[i] >= 0 {
			pSign[i] = 1.0
			dnAbs[i] = dn[i]
		} else {
			pSign[i] = -1.0
			dnAbs[i] = -dn[i]
		}
	}

	// 5. Compute search threshold after 3 pulses.
	average := dnAbs[0] + dnAbs[1] + dnAbs[2]
	max0 := dnAbs[0]
	max1 := dnAbs[1]
	max2 := dnAbs[2]
	for i := 5; i < params.L_SUBFR; i += Step {
		average += dnAbs[i] + dnAbs[i+1] + dnAbs[i+2]
		if dnAbs[i] > max0 {
			max0 = dnAbs[i]
		}
		if dnAbs[i+1] > max1 {
			max1 = dnAbs[i+1]
		}
		if dnAbs[i+2] > max2 {
			max2 = dnAbs[i+2]
		}
	}
	max0 += max1 + max2
	average *= 0.125
	thres := average + (max0-average)*ThreshFCB

	// 6. Pre-multiply rr cross-correlations by pulse signs.
	for i := 0; i < NbPos; i++ {
		s0 := pSign[i*Step]
		for j := 0; j < NbPos; j++ {
			rr[OffRri0i1+i*NbPos+j] *= s0 * pSign[j*Step+1]
			rr[OffRri0i2+i*NbPos+j] *= s0 * pSign[j*Step+2]
			rr[OffRri0i3+i*NbPos+j] *= s0 * pSign[j*Step+3]
			rr[OffRri0i4+i*NbPos+j] *= s0 * pSign[j*Step+4]
		}
	}
	for i := 0; i < NbPos; i++ {
		s1 := pSign[i*Step+1]
		for j := 0; j < NbPos; j++ {
			rr[OffRri1i2+i*NbPos+j] *= s1 * pSign[j*Step+2]
			rr[OffRri1i3+i*NbPos+j] *= s1 * pSign[j*Step+3]
			rr[OffRri1i4+i*NbPos+j] *= s1 * pSign[j*Step+4]
		}
	}
	for i := 0; i < NbPos; i++ {
		s2 := pSign[i*Step+2]
		for j := 0; j < NbPos; j++ {
			rr[OffRri2i3+i*NbPos+j] *= s2 * pSign[j*Step+3]
			rr[OffRri2i4+i*NbPos+j] *= s2 * pSign[j*Step+4]
		}
	}

	// 7. Time budget for search.
	timeBudget := MaxTime
	if extra != nil {
		if subfrOffset == 0 {
			*extra = 30
		}
		timeBudget += *extra
	} else if subfrOffset == 0 {
		timeBudget += 30
	}

	// Default pulse positions
	ip0 := 0
	ip1 := 1
	ip2 := 2
	ip3 := 3
	psc := float32(0.0)
	alpha := float32(1000000.0)

	// 4 nested loops to search codebook
	searchFinished := false
	for i0 := 0; i0 < params.L_SUBFR; i0 += Step {
		if searchFinished {
			break
		}
		i0Idx := i0 / 5
		ps0 := dnAbs[i0]
		alp0 := rr[OffRri0i0+i0Idx]

		for i1 := 1; i1 < params.L_SUBFR; i1 += Step {
			if searchFinished {
				break
			}
			i1Idx := i1 / 5
			ps1 := ps0 + dnAbs[i1]
			alp1 := alp0 + rr[OffRri1i1+i1Idx] + 2.0*rr[OffRri0i1+i0Idx*NbPos+i1Idx]

			for i2 := 2; i2 < params.L_SUBFR; i2 += Step {
				if searchFinished {
					break
				}
				i2Idx := i2 / 5
				ps2 := ps1 + dnAbs[i2]
				alp2 := alp1 + rr[OffRri2i2+i2Idx] + 2.0*(rr[OffRri0i2+i0Idx*NbPos+i2Idx]+rr[OffRri1i2+i1Idx*NbPos+i2Idx])

				if ps2 > thres {
					// 4th loop part A: Track 3 (positions 3, 8, 13, 18, 23, 28, 33, 38)
					for i3 := 3; i3 < params.L_SUBFR; i3 += Step {
						i3Idx := i3 / 5
						ps3 := ps2 + dnAbs[i3]
						alp3 := alp2 + rr[OffRri3i3+i3Idx] + 2.0*(rr[OffRri0i3+i0Idx*NbPos+i3Idx]+rr[OffRri1i3+i1Idx*NbPos+i3Idx]+rr[OffRri2i3+i2Idx*NbPos+i3Idx])

						ps3c := ps3 * ps3
						if ps3c*alpha > psc*alp3 {
							psc = ps3c
							alpha = alp3
							ip0 = i0
							ip1 = i1
							ip2 = i2
							ip3 = i3
						}
					}

					// 4th loop part B: Track 4 (positions 4, 9, 14, 19, 24, 29, 34, 39)
					for i3 := 4; i3 < params.L_SUBFR; i3 += Step {
						i3Idx := i3 / 5
						ps3 := ps2 + dnAbs[i3]
						alp3 := alp2 + rr[OffRri4i4+i3Idx] + 2.0*(rr[OffRri0i4+i0Idx*NbPos+i3Idx]+rr[OffRri1i4+i1Idx*NbPos+i3Idx]+rr[OffRri2i4+i2Idx*NbPos+i3Idx])

						ps3c := ps3 * ps3
						if ps3c*alpha > psc*alp3 {
							psc = ps3c
							alpha = alp3
							ip0 = i0
							ip1 = i1
							ip2 = i2
							ip3 = i3
						}
					}

					timeBudget--
					if timeBudget <= 0 {
						searchFinished = true
						break
					}
				}
			}
		}
	}

	if extra != nil {
		*extra = timeBudget
	}

	// 8. Reconstruct codevector
	code[ip0] = pSign[ip0]
	code[ip1] = pSign[ip1]
	code[ip2] = pSign[ip2]
	code[ip3] = pSign[ip3]

	// 9. Filter codevector with hBuf to compute filtered codeword y
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

	// 10. Pack pulse position and sign indices
	index, sign = PackPulseIndex(ip0, ip1, ip2, ip3, int(pSign[ip0]), int(pSign[ip1]), int(pSign[ip2]), int(pSign[ip3]))

	// 11. Pitch sharpening on final codevector
	if t0 < params.L_SUBFR {
		for i := t0; i < params.L_SUBFR; i++ {
			code[i] += pitchSharp * code[i-t0]
		}
	}

	return index, sign, code, y
}
