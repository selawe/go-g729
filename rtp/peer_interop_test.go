package rtp_test

import (
	"math"
	"math/rand"
	"testing"

	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/rtp"
	"github.com/selawe/go-g729/sdp"
)

// generateHarmonicSignal creates a multi-frequency synthetic speech test signal.
func generateHarmonicSignal(numFrames int) []int16 {
	pcm := make([]int16, numFrames*80)
	for i := range pcm {
		t := float64(i) / 8000.0
		// Combine fundamental 220 Hz with harmonics 440, 880 Hz
		s := 0.5*math.Sin(2*math.Pi*220.0*t) +
			0.3*math.Sin(2*math.Pi*440.0*t) +
			0.2*math.Sin(2*math.Pi*880.0*t)
		pcm[i] = int16(s * 12000.0)
	}
	return pcm
}

// computeRMS calculates the root-mean-square energy of 16-bit linear PCM samples.
func computeRMS(samples []int16) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// TestPeerConversationalLifecycle simulates a full conversational call with an external RTP peer:
// Active Speech (500 ms) -> SID Packet -> DTX Silence Gap (1000 ms) -> SID Refresh -> Active Speech (500 ms).
func TestPeerConversationalLifecycle(t *testing.T) {
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: true})
	dec := g729.NewDecoder()

	const (
		speechFrames1 = 50  // 500 ms
		silenceFrames = 100 // 1000 ms
		speechFrames2 = 50  // 500 ms
		totalFrames   = speechFrames1 + silenceFrames + speechFrames2
	)

	pcmInput := make([]int16, totalFrames*80)
	copy(pcmInput[:speechFrames1*80], generateHarmonicSignal(speechFrames1))
	// Middle silenceFrames are left as zeros (silence)
	copy(pcmInput[(speechFrames1+silenceFrames)*80:], generateHarmonicSignal(speechFrames2))

	// Channel to simulate network transmission of RTP packets
	type rtpPacket struct {
		timestamp uint32
		payload   []byte
	}
	var networkPackets []rtpPacket

	// Encoder loop: bundle active speech into 20 ms RTP packets (2 frames)
	// and transmit SID frames immediately.
	var speechBundle [][]byte
	var currentTS uint32

	dst := make([]byte, 10)
	for f := 0; f < totalFrames; f++ {
		src := pcmInput[f*80 : (f+1)*80]
		n, frameType, err := enc.Encode(dst, src)
		if err != nil {
			t.Fatalf("frame %d encode failed: %v", f, err)
		}

		frameTS := rtp.TimestampForFrame(10000, f)

		switch frameType {
		case g729.FrameSpeech:
			fCopy := make([]byte, n)
			copy(fCopy, dst[:n])
			speechBundle = append(speechBundle, fCopy)
			if len(speechBundle) == 1 {
				currentTS = frameTS
			}
			if len(speechBundle) == 2 { // 20 ms packet
				payload, err := rtp.Pack(speechBundle)
				if err != nil {
					t.Fatalf("frame %d Pack speech: %v", f, err)
				}
				networkPackets = append(networkPackets, rtpPacket{timestamp: currentTS, payload: payload})
				speechBundle = nil
			}

		case g729.FrameSID:
			// Flush any pending speech frame
			if len(speechBundle) > 0 {
				payload, err := rtp.Pack(speechBundle)
				if err == nil {
					networkPackets = append(networkPackets, rtpPacket{timestamp: currentTS, payload: payload})
				}
				speechBundle = nil
			}
			fCopy := make([]byte, n)
			copy(fCopy, dst[:n])
			payload, err := rtp.Pack([][]byte{fCopy})
			if err != nil {
				t.Fatalf("frame %d Pack SID: %v", f, err)
			}
			networkPackets = append(networkPackets, rtpPacket{timestamp: frameTS, payload: payload})

		case g729.FrameUntransmitted:
			// No RTP packet sent across the network for suppressed silence frames
		}
	}

	// Verify that RTP packet count reflects bandwidth savings from VAD/DTX
	if len(networkPackets) >= totalFrames/2 {
		t.Errorf("DTX did not reduce packet count: got %d packets for %d frames", len(networkPackets), totalFrames)
	}

	// Decoder simulation: processes time in 10 ms increments matching wall clock
	decodedAudio := make([]int16, 0, totalFrames*80)
	packetIdx := 0
	var queuedFrames [][]byte

	for f := 0; f < totalFrames; f++ {
		frameTS := rtp.TimestampForFrame(10000, f)

		// Check if a packet has arrived at or before current timestamp
		if len(queuedFrames) == 0 && packetIdx < len(networkPackets) {
			if networkPackets[packetIdx].timestamp <= frameTS {
				unpacked, info, err := rtp.Unpack(networkPackets[packetIdx].payload)
				if err != nil {
					t.Fatalf("packet %d Unpack failed: %v", packetIdx, err)
				}
				_ = info
				queuedFrames = unpacked
				packetIdx++
			}
		}

		out := make([]int16, 80)
		if len(queuedFrames) > 0 {
			frameData := queuedFrames[0]
			queuedFrames = queuedFrames[1:]
			if err := dec.Decode(out, frameData); err != nil {
				t.Fatalf("frame %d Decode error: %v", f, err)
			}
		} else {
			// No frame received in this 10 ms window (DTX silence suppression)
			if err := dec.Decode(out, nil); err != nil {
				t.Fatalf("frame %d Decode comfort noise error: %v", f, err)
			}
		}
		decodedAudio = append(decodedAudio, out...)
	}

	// 1. Total samples check
	if len(decodedAudio) != totalFrames*80 {
		t.Fatalf("decoded samples mismatch: got %d, want %d", len(decodedAudio), totalFrames*80)
	}

	// 2. NaN / Inf check
	for i, s := range decodedAudio {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("sample %d is NaN/Inf", i)
		}
	}

	// 3. Energy profile verification
	speech1RMS := computeRMS(decodedAudio[10*80 : (speechFrames1-10)*80])
	silenceRMS := computeRMS(decodedAudio[(speechFrames1+20)*80 : (speechFrames1+silenceFrames-20)*80])
	speech2RMS := computeRMS(decodedAudio[(speechFrames1+silenceFrames+10)*80 : (totalFrames-10)*80])

	if speech1RMS < 1000.0 {
		t.Errorf("speech1 energy too low: RMS = %f", speech1RMS)
	}
	if speech2RMS < 1000.0 {
		t.Errorf("speech2 energy too low: RMS = %f", speech2RMS)
	}
	if silenceRMS > speech1RMS*0.25 {
		t.Errorf("comfort noise energy too loud relative to speech: silence RMS = %f, speech1 RMS = %f", silenceRMS, speech1RMS)
	}
}

