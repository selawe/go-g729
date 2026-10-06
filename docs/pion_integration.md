# Pion WebRTC & Pion RTP Integration Guide

This guide demonstrates how to integrate `github.com/selawe/go-g729` with the [Pion](https://github.com/pion) WebRTC and RTP ecosystem (`github.com/pion/webrtc` and `github.com/pion/rtp`).

`go-g729` is designed with zero third-party dependencies, making it directly compatible with Pion's standard `rtp.Packet` buffers and sample-based tracks.

---

## 1. Registering G.729 in Pion `webrtc.MediaEngine`

When configuring a WebRTC PeerConnection or SIP media engine, register G.729 as an audio codec with static Payload Type 18:

```go
package main

import (
	"github.com/pion/webrtc/v4"
	"github.com/selawe/go-g729/sdp"
)

func configureMediaEngine(m *webrtc.MediaEngine) error {
	// Register G.729 / G.729A audio codec
	return m.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeG729,
			ClockRate:    sdp.ClockRate, // 8000 Hz
			Channels:     1,             // Mono
			SDPFmtpLine:  "annexb=no",   // Or "annexb=yes" for Annex B DTX
		},
		PayloadType: sdp.DefaultPayloadType, // 18
	}, webrtc.RTPCodecTypeAudio)
}
```

---

## 2. Ingress Pipeline: Receiving & Decoding Pion RTP Packets

In WebRTC, remote audio packets arrive as `*rtp.Packet`. The example below shows how to pass them through `jitter.Buffer`, `g729.DecoderPool`, and `rtp.RTCPTracker`:

```go
package main

import (
	"log"
	"time"

	pionrtp "github.com/pion/rtp"
	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/jitter"
	"github.com/selawe/go-g729/rtp"
)

type IngressSession struct {
	decPool *g729.DecoderPool
	jb      *jitter.Buffer
	tracker *rtp.RTCPTracker
}

func NewIngressSession(ssrc uint32) *IngressSession {
	return &IngressSession{
		decPool: g729.NewDecoderPool(),
		jb: jitter.New(jitter.Config{
			TargetDelay: 40 * time.Millisecond,
		}),
		tracker: rtp.NewRTCPTracker(ssrc),
	}
}

// HandleRTP processes an incoming Pion RTP packet.
func (s *IngressSession) HandleRTP(pkt *pionrtp.Packet) {
	now := time.Now()

	// 1. Update RTCP reception statistics and jitter
	s.tracker.RecordPacket(pkt.SequenceNumber, pkt.Timestamp, now)

	// 2. Push payload to jitter buffer (with packet reordering)
	if err := s.jb.Push(pkt.SequenceNumber, pkt.Timestamp, pkt.Marker, pkt.Payload); err != nil {
		log.Printf("jitter push error: %v", err)
	}
}

// PlayoutLoop runs at 10 ms intervals to drain frames and decode PCM.
func (s *IngressSession) PlayoutLoop(pcmOutput chan<- []int16, stop <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	dec := s.decPool.Get()
	defer s.decPool.Put(dec)

	var frame [10]byte
	pcm := make([]int16, 80) // 10 ms @ 8 kHz

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			n, isLoss, ok := s.jb.PopInto(frame[:])
			if !ok {
				continue // Jitter buffer still buffering
			}

			if isLoss {
				// Packet Loss Concealment (PLC): pass nil frame
				_ = dec.Decode(pcm, nil)
			} else {
				_ = dec.Decode(pcm, frame[:n])
			}

			// Send 80 samples of decoded linear PCM
			pcmCopy := make([]int16, 80)
			copy(pcmCopy, pcm)
			pcmOutput <- pcmCopy
		}
	}
}
```

---

## 3. Egress Pipeline: Encoding & Sending with Pion

To encode PCM microphone audio and send it via Pion `TrackLocalStaticSample`:

```go
package main

import (
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/rtp"
)

type EgressSession struct {
	encTrack *webrtc.TrackLocalStaticSample
	encPool  *g729.EncoderPool
}

func NewEgressSession(track *webrtc.TrackLocalStaticSample) *EgressSession {
	return &EgressSession{
		encTrack: track,
		encPool:  g729.NewEncoderPool(g729.ProfileFast()),
	}
}

// Send20msAudio encodes 160 linear PCM samples (20 ms) into a bundled G.729 RTP packet.
func (s *EgressSession) Send20msAudio(pcm160 []int16) error {
	enc := s.encPool.Get()
	defer s.encPool.Put(enc)

	// Encode 2 consecutive 10 ms frames
	var frame1 [10]byte
	var frame2 [10]byte

	_, _, err := enc.Encode(frame1[:], pcm160[:80])
	if err != nil {
		return err
	}
	_, _, err = enc.Encode(frame2[:], pcm160[80:160])
	if err != nil {
		return err
	}

	// Pack bundled frames into a 20-byte RTP payload (20 ms ptime)
	var payload [20]byte
	_, err = rtp.PackInto(payload[:], [][]byte{frame1[:], frame2[:]})
	if err != nil {
		return err
	}

	// Write sample to Pion WebRTC track
	return s.encTrack.WriteSample(media.Sample{
		Data:      payload[:],
		Duration:  20 * time.Millisecond,
		Timestamp: time.Now(),
	})
}
```

---

## 4. Periodic RTCP Quality Monitoring

Emit RFC 3550 reception reports and E-model MOS estimates periodically:

```go
func (s *IngressSession) LogQualityReport() {
	report := s.tracker.GenerateReport()
	mos := report.EstimatedMOS(40 * time.Millisecond) // e.g. 40 ms network RTT/2

	log.Printf("[RTCP Report] SSRC=%x Packets=%d Lost=%d (%.2f%%) Jitter=%v MOS=%.2f",
		report.SSRC,
		report.PacketsReceived,
		report.CumulativeLost,
		report.LossRate()*100,
		report.JitterDuration(),
		mos,
	)
}
```
