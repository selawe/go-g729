package dsp

import (
	"github.com/selawe/go-g729/internal/params"
	"github.com/selawe/go-g729/internal/tables"
)

// Autocorr computes the windowed autocorrelation of speech with 60 Hz lag windowing.
// speech must have at least params.L_WINDOW (240) samples.
// window is the 240-point analysis window; if nil, tables.Window240 is used.
// r receives the autocorrelations r[0...m]. m is typically params.M (10).
//
// The process performs:
//  1. Windowing: y[n] = speech[n] * window[n]
//  2. Autocorrelation: r[k] = sum_{j=0}^{239-k} y[j] * y[j+k]
//  3. Energy floor: if r[0] < 1.0 { r[0] = 1.0 }
//  4. Lag windowing: r[0] *= 1.0001 (white noise floor), r[k] *= LagWindow[k]
func Autocorr(r, speech, window []float32, m int) {
	AutocorrRaw(r, speech, window, m)
	LagWindow(r, m)
}

// AutocorrRaw computes the windowed autocorrelation without lag windowing.
// Useful when raw energy or autocorrelation without bandwidth expansion is needed.
func AutocorrRaw(r, speech, window []float32, m int) {
	if len(speech) < params.L_WINDOW {
		panic("dsp: speech length must be at least L_WINDOW (240)")
	}
	if len(r) < m+1 {
		panic("dsp: r slice length must be at least m+1")
	}

	win := window
	if win == nil {
		win = tables.Window240[:]
	} else if len(win) < params.L_WINDOW {
		panic("dsp: window length must be at least L_WINDOW (240)")
	}

	var y [params.L_WINDOW]float32
	_ = speech[params.L_WINDOW-1]
	_ = win[params.L_WINDOW-1]
	for i := 0; i < params.L_WINDOW; i++ {
		y[i] = speech[i] * win[i]
	}

	_ = r[m]
	for k := 0; k <= m; k++ {
		var sum float32
		end := params.L_WINDOW - k
		yj := y[:end]
		yjk := y[k : k+end]
		_ = yjk[end-1]
		j := 0
		for ; j <= end-8; j += 8 {
			s0 := yj[j : j+8]
			s1 := yjk[j : j+8]
			_ = s0[7]
			_ = s1[7]
			sum += s0[0] * s1[0]
			sum += s0[1] * s1[1]
			sum += s0[2] * s1[2]
			sum += s0[3] * s1[3]
			sum += s0[4] * s1[4]
			sum += s0[5] * s1[5]
			sum += s0[6] * s1[6]
			sum += s0[7] * s1[7]
		}
		for ; j < end; j++ {
			sum += yj[j] * yjk[j]
		}
		r[k] = sum
	}

	// ITU-T G.729 minimum energy floor
	if r[0] < 1.0 {
		r[0] = 1.0
	}
}

// LagWindow applies the 60 Hz bandwidth expansion lag window to autocorrelation vector r[0...m].
// r[0] is multiplied by tables.LagWindow[0] (1.0001, white noise correction).
// r[k] is multiplied by tables.LagWindow[k] for k = 1...m.
func LagWindow(r []float32, m int) {
	if len(r) == 0 {
		return
	}
	if len(tables.LagWindow) > 0 {
		r[0] *= tables.LagWindow[0]
	}
	for k := 1; k <= m && k < len(r) && k < len(tables.LagWindow); k++ {
		r[k] *= tables.LagWindow[k]
	}
}
