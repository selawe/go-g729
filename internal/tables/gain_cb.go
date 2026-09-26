// Code generated from ITU-T G.729A tab_ld8a.c; DO NOT EDIT.

package tables

// GACodebook is the 3-bit gain stage 1 codebook (8 entries: [pitch gain in Q14, code gain correction factor in Q12]).
var GACodebook = [8][2]float32{
	{ 0.0000000,  0.1850586},
	{ 0.0946655,  0.2961426},
	{ 0.1117554,  0.6130371},
	{ 0.0034790,  0.6596680},
	{ 0.1172485,  1.1342773},
	{ 0.1978760,  1.2145996},
	{ 0.0217285,  1.8012695},
	{ 0.1634521,  3.3156738},
}

// GBCodebook is the 4-bit gain stage 2 codebook (16 entries: [pitch gain in Q14, code gain correction factor in Q12]).
var GBCodebook = [16][2]float32{
	{ 0.0504150,  0.2448730},
	{ 0.1217041,  0.0000000},
	{ 0.3138428,  0.0722656},
	{ 0.3759766,  0.2922363},
	{ 0.4938354,  0.5935059},
	{ 0.5566406,  0.0642090},
	{ 0.6453247,  0.3620605},
	{ 0.7061157,  0.1459961},
	{ 0.8093262,  0.3974609},
	{ 0.8663330,  0.1989746},
	{ 0.9235840,  0.5998535},
	{ 0.9253540,  1.7426758},
	{ 0.9420166,  0.0290527},
	{ 0.9833984,  0.4140625},
	{ 1.0558472,  0.2272949},
	{ 1.1580200,  0.7246094},
}

// MAPredGainCoeffs are the Moving Average prediction coefficients for energy in log domain: {0.68, 0.58, 0.34, 0.19}.
var MAPredGainCoeffs = [4]float32{0.68, 0.58, 0.34, 0.19}

// MapGA and MapGB maps index for conjugate structure vector quantization
var MapGA = [8]uint8{5, 1, 4, 7, 3, 0, 6, 2}
var InvMapGA = [8]uint8{5, 1, 7, 4, 2, 0, 6, 3}
var MapGB = [16]uint8{4, 6, 0, 2, 12, 14, 8, 10, 15, 11, 9, 13, 7, 3, 1, 5}
var InvMapGB = [16]uint8{2, 14, 3, 13, 0, 15, 1, 12, 6, 10, 7, 9, 4, 11, 5, 8}

// GainCoef is the 2x2 matrix for preselection of gain codebook in G.729 / G.729A.
var GainCoef = [2][2]float32{
	{31.134575, 1.612322},
	{0.481389, 0.053056},
}

// GainThr1 is the threshold array for preselection of codebook 1 (GA, 4 thresholds for 4 candidates out of 8).
var GainThr1 = [4]float32{
	0.659681, 0.755274, 1.207205, 1.987740,
}

// GainThr2 is the threshold array for preselection of codebook 2 (GB, 8 thresholds for 8 candidates out of 16).
var GainThr2 = [8]float32{
	0.429912, 0.494045, 0.618737, 0.650676,
	0.717949, 0.770050, 0.850628, 0.932089,
}

// SIDGainTable contains the 32 quantized energy levels for Annex B SID frame (Q3 / 8.0).
var SIDGainTable = [32]float32{
	 0.2500000,
	 0.6250000,
	 1.0000000,
	 1.6250000,
	 2.5000000,
	 4.0000000,
	 6.2500000,
	 8.0000000,
	10.0000000,
	12.6250000,
	15.8750000,
	20.0000000,
	25.1250000,
	31.6250000,
	39.7500000,
	50.1250000,
	63.1250000,
	79.3750000,
	100.0000000,
	125.8750000,
	158.5000000,
	199.5000000,
	251.2500000,
	316.2500000,
	398.1250000,
	501.1250000,
	631.0000000,
	794.3750000,
	1000.0000000,
	1258.8750000,
	1584.8750000,
	1995.2500000,
}
