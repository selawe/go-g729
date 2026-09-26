package pitch

import (
	"github.com/selawe/go-g729/internal/params"
)

// EncodePitch encodes integer pitch lag t0 and fraction frac into an index.
// Subframe 0 (1st subframe) produces an 8-bit index (0..255) and computes t0Min, t0Max for subframe 2.
// Subframe 1 (2nd subframe) produces a 5-bit index (0..31) relative to t0Min.
func EncodePitch(t0 int, frac int, subframe int, t0Min, t0Max *int) int {
	if subframe == 0 {
		var index int
		if t0 <= 85 {
			index = t0*3 - 58 + frac
		} else {
			index = t0 + 112
		}

		min := t0 - 5
		if min < params.PIT_MIN {
			min = params.PIT_MIN
		}
		max := min + 9
		if max > params.PIT_MAX {
			max = params.PIT_MAX
			min = max - 9
		}
		if t0Min != nil {
			*t0Min = min
		}
		if t0Max != nil {
			*t0Max = max
		}
		return index
	}

	min := params.PIT_MIN
	if t0Min != nil {
		min = *t0Min
	}
	index := (t0-min)*3 + 2 + frac
	return index
}

// DecodePitch decodes a pitch index back into integer lag t0 and fraction frac (-1, 0, 1).
// For subframe 0: updates t0Min and t0Max for the next subframe.
// For subframe 1: uses t0Min computed from subframe 0.
func DecodePitch(index int, subframe int, t0Min, t0Max *int) (t0 int, frac int) {
	if subframe == 0 {
		if index < 197 {
			t0 = (index+2)/3 + 19
			frac = index - t0*3 + 58
		} else {
			t0 = index - 112
			frac = 0
		}

		min := t0 - 5
		if min < params.PIT_MIN {
			min = params.PIT_MIN
		}
		max := min + 9
		if max > params.PIT_MAX {
			max = params.PIT_MAX
			min = max - 9
		}
		if t0Min != nil {
			*t0Min = min
		}
		if t0Max != nil {
			*t0Max = max
		}
		return t0, frac
	}

	min := params.PIT_MIN
	if t0Min != nil {
		min = *t0Min
	}
	i := (index+2)/3 - 1
	t0 = i + min
	frac = index - 2 - i*3
	return t0, frac
}