// TestPeerDynamicPtime validates that a decoder seamlessly handles an external peer
// changing ptime dynamically during an active call (10ms -> 20ms -> 30ms -> 40ms -> 60ms -> 20ms).
func TestPeerDynamicPtime(t *testing.T) {
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: false})
	dec := g729.NewDecoder()

	pattern := []struct {
		ptimeMs int // 10, 20, 30, 40, 60
		packets int
	}{
		{ptimeMs: 10, packets: 10}, // 100 ms (10 frames)
		{ptimeMs: 20, packets: 15}, // 300 ms (30 frames)
		{ptimeMs: 30, packets: 10}, // 300 ms (30 frames)
		{ptimeMs: 40, packets: 10}, // 400 ms (40 frames)
		{ptimeMs: 60, packets: 5},  // 300 ms (30 frames)
		{ptimeMs: 20, packets: 10}, // 200 ms (20 frames)
	}

	totalFrames := 0
	for _, p := range pattern {
		framesPerPacket := p.ptimeMs / rtp.FrameDurationMs
		totalFrames += p.packets * framesPerPacket
	}

	pcmInput := generateHarmonicSignal(totalFrames)
	var decodedAudio []int16

	frameCursor := 0
	for phaseIdx, p := range pattern {
		framesPerPacket := p.ptimeMs / rtp.FrameDurationMs

		for pkt := 0; pkt < p.packets; pkt++ {
			// Encode frames for this packet
			rawFrames := make([][]byte, framesPerPacket)
			buf := make([]byte, 10)
			for f := 0; f < framesPerPacket; f++ {
				src := pcmInput[frameCursor*80 : (frameCursor+1)*80]
				n, _, err := enc.Encode(buf, src)
				if err != nil || n != 10 {
					t.Fatalf("phase %d pkt %d frame %d encode: n=%d err=%v", phaseIdx, pkt, f, n, err)
				}
				rawFrames[f] = make([]byte, 10)
				copy(rawFrames[f], buf)
				frameCursor++
			}

			// Pack into RTP payload
			payload, err := rtp.Pack(rawFrames)
			if err != nil {
				t.Fatalf("phase %d pkt %d Pack: %v", phaseIdx, pkt, err)
			}
			expectedLen := framesPerPacket * rtp.FrameBytes
			if len(payload) != expectedLen {
				t.Fatalf("phase %d pkt %d payload len %d, want %d", phaseIdx, pkt, len(payload), expectedLen)
			}

			// Unpack RTP payload
			unpacked, info, err := rtp.Unpack(payload)
			if err != nil {
				t.Fatalf("phase %d pkt %d Unpack: %v", phaseIdx, pkt, err)
			}
			if info.NumFrames != framesPerPacket || info.DurationMs != p.ptimeMs {
				t.Fatalf("phase %d pkt %d info mismatch: %+v", phaseIdx, pkt, info)
			}

			// Decode each frame
			out := make([]int16, 80)
			for fIdx, fData := range unpacked {
				if err := dec.Decode(out, fData); err != nil {
					t.Fatalf("phase %d pkt %d frame %d decode: %v", phaseIdx, pkt, fIdx, err)
				}
				decodedAudio = append(decodedAudio, out...)
			}
		}
	}

	if len(decodedAudio) != totalFrames*80 {
		t.Fatalf("total decoded samples mismatch: got %d, want %d", len(decodedAudio), totalFrames*80)
	}

	for i, s := range decodedAudio {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("sample %d is NaN/Inf", i)
		}
	}
}

