package lsp

import (
	"math"

	"github.com/selawe/go-g729/internal/params"
)

const (
	LLimit = float32(0.005)
	MLimit = float32(3.135)
	Gap1   = float32(0.0012)
	Gap2   = float32(0.0006)
	Gap3   = float32(0.0392)
)

// LSF2LSP converts line spectral frequencies (in radians, 0 < lsf < pi)
// to line spectral pairs (in cosine domain, 1 > lsp > -1).
func LSF2LSP(lsf [params.M]float32) (lsp [params.M]float32) {
	for i := 0; i < params.M; i++ {
		lsp[i] = float32(math.Cos(float64(lsf[i])))
	}
	return lsp
}

// LSP2LSF converts line spectral pairs (cosine domain)
// to line spectral frequencies (in radians).
func LSP2LSF(lsp [params.M]float32) (lsf [params.M]float32) {
	for i := 0; i < params.M; i++ {
		v := float64(lsp[i])
		if v > 1.0 {
			v = 1.0
		} else if v < -1.0 {
			v = -1.0
		}
		lsf[i] = float32(math.Acos(v))
	}
	return lsf
}

// StabilizeLSF ensures monotonic ascending order, boundary limits,
// and minimum distance between adjacent LSF coefficients.
func StabilizeLSF(buf *[params.M]float32, minGap float32) {
	if minGap <= 0 {
		minGap = Gap3
	}

	// 1. Ensure ascending order
	for j := 0; j < params.M-1; j++ {
		if buf[j+1] < buf[j] {
			buf[j], buf[j+1] = buf[j+1], buf[j]
		}
	}

	// 2. Enforce lower limit
	if buf[0] < LLimit {
		buf[0] = LLimit
	}

	// 3. Enforce minimum gap between adjacent frequencies
	for j := 0; j < params.M-1; j++ {
		if buf[j+1]-buf[j] < minGap {
			buf[j+1] = buf[j] + minGap
		}
	}

	// 4. Enforce upper limit
	if buf[params.M-1] > MLimit {
		buf[params.M-1] = MLimit
	}
}

// Expand1 enforces minimum gap separation for the lower LSFs (indices 0..4).
func Expand1(buf []float32, gap float32) {
	for j := 1; j < 5 && j < len(buf); j++ {
		diff := buf[j-1] - buf[j]
		tmp := (diff + gap) * 0.5
		if tmp > 0 {
			buf[j-1] -= tmp
			buf[j] += tmp
		}
	}
}

// Expand2 enforces minimum gap separation for the higher LSFs (indices 5..9).
func Expand2(buf []float32, gap float32) {
	for j := 5; j < params.M && j < len(buf); j++ {
		diff := buf[j-1] - buf[j]
		tmp := (diff + gap) * 0.5
		if tmp > 0 {
			buf[j-1] -= tmp
			buf[j] += tmp
		}
	}
}

// Expand12 enforces minimum gap separation across all adjacent LSF pairs (0..9).
func Expand12(buf []float32, gap float32) {
	for j := 1; j < params.M && j < len(buf); j++ {
		diff := buf[j-1] - buf[j]
		tmp := (diff + gap) * 0.5
		if tmp > 0 {
			buf[j-1] -= tmp
			buf[j] += tmp
		}
	}
}
