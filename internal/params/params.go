// Package params contains constants and parameters for the G.729 speech codec
// as defined in ITU-T Recommendations G.729, G.729 Annex A, and G.729 Annex B.
package params

const (
	// SampleRate is the standard acoustic sampling rate in Hz.
	SampleRate = 8000

	// L_FRAME is the number of speech samples in one 10 ms frame.
	L_FRAME = 80

	// L_SUBFR is the number of speech samples in one 5 ms subframe.
	L_SUBFR = 40

	// N_SUBFR is the number of subframes in one frame.
	N_SUBFR = 2

	// M is the order of the Linear Prediction (LP) filter.
	M = 10

	// L_WINDOW is the length of the asymmetric Hamming analysis window.
	L_WINDOW = 240

	// L_LOOKAHEAD is the speech lookahead delay in samples (5 ms).
	L_LOOKAHEAD = 40

	// L_PAST_SPEECH is the past speech history required for the 240-point analysis window.
	// 240 (window) = 120 (past) + 80 (frame) + 40 (lookahead).
	L_PAST_SPEECH = 120

	// L_TOTAL is the total size of the speech buffer in the encoder (120 past + 80 frame + 40 lookahead).
	L_TOTAL = L_PAST_SPEECH + L_FRAME + L_LOOKAHEAD

	// PIT_MIN is the minimum pitch delay (lag) in samples.
	PIT_MIN = 20

	// PIT_MAX is the maximum pitch delay (lag) in samples.
	PIT_MAX = 143

	// UP_SAMP is the fractional pitch interpolation factor (1/3 resolution).
	UP_SAMP = 3

	// L_INTER10 is the length of the sinc interpolation filter on each side for pitch interpolation.
	L_INTER10 = 10

	// FIR_SIZE_SYN is the total length of the fractional pitch sinc filter table (UP_SAMP*L_INTER10 + 1).
	FIR_SIZE_SYN = UP_SAMP*L_INTER10 + 1

	// L_PAST_EXC is the past excitation history needed for maximum pitch lag and sinc interpolation.
	L_PAST_EXC = PIT_MAX + L_INTER10 + 1 // 154 samples

	// EXC_BUF_LEN is the total size of the excitation buffer (154 past + 80 frame).
	EXC_BUF_LEN = L_PAST_EXC + L_FRAME // 234 samples

	// Perceptual weighting filter factors (fixed in G.729 Annex A).
	GAMMA1 = float32(0.9)
	GAMMA2 = float32(0.6)

	// Post-filter factors.
	GAMMA_P = float32(0.5)  // Long-term pitch post-filter factor
	GAMMA_N = float32(0.55) // Short-term formant post-filter numerator factor
	GAMMA_D = float32(0.70) // Short-term formant post-filter denominator factor
	GAMMA_T = float32(0.8)  // Spectral tilt compensation factor
	AGC_FAC = float32(0.9)  // Adaptive Gain Control smoothing factor

	// Bitstream sizes.
	BITS_PER_FRAME  = 80
	BYTES_PER_FRAME = 10
	BITS_PER_SID    = 16
	BYTES_PER_SID   = 2

	// Codebook sizes.
	MA_NP  = 4   // Order of the Moving Average (MA) predictor for LSP and gain
	NC0    = 128 // Stage 1 LSP codebook entries (7 bits)
	NC1    = 32  // Stage 2 LSP codebooks (L2 and L3) entries (5 bits each)
	NCODE1 = 8   // GA codebook entries (3 bits)
	NCODE2 = 16  // GB codebook entries (4 bits)

	// LSP constants.
	// GRID_POINTS is the number of equal-cosine intervals in the Chebyshev grid.
	// Grid50 contains 51 sample points covering 50 intervals (cos(pi*k/50) for k=0..50).
	GRID_POINTS = 50
	LSP_GAP     = float32(0.005) // Minimum distance separation between adjacent LSFs (radians)

	// Gain quantization limits and constants.
	GP_CLIP    = float32(0.95)   // Pitch gain clipping limit
	GP_MIN     = float32(0.0)    // Minimum pitch gain
	GP_MAX     = float32(1.2)    // Maximum pitch gain
	MEAN_ENER  = float32(36.0)   // Mean energy in dB for gain prediction
	NCAN1      = 4               // Pre-selecting candidate count for GA
	NCAN2      = 8               // Pre-selecting candidate count for GB
	INV_COEF   = float32(-0.032623) // Inverse determinant factor for gain preselection
	GPCLIP2    = float32(0.94)   // Pitch gain clip during taming
	GP0999     = float32(0.9999) // Pitch gain threshold during taming
	THRESH_ERR = float32(60000.0)// Error threshold for taming

	// Annex B VAD / DTX / CNG constants.
	HANG_COUNT = 8  // DTX hangover frames before entering inactive state
	SID_FREQ   = 8  // SID frame repetition interval during silence
	SEED_INIT  = 21845 // Initial pseudo-random seed for CNG excitation
)