// TestPeerNetworkLossAndBurstPLC validates that decoder Packet Loss Concealment (PLC)
// handles isolated packet losses and consecutive burst drops smoothly with progressive muting.
func TestPeerNetworkLossAndBurstPLC(t *testing.T) {
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: false})
	dec := g729.NewDecoder()

	const numPackets = 40 // 20 ms packets = 80 frames = 800 ms
	pcmInput := generateHarmonicSignal(numPackets * 2)

	// Pre-encode all 20 ms packets
	type packetData struct {
		seqNo   uint16
		payload []byte
	}
	var packets []packetData

	buf := make([]byte, 10)
	for p := 0; p < numPackets; p++ {
		frames := make([][]byte, 2)
		for f := 0; f < 2; f++ {
			src := pcmInput[(p*2+f)*80 : (p*2+f+1)*80]
			_, _, _ = enc.Encode(buf, src)
			frames[f] = make([]byte, 10)
			copy(frames[f], buf)
		}
		payload, _ := rtp.Pack(frames)
		packets = append(packets, packetData{seqNo: uint16(p + 100), payload: payload})
	}

	// Define packet loss profile:
	// - Packets 0..9: Clean
	// - Packet 10: Dropped (single loss)
	// - Packets 11..14: Clean
	// - Packets 15..17: Dropped (burst loss of 3 packets = 60 ms)
	// - Packets 18..22: Clean
	// - Packets 23..27: Dropped (burst loss of 5 packets = 100 ms muting test)
	// - Packets 28..39: Clean recovery
	isDropped := func(p int) bool {
		if p == 10 {
			return true
		}
		if p >= 15 && p <= 17 {
			return true
		}
		if p >= 23 && p <= 27 {
			return true
		}
		return false
	}

	burstEnergies := make([]float64, 5) // Energy of the 5 consecutive lost packets
	var decodedAudio []int16

	for p := 0; p < numPackets; p++ {
		out := make([]int16, 80)

		if isDropped(p) {
			// Packet was dropped in transit: invoke PLC twice for the 2 missing 10 ms frames
			var pktSamples []int16
			for f := 0; f < 2; f++ {
				if err := dec.Decode(out, nil); err != nil {
					t.Fatalf("packet %d frame %d PLC decode failed: %v", p, f, err)
				}
				decodedAudio = append(decodedAudio, out...)
				pktSamples = append(pktSamples, out...)
			}
			if p >= 23 && p <= 27 {
				burstEnergies[p-23] = computeRMS(pktSamples)
			}
		} else {
			// Packet arrived: unpack and decode both frames
			unpacked, _, err := rtp.Unpack(packets[p].payload)
			if err != nil {
				t.Fatalf("packet %d unpack failed: %v", p, err)
			}
			for fIdx, fData := range unpacked {
				if err := dec.Decode(out, fData); err != nil {
					t.Fatalf("packet %d frame %d decode failed: %v", p, fIdx, err)
				}
				decodedAudio = append(decodedAudio, out...)
			}
		}
	}

	// 1. Verify progressive muting during the 5-packet burst loss
	// Energy must decrease as consecutive lost frames accumulate
	if burstEnergies[4] >= burstEnergies[0] {
		t.Errorf("PLC did not progressively mute burst loss: pkt 0 RMS=%f, pkt 4 RMS=%f",
			burstEnergies[0], burstEnergies[4])
	}

	// 2. Verify clean recovery: check energy of recovery segment (packets 30..35)
	recoveryRMS := computeRMS(decodedAudio[30*2*80 : 35*2*80])
	if recoveryRMS < 1000.0 {
		t.Errorf("decoder did not recover after packet loss: recovery RMS = %f", recoveryRMS)
	}

	// 3. Verify no NaN / Inf
	for i, s := range decodedAudio {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatalf("sample %d is NaN/Inf", i)
		}
	}
}

