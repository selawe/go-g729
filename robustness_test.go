package g729

import (
	"testing"
)

// TestEncoder_ExtremeInputRegression verifies that extreme PCM boundary values
// never cause panics or errors across repeated frames on the same encoder instance.
// Covers: all-min (-32768), all-max (+32767), Nyquist rail-to-rail alternating.
func TestEncoder_ExtremeInputRegression(t *testing.T) {
	enc := NewEncoder(Config{Variant: VariantG729A, EnableVAD: false})
	var dst [10]byte
	var pcm [80]int16

	for rep := 0; rep < 20; rep++ {
		for i := range pcm {
			pcm[i] = -32768
		}
		if _, _, err := enc.Encode(dst[:], pcm[:]); err != nil {
			t.Fatalf("rep %d all-min: %v", rep, err)
		}

		for i := range pcm {
			pcm[i] = 32767
		}
		if _, _, err := enc.Encode(dst[:], pcm[:]); err != nil {
			t.Fatalf("rep %d all-max: %v", rep, err)
		}

		for i := range pcm {
			if i%2 == 0 {
				pcm[i] = -32768
			} else {
				pcm[i] = 32767
			}
		}
		if _, _, err := enc.Encode(dst[:], pcm[:]); err != nil {
			t.Fatalf("rep %d nyquist rail-to-rail: %v", rep, err)
		}
	}
}
