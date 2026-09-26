package dsp

import "errors"

var (
	// ErrSingularMatrix is returned when the autocorrelation matrix is singular or non-positive definite.
	ErrSingularMatrix = errors.New("g729: singular or non-positive definite autocorrelation matrix")

	// ErrUnstableFilter is returned when the computed LPC filter is unstable (reflection coefficient >= 1.0).
	ErrUnstableFilter = errors.New("g729: unstable LPC synthesis filter")

	// ErrInvalidInput is returned when input slices have insufficient length or invalid dimensions.
	ErrInvalidInput = errors.New("g729: invalid input slice length for DSP operation")
)
