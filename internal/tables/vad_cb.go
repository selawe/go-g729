// Code generated and adapted from ITU-T G.729 Annex B / Annex C+ tab_dtx.c; DO NOT EDIT.

package tables

// LowBandFilter contains the 13-tap FIR filter coefficients for VAD in Q15 / 32768.0.
var LowBandFilter = [13]float32{
	0.2401428,
	0.2139587,
	0.1476440,
	0.0701599,
	0.0097961,
	-0.0201416,
	-0.0238647,
	-0.0147705,
	-0.0050049,
	0.0000916,
	0.0011902,
	0.0006409,
	0.0001221,
}

// LbfCorr contains the floating-point low-band filter correlation coefficients (NP+1 = 13).
var LbfCorr = [13]float32{
	0.24017939691329,
	0.21398822343783,
	0.14767692339633,
	0.07018811903116,
	0.00980856433051,
	-0.02015934721195,
	-0.02388269958005,
	-0.01480076155002,
	-0.00503292155509,
	0.00012141366508,
	0.00119354245231,
	0.00065908718613,
	0.00015015782285,
}

// Fact contains scaling factors for SID gain quantization average energy.
var Fact = [3]float32{
	0.003125,
	0.00078125,
	0.000390625,
}

// TabSidGain contains the 32 quantized linear SID energy gains.
var TabSidGain = [32]float32{
	0.502, 1.262, 2.000, 3.170, 5.024, 7.962,
	12.619, 15.887, 20.000, 25.179, 31.698, 39.905,
	50.238, 63.246, 79.621, 100.237, 126.191, 158.866,
	200.000, 251.785, 316.979, 399.052, 502.377, 632.456,
	796.214, 1002.374, 1261.915, 1588.656, 2000.000, 2517.851,
	3169.786, 3990.525,
}

// PtrTab1 contains indices mapping SID 1st-stage codebook entries to lspcb1.
var PtrTab1 = [32]int{
	96, 52, 20, 54, 86, 114, 82, 68, 36, 121, 48, 92, 18, 120,
	94, 124, 50, 125, 4, 100, 28, 76, 12, 117, 81, 22, 90, 116,
	127, 21, 108, 66,
}

// PtrTab2 contains indices mapping SID 2nd-stage codebook entries to lspcb2 (lower and upper).
var PtrTab2 = [2][16]int{
	{31, 21, 9, 3, 10, 2, 19, 26, 4, 3, 11, 29, 15, 27, 21, 12},
	{16, 1, 0, 0, 8, 25, 22, 20, 19, 23, 20, 31, 4, 31, 20, 31},
}

// NoiseFgSum contains (1 - sum(NoiseMAPredictor)) for noise quantization modes 0 and 1.
var NoiseFgSum = [2][10]float32{
	{
		0.2379833, 0.2577898, 0.2504044, 0.2530900, 0.2479934,
		0.2587054, 0.2577898, 0.2656026, 0.2759789, 0.2625813,
	},
	{
		0.320883796, 0.378502704, 0.391650136, 0.363609794, 0.349357626,
		0.356157116, 0.339738164, 0.345200944, 0.361461228, 0.349363712,
	},
}

// NoiseFgSumInv contains 1 / (1 - sum(NoiseMAPredictor)) for noise modes 0 and 1.
var NoiseFgSumInv = [2][10]float32{
	{
		4.201788, 3.879025, 3.993530, 3.951048, 4.032350,
		3.865596, 3.879025, 3.765007, 3.623157, 3.807978,
	},
	{
		3.11639295117289, 2.64198905168191, 2.55329925380136, 2.75020094755754, 2.86239636858535,
		2.80774960003888, 2.94344323353675, 2.89686345701303, 2.76654844983817, 2.86234650495126,
	},
}

// NoiseMp contains the weighting factors for noise candidate evaluation across modes 0 and 1.
var NoiseMp = [2]float32{0.065942075, 0.12644604}

// VADA contains the boundary decision slopes for Annex B MakeDec.
var VADA = [14]float32{
	1.750000e-03, -4.545455e-03, -2.500000e+01, 2.000000e+01,
	0.000000e+00, 8.800000e+03, 0.000000e+00, 2.5e+01,
	-2.909091e+01, 0.000000e+00, 1.400000e+04, 0.928571,
	-1.500000e+00, 0.714285,
}

// VADB contains the boundary decision intercepts for Annex B MakeDec.
var VADB = [14]float32{
	0.00085, 0.001159091, -5.0, -6.0, -4.7, -12.2, 0.0009,
	-7.0, -4.8182, -5.3, -15.5, 1.14285, -9.0, -2.1428571,
}
