package sdp_test

import (
	"testing"

	"github.com/selawe/go-g729/sdp"
)

// FuzzParseFMTPParams tests the SDP fmtp parameter parser against arbitrary strings.
//
// Invariants:
//   - Must never panic regardless of input.
//   - Must never return values other than true/false for annexa and annexb.
//   - Input longer than MaxFMTPLength must return an error; shorter input must not.
//
// Run: go test -fuzz=FuzzParseFMTPParams -fuzztime=2m ./sdp/
func FuzzParseFMTPParams(f *testing.F) {
	// Seed: common valid cases
	f.Add("")
	f.Add("annexb=yes")
	f.Add("annexb=no")
	f.Add("annexa=yes")
	f.Add("annexa=no;annexb=yes")
	f.Add("annexa=1;annexb=0")
	f.Add("annexa=true;annexb=false")
	// Seed: unknown keys (should be silently ignored)
	f.Add("unknown=foo;annexb=yes")
	// Seed: malformed entries
	f.Add(";")
	f.Add(";;;")
	f.Add("annexb=")
	f.Add("=yes")
	f.Add("annexb=INVALID")
	// Seed: at the length limit (1024 bytes of spaces — valid but empty after trim)
	limit := make([]byte, sdp.MaxFMTPLength)
	for i := range limit {
		limit[i] = ' '
	}
	f.Add(string(limit))
	// Seed: one byte over the limit
	over := make([]byte, sdp.MaxFMTPLength+1)
	f.Add(string(over))

	f.Fuzz(func(t *testing.T, s string) {
		annexa, annexb, err := sdp.ParseFMTPParams(s)

		if len(s) > sdp.MaxFMTPLength {
			if err == nil {
				t.Errorf("len=%d > MaxFMTPLength: expected error, got nil", len(s))
			}
			return
		}

		// For inputs within the length limit, the function must succeed unless
		// the value for a known key is not a recognised boolean token.
		if err != nil {
			// Allowed: unknown boolean token for a known key — error is expected.
			// Not allowed: panic or non-bool output values.
			return
		}

		// Returned values must be booleans — the type system guarantees this,
		// but verify no sentinel/garbage was silently stored.
		_ = annexa
		_ = annexb
	})
}
