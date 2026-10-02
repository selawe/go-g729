# DTMF Handling with G.729 in Production VoIP (RFC 4733 / RFC 2833)

This document provides architectural guidance and implementation best practices for handling Dual-Tone Multi-Frequency (DTMF) signaling when deploying ITU-T G.729 in production Voice over IP (VoIP), Session Initiation Protocol (SIP), and WebRTC environments.

---

## 1. Why G.729 Cannot Reliably Carry In-Band DTMF

G.729 is a low-bitrate (8.0 kbit/s) **CELP (Code Excited Linear Prediction)** speech codec. It achieves high compression by modeling the physical human vocal tract:
* **LPC Filter (Formants):** Models resonances of the human throat and mouth.
* **Pitch Predictor (Harmonic Periodic Excitation):** Models vibration of human vocal cords.
* **Stochastic Codebook:** Excites the filter with a sparse sequence of pulses to mimic turbulent airflow.

### The Acoustic Conflict with DTMF:
DTMF signals consist of pairs of pure, unmodulated sinusoidal tones:
* **Low Group:** 697 Hz, 770 Hz, 852 Hz, 941 Hz
* **High Group:** 1209 Hz, 1336 Hz, 1477 Hz, 1633 Hz

When DTMF tones pass through an in-band G.729 encoder:
1. **Formant Mismatch:** Linear predictive analysis attempts to fit speech formants across pure sinusoids, creating spurious sideband harmonics.
2. **Algebraic Codebook Truncation:** The algebraic fixed codebook (ACELP) has only 4 pulses per 5 ms subframe (40 samples). It cannot reproduce the continuous, smooth sinusoidal curves required for pure dual tones.
3. **Twist and Amplitude Distortion:** Energy levels between the low-frequency and high-frequency tones shift unpredictably ("twist distortion").

**Production Consequence:**
If DTMF tones are encoded in-band via G.729, Interactive Voice Response (IVR) systems, automated attendants, and PBX voicemail gateways will either fail to detect keypresses or generate phantom/duplicate digits.

---

## 2. The Standard Solution: Out-of-Band DTMF (RFC 4733 / RFC 2833)

In carrier-grade VoIP, DTMF is **never encoded inside G.729 speech frames**. Instead, it is transmitted **out-of-band** as distinct RTP event packets conforming to **RFC 4733** (which obsoleted RFC 2833):
* Speech is encoded in G.729 packets (Payload Type 18).
* When a DTMF button is pressed, speech is silenced or muted, and RTP packets with MIME subtype `audio/telephone-event` (typically dynamic Payload Type 101) are sent.
* Each RFC 4733 packet explicitly carries the event code (0–9, \*, #, A–D), volume, and duration, guaranteeing 100% detection accuracy regardless of network packet loss or codec compression.

---

## 3. SDP Offer/Answer Negotiation

To enable out-of-band DTMF alongside G.729, the SDP media section must declare both formats in the `m=audio` line and provide corresponding `a=rtpmap` and `a=fmtp` attributes.

### Standard SDP Offer:
```sdp
m=audio 10000 RTP/AVP 18 101
a=rtpmap:18 G729/8000
a=fmtp:18 annexb=no
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-16
```

### Explanation:
* `18`: Static payload type for G.729 (8000 Hz, mono).
* `101`: Dynamic payload type assigned to `telephone-event/8000`.
* `a=fmtp:101 0-16`: Specifies that events 0 through 15 (DTMF digits `0-9`, `*`, `#`, `A-D`) and event 16 (`flash`) are supported.

### Using `go-g729/sdp`:
The `github.com/selawe/go-g729/sdp` package provides helper constants and formatting functions:
```go
package main

import (
	"fmt"
	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/sdp"
)

func main() {
	cfg := g729.Config{EnableVAD: false}

	// Build G.729 and telephone-event SDP lines
	fmt.Println("m=audio 10000 RTP/AVP 18 101")
	fmt.Println(sdp.RTPMapLine(sdp.DefaultPayloadType))          // a=rtpmap:18 G729/8000
	fmt.Println(sdp.FMTPLine(sdp.DefaultPayloadType, cfg))        // a=fmtp:18 annexb=no
	fmt.Println(sdp.TelephoneEventRTPMapLine(101))                // a=rtpmap:101 telephone-event/8000
	fmt.Println(sdp.TelephoneEventFMTPLine(101))                  // a=fmtp:101 0-16
}
```

---

## 4. RTP Ingress Architecture: Demultiplexing by Payload Type

In your media gateway, softswitch, or WebRTC server, inspect the RTP header `PayloadType` on every incoming packet:

```
                      Incoming RTP Packet
                               │
                ┌──────────────┴──────────────┐
                ▼                             ▼
       PayloadType == 18             PayloadType == 101
         (G.729 Audio)             (RFC 4733 DTMF Event)
                │                             │
                ▼                             ▼
      ┌──────────────────┐          ┌───────────────────┐
      │  jitter.Buffer   │          │  DTMF Event Loop  │
      │  g729.Decoder    │          │  (IVR / Action)   │
      └──────────────────┘          └───────────────────┘
                │
                ▼
         Decoded 16-bit
         Linear PCM Audio
```

### Go Implementation Example:
```go
package main

import (
	"log"

	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/jitter"
	"github.com/selawe/go-g729/rtp"
	"github.com/selawe/go-g729/sdp"
)

type MediaSession struct {
	decoder *g729.Decoder
	jb      *jitter.Buffer
}

func (s *MediaSession) HandleIncomingRTP(packet []byte) {
	if len(packet) < 12 {
		return // Truncated RTP header
	}

	payloadType := packet[1] & 0x7F

	switch payloadType {
	case sdp.DefaultPayloadType: // 18: G.729 speech
		// Forward to jitter buffer and decoder
		if err := s.jb.Push(packet); err != nil {
			log.Printf("jitter push error: %v", err)
		}

	case sdp.DefaultTelephoneEventPayloadType: // 101: RFC 4733 DTMF
		// DO NOT feed this payload into g729.Decoder!
		// Parse RFC 4733 telephone-event structure:
		payload := packet[12:]
		if len(payload) >= 4 {
			eventID := payload[0]
			endBit := (payload[1] & 0x80) != 0
			volume := payload[1] & 0x3F
			duration := uint16(payload[2])<<8 | uint16(payload[3])

			if endBit {
				log.Printf("DTMF Digit Detected: Event=%d, Volume=%d, Duration=%d ms",
					eventID, volume, duration/8)
				// Dispatch event to IVR / business logic
			}
		}

	default:
		log.Printf("Ignored unnegotiated RTP payload type: %d", payloadType)
	}
}
```

---

## 5. Summary Checklist for Production

| Requirement | Recommendation | Status |
| :--- | :--- | :---: |
| **Codec Selection** | Use G.729 / G.729A for human voice only | Required |
| **DTMF Transmission** | Use RFC 4733 (`telephone-event`) out-of-band | Mandatory |
| **SDP Negotiation** | Advertise `a=rtpmap:101 telephone-event/8000` | Mandatory |
| **RTP Ingress** | Demultiplex by Payload Type before decoding | Mandatory |
| **In-Band Fallback** | Disable in-band DTMF tone generation in SIP trunk | Recommended |
