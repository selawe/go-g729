package sdp_test

import (
	"strings"
	"testing"

	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/sdp"
)

func TestFMTPLine(t *testing.T) {
	cases := []struct {
		cfg  g729.Config
		want string
	}{
		{g729.Config{EnableVAD: true}, "a=fmtp:18 annexb=yes"},
		{g729.Config{EnableVAD: false}, "a=fmtp:18 annexb=no"},
	}
	for _, c := range cases {
		got := sdp.FMTPLine(sdp.DefaultPayloadType, c.cfg)
		if got != c.want {
			t.Errorf("FMTPLine(%v) = %q, want %q", c.cfg.EnableVAD, got, c.want)
		}
	}
}

func TestRTPMapLine(t *testing.T) {
	got := sdp.RTPMapLine(18)
	if got != "a=rtpmap:18 G729/8000" {
		t.Errorf("RTPMapLine = %q", got)
	}
}

func TestMediaSection(t *testing.T) {
	cfg := g729.Config{EnableVAD: true}
	section, err := sdp.MediaSection(8000, 18, cfg)
	if err != nil {
		t.Fatalf("MediaSection error: %v", err)
	}
	lines := strings.Split(section, "\r\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), section)
	}
	if lines[0] != "m=audio 8000 RTP/AVP 18" {
		t.Errorf("line 0: %q", lines[0])
	}
	if lines[1] != "a=rtpmap:18 G729/8000" {
		t.Errorf("line 1: %q", lines[1])
	}
	if lines[2] != "a=fmtp:18 annexb=yes" {
		t.Errorf("line 2: %q", lines[2])
	}
}

func TestMediaSectionInvalidInputs(t *testing.T) {
	cfg := g729.Config{}
	if _, err := sdp.MediaSection(0, 18, cfg); err == nil {
		t.Error("expected error for port=0")
	}
	if _, err := sdp.MediaSection(65536, 18, cfg); err == nil {
		t.Error("expected error for port=65536")
	}
	if _, err := sdp.MediaSection(8000, -1, cfg); err == nil {
		t.Error("expected error for payloadType=-1")
	}
	if _, err := sdp.MediaSection(8000, 128, cfg); err == nil {
		t.Error("expected error for payloadType=128")
	}
}

