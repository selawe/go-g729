// Package sdp provides helpers for building and parsing G.729 SDP attributes
// per RFC 4566 (SDP) and RFC 3551 §4.5.6 (G.729 RTP payload format).
//
// # G.729 SDP negotiation
//
// A minimal G.729 SDP media section looks like:
//
//	m=audio 8000 RTP/AVP 18
//	a=rtpmap:18 G729/8000
//	a=fmtp:18 annexb=yes
//
// The "annexb" fmtp parameter controls Annex B (VAD/DTX/CNG) behaviour:
//
//   - annexb=yes  Voice Activity Detection and Discontinuous Transmission
//     are enabled. Silence periods produce SID frames (2 bytes) or are
//     suppressed entirely (0-byte RTP packets). The remote decoder must
//     synthesise comfort noise from the SID parameters.
//
//   - annexb=no   Constant bit-rate mode: the encoder always emits 10-byte
//     speech frames regardless of signal content. No SID frames are sent.
//
// RFC 3551 states the default value is "annexb=yes" when the fmtp attribute
// is absent. Implementations SHOULD include the attribute explicitly to
// avoid ambiguity during SDP offer/answer negotiation.
//
// # Mapping to Config
//
//	cfg, err := sdp.ConfigFromFMTP("annexb=yes")
//	enc := g729.NewEncoder(cfg)
//
//	// Inspect what fmtp line to write into SDP offer:
//	fmt.Println(sdp.FMTPLine(18, cfg)) // "a=fmtp:18 annexb=yes"
package sdp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	g729 "github.com/selawe/go-g729"
)

const (
	// DefaultPayloadType is the IANA-assigned static payload type for G.729.
	DefaultPayloadType = 18

	// ClockRate is the G.729 RTP clock rate in Hz.
	ClockRate = 8000

	// DefaultAnnexA is the default annexa value per RFC 3551 (true = G.729 Annex A).
	DefaultAnnexA = true

	// DefaultAnnexB is the default annexb value per RFC 3551 (true = enabled).
	DefaultAnnexB = true
)

// Sentinel errors.
var (
	// ErrUnknownParam is returned for unrecognised fmtp parameters.
	ErrUnknownParam = errors.New("sdp: unknown G.729 fmtp parameter")

	// ErrInvalidValue is returned when an fmtp parameter has an invalid value.
	ErrInvalidValue = errors.New("sdp: invalid fmtp parameter value")
)

// RTPMapLine returns the a=rtpmap SDP attribute line for G.729.
//
//	a=rtpmap:18 G729/8000
func RTPMapLine(payloadType int) string {
	return fmt.Sprintf("a=rtpmap:%d G729/%d", payloadType, ClockRate)
}

// FMTPLine builds the a=fmtp SDP attribute line for G.729 from a Config.
//
//	a=fmtp:18 annexb=yes              (G.729A, EnableVAD=true)
//	a=fmtp:18 annexb=no               (G.729A, EnableVAD=false)
//	a=fmtp:18 annexa=no; annexb=yes   (Full G.729, EnableVAD=true)
func FMTPLine(payloadType int, cfg g729.Config) string {
	annexb := "no"
	if cfg.EnableVAD {
		annexb = "yes"
	}
	if cfg.Variant == g729.VariantG729 {
		return fmt.Sprintf("a=fmtp:%d annexa=no; annexb=%s", payloadType, annexb)
	}
	return fmt.Sprintf("a=fmtp:%d annexb=%s", payloadType, annexb)
}

// MediaSection returns the minimal SDP media section for G.729 as a
// newline-joined string, ready to embed in an SDP body.
//
//	m=audio <port> RTP/AVP <payloadType>
//	a=rtpmap:<payloadType> G729/8000
//	a=fmtp:<payloadType> annexb=yes|no
func MediaSection(port, payloadType int, cfg g729.Config) string {
	return strings.Join([]string{
		fmt.Sprintf("m=audio %d RTP/AVP %d", port, payloadType),
		RTPMapLine(payloadType),
		FMTPLine(payloadType, cfg),
	}, "\r\n")
}

// ParseFMTP parses a G.729 fmtp attribute value string (the part after
// "a=fmtp:<pt> ") and returns the annexb flag.
//
// Examples of valid input:
//
//	"annexb=yes"
//	"annexb=no"
//	"annexb=1"
//	"annexb=0"
//	""  → returns DefaultAnnexB (true), nil
//
// Unknown additional parameters are silently ignored to allow forward
// compatibility with future G.729 extensions.
// For parsing both annexa and annexb parameters, see ParseFMTPParams.
func ParseFMTP(fmtp string) (annexb bool, err error) {
	_, annexb, err = ParseFMTPParams(fmtp)
	return annexb, err
}