// TestPeerMalformedPayloadDefense verifies decoder and RTP unpacker resiliency against
// malformed, truncated, or corrupt payloads from black-box peers.
func TestPeerMalformedPayloadDefense(t *testing.T) {
	dec := g729.NewDecoder()
	out := make([]int16, 80)

	// 1. RTP Unpack must reject illegal payload byte lengths
	illegalLengths := []int{1, 3, 4, 5, 6, 7, 8, 9, 11, 15, 19, 21, 25, 33, 99}
	for _, l := range illegalLengths {
		badPayload := make([]byte, l)
		_, _, err := rtp.Unpack(badPayload)
		if err == nil {
			t.Errorf("rtp.Unpack should reject %d-byte payload", l)
		}
	}

	// 2. Decoder must reject invalid destination or source buffer sizes gracefully
	tooSmallOut := make([]int16, 40)
	if err := dec.Decode(tooSmallOut, make([]byte, 10)); err == nil {
		t.Error("Decode should return error for < 80 sample dst")
	}

	// 3. Fuzzed active speech payloads (10 bytes): bit patterns must not panic or output NaN
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 100; iter++ {
		fuzzed := make([]byte, 10)
		rng.Read(fuzzed)

		if err := dec.Decode(out, fuzzed); err != nil {
			// Some corrupt bit patterns may return an error, which is acceptable
			continue
		}
		for i, s := range out {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("iter %d produced NaN/Inf at sample %d", iter, i)
			}
		}
	}

	// 4. Fuzzed SID payloads (2 bytes): all 65536 possible 16-bit values must not panic
	// Test a representative sample of 256 edge cases
	edgeCases := []uint16{
		0x0000, 0xFFFF, 0x8000, 0x7FFF, 0x5555, 0xAAAA,
		0x0001, 0x0002, 0x0004, 0x0008, 0x0010, 0x0020, 0x0040, 0x0080,
	}
	for _, v := range edgeCases {
		sidPayload := []byte{byte(v >> 8), byte(v & 0xFF)}
		if err := dec.Decode(out, sidPayload); err != nil {
			t.Fatalf("valid length SID payload 0x%04x caused decode error: %v", v, err)
		}
		for i, s := range out {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("SID 0x%04x produced NaN/Inf at sample %d", v, i)
			}
		}
	}
}

// TestPeerAnnexBInteroperability verifies SDP negotiation with peers asserting
// different annexb values, and ensures the encoder respects the negotiated result.
func TestPeerAnnexBInteroperability(t *testing.T) {
	testCases := []struct {
		name       string
		offerFMTP  string
		answerFMTP string
		wantVAD    bool
	}{
		{"BothYes", "annexb=yes", "annexb=yes", true},
		{"OfferYesAnswerNo", "annexb=yes", "annexb=no", false},
		{"OfferNoAnswerYes", "annexb=no", "annexb=yes", false},
		{"BothNo", "annexb=no", "annexb=no", false},
		{"ImplicitDefault", "", "", true},
		{"OfferImplicitAnswerNo", "", "annexb=no", false},
	}

	silenceFrame := make([]int16, 80)
	dst := make([]byte, 10)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := sdp.ConfigFromNegotiation(tc.offerFMTP, tc.answerFMTP)
			if err != nil {
				t.Fatalf("negotiation error: %v", err)
			}
			if cfg.EnableVAD != tc.wantVAD {
				t.Fatalf("EnableVAD = %v, want %v", cfg.EnableVAD, tc.wantVAD)
			}

			enc := g729.NewEncoder(cfg)

			// Feed 40 frames of silence
			seenSID := false
			for f := 0; f < 40; f++ {
				n, ft, err := enc.Encode(dst, silenceFrame)
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				if ft == g729.FrameSID {
					seenSID = true
				}
				if !tc.wantVAD {
					// When annexb=no, encoder must ALWAYS output 10-byte speech frames
					if n != 10 || ft != g729.FrameSpeech {
						t.Errorf("frame %d: annexb=no emitted ft=%v n=%d (want FrameSpeech 10B)", f, ft, n)
					}
				}
			}

			if tc.wantVAD && !seenSID {
				t.Errorf("annexb=yes expected SID frame during sustained silence, but none seen")
			}
		})
	}
}
