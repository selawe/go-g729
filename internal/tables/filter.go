// Code generated from ITU-T G.729A tab_ld8a.c; DO NOT EDIT.

package tables

// HPF 140 Hz 2nd-order IIR filter coefficients (b140 and a140 from ITU-T G.729 Annex C).
// Difference equation: y[n] = b[0]*x[n] + b[1]*x[n-1] + b[2]*x[n-2] + a[1]*y[n-1] + a[2]*y[n-2]
var (
	HPF140_B = [3]float32{0.92727435, -1.8544941, 0.92727435}
	HPF140_A = [3]float32{1.00000000,  1.9059465, -0.91140240}

	HPF100_B = [3]float32{0.93980581, -1.8795834, 0.93980581}
	HPF100_A = [3]float32{1.00000000,  1.9330735, -0.93589199}

	// HPF_B and HPF_A aliases for 140 Hz pre-processing filter
	HPF_B = HPF140_B
	HPF_A = HPF140_A
)