func TestParseFMTP(t *testing.T) {
	cases := []struct {
		in      string
		wantVAD bool
		wantErr bool
	}{
		// Standard values
		{"annexb=yes", true, false},
		{"annexb=no", false, false},
		{"annexb=1", true, false},
		{"annexb=0", false, false},
		// Case insensitive
		{"annexb=YES", true, false},
		{"annexb=No", false, false},
		// Whitespace tolerance
		{" annexb=yes ", true, false},
		{"annexb = yes", true, false},
		// Empty → default (true)
		{"", true, false},
		{"   ", true, false},
		// Semi-colon separated (future extensions)
		{"annexb=yes;unknown=foo", true, false},
		{"annexb=no;unknown=bar", false, false},
		// Invalid value
		{"annexb=maybe", false, true},
	}
	for _, c := range cases {
		got, err := sdp.ParseFMTP(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseFMTP(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseFMTP(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got != c.wantVAD {
			t.Errorf("ParseFMTP(%q) = %v, want %v", c.in, got, c.wantVAD)
		}
	}
}

func TestParseFMTPLine(t *testing.T) {
	cases := []struct {
		line string
		pt   int
		want bool
	}{
		{"a=fmtp:18 annexb=yes", 18, true},
		{"a=fmtp:18 annexb=no", 18, false},
		// Without "a=fmtp:" prefix
		{"18 annexb=yes", 18, true},
		// Without payload type prefix (pt=0)
		{"annexb=no", 0, false},
		// Full line, zero pt → strip any leading number
		{"18 annexb=yes", 0, true},
	}
	for _, c := range cases {
		got, err := sdp.ParseFMTPLine(c.line, c.pt)
		if err != nil {
			t.Errorf("ParseFMTPLine(%q, %d): %v", c.line, c.pt, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseFMTPLine(%q, %d) = %v, want %v", c.line, c.pt, got, c.want)
		}
	}
}

func TestParseFMTPLineMismatch(t *testing.T) {
	_, err := sdp.ParseFMTPLine("a=fmtp:96 annexb=no", 18)
	if err == nil {
		t.Fatal("expected error for payload type mismatch, got nil")
	}
}

func TestConfigFromFMTP(t *testing.T) {
	cfg, err := sdp.ConfigFromFMTP("annexb=yes")
	if err != nil {
		t.Fatalf("ConfigFromFMTP: %v", err)
	}
	if !cfg.EnableVAD {
		t.Error("expected EnableVAD=true for annexb=yes")
	}
	if cfg.Variant != g729.VariantG729A {
		t.Errorf("expected VariantG729A, got %v", cfg.Variant)
	}

	cfg2, err := sdp.ConfigFromFMTP("annexb=no")
	if err != nil {
		t.Fatalf("ConfigFromFMTP(no): %v", err)
	}
	if cfg2.EnableVAD {
		t.Error("expected EnableVAD=false for annexb=no")
	}
}

func TestConfigFromFMTP_AnnexA(t *testing.T) {
	cfg, err := sdp.ConfigFromFMTP("annexa=no; annexb=yes")
	if err != nil {
		t.Fatalf("ConfigFromFMTP: %v", err)
	}
	if cfg.Variant != g729.VariantG729 {
		t.Errorf("expected VariantG729 for annexa=no, got %v", cfg.Variant)
	}
	if !cfg.EnableVAD {
		t.Error("expected EnableVAD=true for annexb=yes")
	}

	line := sdp.FMTPLine(18, cfg)
	if !strings.Contains(line, "annexa=no") {
		t.Errorf("FMTPLine for VariantG729 should contain annexa=no, got: %s", line)
	}
}

func TestNegotiateAnnexA(t *testing.T) {
	cases := []struct {
		offer, answer string
		want          bool
	}{
		{"annexa=yes", "annexa=yes", true},
		{"annexa=yes", "annexa=no", false},
		{"annexa=no", "annexa=yes", false},
		{"annexa=no", "annexa=no", false},
		{"", "", true},
	}
	for _, c := range cases {
		got, err := sdp.NegotiateAnnexA(c.offer, c.answer)
		if err != nil {
			t.Errorf("NegotiateAnnexA(%q, %q): %v", c.offer, c.answer, err)
			continue
		}
		if got != c.want {
			t.Errorf("NegotiateAnnexA(%q, %q) = %v, want %v", c.offer, c.answer, got, c.want)
		}
	}

	// Verify error cases return false
	if got, err := sdp.NegotiateAnnexA("annexa=invalid", "annexa=yes"); err == nil || got != false {
		t.Errorf("expected error and false for invalid offer, got %v, err=%v", got, err)
	}
	if got, err := sdp.NegotiateAnnexA("annexa=yes", "annexa=invalid"); err == nil || got != false {
		t.Errorf("expected error and false for invalid answer, got %v, err=%v", got, err)
	}
}

func TestNegotiateAnnexB(t *testing.T) {
	cases := []struct {
		offer, answer string
		want          bool
	}{
		// Both yes → yes
		{"annexb=yes", "annexb=yes", true},
		// One side says no → no
		{"annexb=yes", "annexb=no", false},
		{"annexb=no", "annexb=yes", false},
		// Both no → no
		{"annexb=no", "annexb=no", false},
		// Absent fmtp → default (yes)
		{"", "", true},
		{"", "annexb=no", false},
		{"annexb=yes", "", true},
	}
	for _, c := range cases {
		got, err := sdp.NegotiateAnnexB(c.offer, c.answer)
		if err != nil {
			t.Errorf("NegotiateAnnexB(%q, %q): %v", c.offer, c.answer, err)
			continue
		}
		if got != c.want {
			t.Errorf("NegotiateAnnexB(%q, %q) = %v, want %v", c.offer, c.answer, got, c.want)
		}
	}
}

func TestConfigFromNegotiation(t *testing.T) {
	cfg, err := sdp.ConfigFromNegotiation("annexb=yes", "annexb=yes")
	if err != nil || !cfg.EnableVAD {
		t.Errorf("yes+yes: got err=%v vad=%v", err, cfg.EnableVAD)
	}

	cfg, err = sdp.ConfigFromNegotiation("annexb=yes", "annexb=no")
	if err != nil || cfg.EnableVAD {
		t.Errorf("yes+no: got err=%v vad=%v", err, cfg.EnableVAD)
	}
}

// TestFMTPRoundTrip verifies FMTPLine → ParseFMTP → Config round-trip
func TestFMTPRoundTrip(t *testing.T) {
	for _, enableVAD := range []bool{true, false} {
		origCfg := g729.Config{Variant: g729.VariantG729A, EnableVAD: enableVAD}
		line := sdp.FMTPLine(18, origCfg)

		annexb, err := sdp.ParseFMTPLine(line, 18)
		if err != nil {
			t.Fatalf("ParseFMTPLine(%q): %v", line, err)
		}
		if annexb != enableVAD {
			t.Errorf("round-trip failed: EnableVAD %v → FMTPLine → ParseFMTP → %v", enableVAD, annexb)
		}
	}
}

// TestSDPEncoderIntegration verifies Config derived from SDP produces correct
// encoder behavior: annexb=yes → SID frames appear during silence.
func TestSDPEncoderIntegration(t *testing.T) {
	cfg, err := sdp.ConfigFromFMTP("annexb=yes")
	if err != nil {
		t.Fatalf("ConfigFromFMTP: %v", err)
	}

	enc := g729.NewEncoder(cfg)
	silence := make([]int16, 80)
	dst := make([]byte, 10)

	// Feed silence until DTX kicks in
	var gotSID bool
	for i := 0; i < 50 && !gotSID; i++ {
		n, ft, err := enc.Encode(dst, silence)
		if err != nil {
			t.Fatalf("frame %d encode: %v", i, err)
		}
		if ft == g729.FrameSID && n == 2 {
			gotSID = true
		}
		if ft == g729.FrameUntransmitted && n == 0 {
			gotSID = true // DTX suppressed → Annex B is working
		}
	}
	if !gotSID {
		t.Error("annexb=yes: expected SID or suppressed frame during 50 silence frames, got only speech")
	}

	// annexb=no → only speech frames, even during silence
	cfgNO, _ := sdp.ConfigFromFMTP("annexb=no")
	encNO := g729.NewEncoder(cfgNO)
	for i := 0; i < 50; i++ {
		n, ft, err := encNO.Encode(dst, silence)
		if err != nil {
			t.Fatalf("no-annexb frame %d: %v", i, err)
		}
		if ft != g729.FrameSpeech || n != 10 {
			t.Errorf("annexb=no frame %d: got ft=%v n=%d, want FrameSpeech 10 bytes", i, ft, n)
		}
	}
}

// TestParseFMTPParamsRejectsOversizedInput ensures oversized fmtp values are
// rejected before entering strings.Split, blocking a DoS vector where
// attacker-supplied SDP causes unbounded allocation.
func TestParseFMTPParamsRejectsOversizedInput(t *testing.T) {
	// Build a fmtp value of MaxFMTPLength+1 bytes.
	big := strings.Repeat("a=b;", sdp.MaxFMTPLength) // ~4 KiB
	if len(big) <= sdp.MaxFMTPLength {
		t.Fatalf("test setup: repeat produced %d bytes, expected > %d", len(big), sdp.MaxFMTPLength)
	}

	_, _, err := sdp.ParseFMTPParams(big)
	if err == nil {
		t.Fatal("expected error for oversized fmtp input, got nil")
	}
	// The wrapped sentinel should be ErrInvalidValue.
	if !strings.Contains(err.Error(), "MaxFMTPLength") {
		t.Errorf("expected error mentioning MaxFMTPLength, got: %v", err)
	}
}

// TestParseFMTPParamsAcceptsAtBoundary verifies the length check is inclusive:
// exactly MaxFMTPLength bytes must still parse successfully.
func TestParseFMTPParamsAcceptsAtBoundary(t *testing.T) {
	// annexb=yes is 11 bytes; pad with harmless ignored keys up to exactly the limit.
	prefix := "annexb=yes"
	pad := strings.Repeat(";x=y", (sdp.MaxFMTPLength-len(prefix))/4)
	input := prefix + pad
	if len(input) > sdp.MaxFMTPLength {
		input = input[:sdp.MaxFMTPLength]
	}

	_, annexb, err := sdp.ParseFMTPParams(input)
	if err != nil {
		t.Fatalf("input of %d bytes should parse, got err: %v", len(input), err)
	}
	if !annexb {
		t.Errorf("annexb=yes at boundary length should parse true, got false")
	}
}

func TestTelephoneEventSDP(t *testing.T) {
	if sdp.DefaultTelephoneEventPayloadType != 101 {
		t.Errorf("DefaultTelephoneEventPayloadType = %d, want 101", sdp.DefaultTelephoneEventPayloadType)
	}
	if sdp.TelephoneEventClockRate != 8000 {
		t.Errorf("TelephoneEventClockRate = %d, want 8000", sdp.TelephoneEventClockRate)
	}

	rtpmap := sdp.TelephoneEventRTPMapLine(101)
	if rtpmap != "a=rtpmap:101 telephone-event/8000" {
		t.Errorf("TelephoneEventRTPMapLine = %q, want 'a=rtpmap:101 telephone-event/8000'", rtpmap)
	}

	fmtp := sdp.TelephoneEventFMTPLine(101)
	if fmtp != "a=fmtp:101 0-16" {
		t.Errorf("TelephoneEventFMTPLine = %q, want 'a=fmtp:101 0-16'", fmtp)
	}
}
