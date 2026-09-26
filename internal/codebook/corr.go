package codebook

import "github.com/selawe/go-g729/internal/params"

const (
	// DimRR is the size of the correlation matrix for the 4-track algebraic codebook:
	// 5 self-correlation tracks * 8 positions (40) + 9 cross-correlation pairs * 64 positions (576) = 616.
	DimRR = 616
	NbPos = 8
	Step  = 5
	MSize = 64

	// Offsets in the 616-element correlation array rr.
	OffRri0i0 = 0
	OffRri1i1 = 8
	OffRri2i2 = 16
	OffRri3i3 = 24
	OffRri4i4 = 32
	OffRri0i1 = 40
	OffRri0i2 = 104
	OffRri0i3 = 168
	OffRri0i4 = 232
	OffRri1i2 = 296
	OffRri1i3 = 360
	OffRri1i4 = 424
	OffRri2i3 = 488
	OffRri2i4 = 552
)

// ComputePhi precomputes the symmetric 40x40 impulse response correlation matrix:
//
//	Phi[i][j] = sum_{n=max(i,j)}^{39} h[n-i] * h[n-j]
func ComputePhi(phi *[params.L_SUBFR][params.L_SUBFR]float32, h []float32) {
	const l = params.L_SUBFR
	for i := 0; i < l; i++ {
		for j := i; j < l; j++ {
			var s float32
			for n := j; n < l; n++ {
				s += h[n-i] * h[n-j]
			}
			phi[i][j] = s
			phi[j][i] = s
		}
	}
}

// ComputeDn computes the backward filtered target vector dn:
//
//	dn[i] = sum_{j=i}^{39} x[j] * h[j-i]
func ComputeDn(dn, h, x []float32) {
	l := len(x)
	if l > params.L_SUBFR {
		l = params.L_SUBFR
	}
	for i := 0; i < l; i++ {
		var s float32
		for j := i; j < l; j++ {
			s += x[j] * h[j-i]
		}
		dn[i] = s
	}
}