// ParseFMTPParams parses both annexa and annexb parameters from a G.729 fmtp attribute value string.
//
// Defaults per RFC 3551:
//   - annexa: DefaultAnnexA (true = G.729 Annex A)
//   - annexb: DefaultAnnexB (true = VAD/DTX enabled)
//
// Unknown additional parameters are silently ignored to allow forward
// compatibility with future G.729 extensions.
func ParseFMTPParams(fmtp string) (annexa bool, annexb bool, err error) {
	annexa = DefaultAnnexA
	annexb = DefaultAnnexB
	fmtp = strings.TrimSpace(fmtp)
	if fmtp == "" {
		return annexa, annexb, nil
	}

	for _, param := range strings.Split(fmtp, ";") {
		param = strings.TrimSpace(param)
		if param == "" {
			continue
		}
		kv := strings.SplitN(param, "=", 2)
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		if len(kv) < 2 {
			continue
		}
		val := strings.ToLower(strings.TrimSpace(kv[1]))

		switch key {
		case "annexa":
			switch val {
			case "yes", "1", "true":
				annexa = true
			case "no", "0", "false":
				annexa = false
			default:
				return false, false, fmt.Errorf("%w: annexa=%q (want yes|no|1|0)", ErrInvalidValue, val)
			}
		case "annexb":
			switch val {
			case "yes", "1", "true":
				annexb = true
			case "no", "0", "false":
				annexb = false
			default:
				return false, false, fmt.Errorf("%w: annexb=%q (want yes|no|1|0)", ErrInvalidValue, val)
			}
		default:
			// Unknown parameters: forward-compatible ignore
		}
	}
	return annexa, annexb, nil
}

// ParseFMTPLine parses a full a=fmtp SDP line and returns the annexb flag.
// The payloadType argument is used only to strip the "<pt> " prefix;
// pass 0 to skip the prefix check.
//
// Examples:
//
//	ParseFMTPLine("a=fmtp:18 annexb=yes", 18) → true, nil
//	ParseFMTPLine("a=fmtp:18 annexb=no",  18) → false, nil
//	ParseFMTPLine("annexb=yes",            0)  → true, nil
func ParseFMTPLine(line string, payloadType int) (annexb bool, err error) {
	// Strip optional "a=fmtp:" prefix
	line = strings.TrimSpace(line)
	prefix := "a=fmtp:"
	if strings.HasPrefix(strings.ToLower(line), prefix) {
		line = line[len(prefix):]
	}

	// Strip payload-type prefix if present
	if idx := strings.Index(line, " "); idx != -1 {
		if pt, e := strconv.Atoi(line[:idx]); e == nil {
			if payloadType > 0 && pt != payloadType {
				return false, fmt.Errorf("sdp: payload type mismatch (line has %d, expected %d)", pt, payloadType)
			}
			line = line[idx+1:]
		}
	} else if pt, e := strconv.Atoi(line); e == nil {
		if payloadType > 0 && pt != payloadType {
			return false, fmt.Errorf("sdp: payload type mismatch (line has %d, expected %d)", pt, payloadType)
		}
		line = ""
	}

	return ParseFMTP(line)
}

// ConfigFromFMTP derives a g729.Config from an fmtp attribute value string,
// configuring Variant (G729A vs G729 Full via annexa) and EnableVAD (via annexb).
//
//	cfg, err := sdp.ConfigFromFMTP("annexa=no; annexb=no")
func ConfigFromFMTP(fmtp string) (g729.Config, error) {
	annexa, annexb, err := ParseFMTPParams(fmtp)
	if err != nil {
		return g729.Config{}, err
	}
	variant := g729.VariantG729A
	if !annexa {
		variant = g729.VariantG729
	}
	return g729.Config{
		Variant:   variant,
		EnableVAD: annexb,
	}, nil
}

// NegotiateAnnexA negotiates the G.729 vs G.729A variant between offer and answer
// per RFC 3551 §4.5.6. Both sides must agree to use Annex A; if either side specifies
// annexa=no, full-complexity G.729 is used (returns false).
func NegotiateAnnexA(offerFMTP, answerFMTP string) (bool, error) {
	offerA, _, err := ParseFMTPParams(offerFMTP)
	if err != nil {
		return false, fmt.Errorf("offer: %w", err)
	}
	answerA, _, err := ParseFMTPParams(answerFMTP)
	if err != nil {
		return false, fmt.Errorf("answer: %w", err)
	}
	return offerA && answerA, nil
}

// NegotiateAnnexB implements the SDP offer/answer annexb negotiation rule
// per RFC 3551 §4.5.6:
//
//   - If both offer and answer assert annexb=yes → agreed: yes
//   - If either side asserts annexb=no → agreed: no
//   - Absent fmtp is treated as the default (yes)
//
// Returns the agreed-upon annexb value to use for both encoder and decoder.
func NegotiateAnnexB(offerFMTP, answerFMTP string) (bool, error) {
	offer, err := ParseFMTP(offerFMTP)
	if err != nil {
		return false, fmt.Errorf("offer: %w", err)
	}
	answer, err := ParseFMTP(answerFMTP)
	if err != nil {
		return false, fmt.Errorf("answer: %w", err)
	}
	// Both sides must agree; any "no" wins
	return offer && answer, nil
}

// ConfigFromNegotiation builds a Config from a completed SDP offer/answer
// exchange, negotiating both annexa (algorithm variant) and annexb (VAD).
func ConfigFromNegotiation(offerFMTP, answerFMTP string) (g729.Config, error) {
	annexa, err := NegotiateAnnexA(offerFMTP, answerFMTP)
	if err != nil {
		return g729.Config{}, err
	}
	annexb, err := NegotiateAnnexB(offerFMTP, answerFMTP)
	if err != nil {
		return g729.Config{}, err
	}
	variant := g729.VariantG729A
	if !annexa {
		variant = g729.VariantG729
	}
	return g729.Config{
		Variant:   variant,
		EnableVAD: annexb,
	}, nil
}