// CorH computes the impulse response self-correlations and cross-correlations
// required by the algebraic codebook search in G.729 and G.729A.
// The result is populated into rr (size 616), identically to ITU-T cor_h().
func CorH(h []float32, rr *[DimRR]float32) {
	rri0i0 := rr[OffRri0i0 : OffRri0i0+NbPos]
	rri1i1 := rr[OffRri1i1 : OffRri1i1+NbPos]
	rri2i2 := rr[OffRri2i2 : OffRri2i2+NbPos]
	rri3i3 := rr[OffRri3i3 : OffRri3i3+NbPos]
	rri4i4 := rr[OffRri4i4 : OffRri4i4+NbPos]

	rri0i1 := rr[OffRri0i1 : OffRri0i1+MSize]
	rri0i2 := rr[OffRri0i2 : OffRri0i2+MSize]
	rri0i3 := rr[OffRri0i3 : OffRri0i3+MSize]
	rri0i4 := rr[OffRri0i4 : OffRri0i4+MSize]
	rri1i2 := rr[OffRri1i2 : OffRri1i2+MSize]
	rri1i3 := rr[OffRri1i3 : OffRri1i3+MSize]
	rri1i4 := rr[OffRri1i4 : OffRri1i4+MSize]
	rri2i3 := rr[OffRri2i3 : OffRri2i3+MSize]
	rri2i4 := rr[OffRri2i4 : OffRri2i4+MSize]

	p0 := NbPos - 1
	p1 := NbPos - 1
	p2 := NbPos - 1
	p3 := NbPos - 1
	p4 := NbPos - 1

	ptrH1 := 0
	var cor float32
	for i := 0; i < NbPos; i++ {
		cor += h[ptrH1] * h[ptrH1]
		ptrH1++
		rri4i4[p4] = cor
		p4--

		cor += h[ptrH1] * h[ptrH1]
		ptrH1++
		rri3i3[p3] = cor
		p3--

		cor += h[ptrH1] * h[ptrH1]
		ptrH1++
		rri2i2[p2] = cor
		p2--

		cor += h[ptrH1] * h[ptrH1]
		ptrH1++
		rri1i1[p1] = cor
		p1--

		cor += h[ptrH1] * h[ptrH1]
		ptrH1++
		rri0i0[p0] = cor
		p0--
	}

	lFinSup := MSize - 1
	lFinInf := lFinSup - 1
	ldec := NbPos + 1

	ptrHD := 0
	ptrHF := ptrHD + 1

	for k := 0; k < NbPos; k++ {
		p3Idx := lFinSup
		p2Idx := lFinSup
		p1Idx := lFinSup
		p0Idx := lFinInf
		cor = 0
		h1 := ptrHD
		h2 := ptrHF

		for i := k + 1; i < NbPos; i++ {
			cor += h[h1] * h[h2]
			h1++
			h2++
			cor += h[h1] * h[h2]
			h1++
			h2++
			rri2i3[p3Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri1i2[p2Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i1[p1Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i4[p0Idx] = cor

			p3Idx -= ldec
			p2Idx -= ldec
			p1Idx -= ldec
			p0Idx -= ldec
		}
		cor += h[h1] * h[h2]
		h1++
		h2++
		cor += h[h1] * h[h2]
		h1++
		h2++
		rri2i3[p3Idx] = cor

		cor += h[h1] * h[h2]
		h1++
		h2++
		rri1i2[p2Idx] = cor

		cor += h[h1] * h[h2]
		h1++
		h2++
		rri0i1[p1Idx] = cor

		lFinSup -= NbPos
		lFinInf--
		ptrHF += Step
	}

	ptrHD = 0
	ptrHF = ptrHD + 2
	lFinSup = MSize - 1
	lFinInf = lFinSup - 1
	for k := 0; k < NbPos; k++ {
		p4Idx := lFinSup
		p3Idx := lFinSup
		p2Idx := lFinSup
		p1Idx := lFinInf
		p0Idx := lFinInf

		cor = 0
		h1 := ptrHD
		h2 := ptrHF
		for i := k + 1; i < NbPos; i++ {
			cor += h[h1] * h[h2]
			h1++
			h2++
			rri2i4[p4Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri1i3[p3Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i2[p2Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri1i4[p1Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i3[p0Idx] = cor

			p4Idx -= ldec
			p3Idx -= ldec
			p2Idx -= ldec
			p1Idx -= ldec
			p0Idx -= ldec
		}
		cor += h[h1] * h[h2]
		h1++
		h2++
		rri2i4[p4Idx] = cor

		cor += h[h1] * h[h2]
		h1++
		h2++
		rri1i3[p3Idx] = cor

		cor += h[h1] * h[h2]
		h1++
		h2++
		rri0i2[p2Idx] = cor

		lFinSup -= NbPos
		lFinInf--
		ptrHF += Step
	}

	ptrHD = 0
	ptrHF = ptrHD + 3
	lFinSup = MSize - 1
	lFinInf = lFinSup - 1
	for k := 0; k < NbPos; k++ {
		p4Idx := lFinSup
		p3Idx := lFinSup
		p2Idx := lFinInf
		p1Idx := lFinInf
		p0Idx := lFinInf

		h1 := ptrHD
		h2 := ptrHF
		cor = 0
		for i := k + 1; i < NbPos; i++ {
			cor += h[h1] * h[h2]
			h1++
			h2++
			rri1i4[p4Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i3[p3Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri2i4[p2Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri1i3[p1Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i2[p0Idx] = cor

			p4Idx -= ldec
			p3Idx -= ldec
			p2Idx -= ldec
			p1Idx -= ldec
			p0Idx -= ldec
		}
		cor += h[h1] * h[h2]
		h1++
		h2++
		rri1i4[p4Idx] = cor

		cor += h[h1] * h[h2]
		h1++
		h2++
		rri0i3[p3Idx] = cor

		lFinSup -= NbPos
		lFinInf--
		ptrHF += Step
	}

	ptrHD = 0
	ptrHF = ptrHD + 4
	lFinSup = MSize - 1
	lFinInf = lFinSup - 1
	for k := 0; k < NbPos; k++ {
		p3Idx := lFinSup
		p2Idx := lFinInf
		p1Idx := lFinInf
		p0Idx := lFinInf

		h1 := ptrHD
		h2 := ptrHF
		cor = 0
		for i := k + 1; i < NbPos; i++ {
			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i4[p3Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			cor += h[h1] * h[h2]
			h1++
			h2++
			rri2i3[p2Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri1i2[p1Idx] = cor

			cor += h[h1] * h[h2]
			h1++
			h2++
			rri0i1[p0Idx] = cor

			p3Idx -= ldec
			p2Idx -= ldec
			p1Idx -= ldec
			p0Idx -= ldec
		}
		cor += h[h1] * h[h2]
		h1++
		h2++
		rri0i4[p3Idx] = cor

		lFinSup -= NbPos
		lFinInf--
		ptrHF += Step
	}
}
