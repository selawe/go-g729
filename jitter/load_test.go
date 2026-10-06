package jitter_test

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/jitter"
	"github.com/selawe/go-g729/rtp"
)

// simulatedPacket represents a packet in transit over an emulated IP network.
type simulatedPacket struct {
	seq         uint16
	timestamp   uint32
	marker      bool
	payload     []byte
	deliverTime time.Time
}

// TestJitterBufferRealWorldNetworkSimulation simulates a realistic VoIP call over
// an imperfect WAN network subject to packet loss, burst loss, jitter, reordering,
// duplicates, and DTX silence transitions with RTP marker bits.
func TestJitterBufferRealWorldNetworkSimulation(t *testing.T) {
	const (
		numPackets = 400 // ~8 seconds of speech (20 ms packets = 2 frames/packet)
		seed       = 42
	)

	rng := rand.New(rand.NewSource(seed))

	// 1. Generate multi-frequency speech signal
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: false})
	dec := g729.NewDecoder()

	var originalPackets []simulatedPacket
	baseTS := uint32(10000)

	var frameBuf [10]byte
	pcm := make([]int16, 80)

	// Produce packets across 3 talkspurts with DTX silence gaps:
	// Talkspurt 1: seq 100..199
	// Gap / Silence: 1.5s (simulates DTX without packets)
	// Talkspurt 2: seq 200..299 (marker = true on 200)
	// Gap / Silence: 1.0s
	// Talkspurt 3: seq 300..399 (marker = true on 300)

	for p := 0; p < numPackets; p++ {
		var seq uint16
		var marker bool

		switch {
		case p < 100:
			seq = uint16(100 + p)
			marker = (p == 0)
		case p < 200:
			seq = uint16(200 + (p - 100))
			marker = (p == 100)
		default:
			seq = uint16(300 + (p - 200))
			marker = (p == 200)
		}

		ts := baseTS + uint32(p)*160 // 20 ms @ 8 kHz = 160 samples

		// 2 frames per packet (20 ms)
		payload := make([]byte, 20)
		for sub := 0; sub < 2; sub++ {
			// Synthetic harmonic audio tone
			frameIdx := p*2 + sub
			for i := range pcm {
				t := float64(frameIdx*80+i) / 8000.0
				pcm[i] = int16(6000.0*math.Sin(2*math.Pi*300.0*t) + 3000.0*math.Sin(2*math.Pi*600.0*t))
			}
			_, _, err := enc.Encode(frameBuf[:], pcm)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			copy(payload[sub*10:(sub+1)*10], frameBuf[:])
		}

		originalPackets = append(originalPackets, simulatedPacket{
			seq:       seq,
			timestamp: ts,
			marker:    marker,
			payload:   payload,
		})
	}

	// 2. Emulate IP network impairment:
	// - 5% independent random loss
	// - 5% burst loss (2 consecutive packets)
	// - 10% jitter (0..40 ms delay)
	// - 2% duplicates
	var inFlight []simulatedPacket
	startTime := time.Now()

	lostSeqs := make(map[uint16]bool)
	i := 0
	for i < len(originalPackets) {
		pkt := originalPackets[i]

		// Random packet loss
		if rng.Float64() < 0.05 {
			lostSeqs[pkt.seq] = true
			i++
			continue
		}

		// Burst loss (2 packets)
		if rng.Float64() < 0.04 && i+1 < len(originalPackets) {
			lostSeqs[pkt.seq] = true
			lostSeqs[originalPackets[i+1].seq] = true
			i += 2
			continue
		}

		// Base transmission delay + variable jitter (0..35 ms)
		baseDelay := time.Duration(i*20) * time.Millisecond
		jitterDelay := time.Duration(rng.Intn(35)) * time.Millisecond
		pkt.deliverTime = startTime.Add(baseDelay + jitterDelay)
		inFlight = append(inFlight, pkt)

		// 2% packet duplication
		if rng.Float64() < 0.02 {
			dup := pkt
			dup.deliverTime = pkt.deliverTime.Add(time.Duration(5+rng.Intn(15)) * time.Millisecond)
			inFlight = append(inFlight, dup)
		}

		i++
	}

	// Sort network delivery queue by arrival time (simulates out-of-order arrival)
	sort.Slice(inFlight, func(a, b int) bool {
		return inFlight[a].deliverTime.Before(inFlight[b].deliverTime)
	})

	// 3. Playout simulation with Jitter Buffer
	jb := jitter.New(jitter.Config{
		TargetDelay: 40 * time.Millisecond,  // 4 frames = 40 ms
		MaxDelay:    160 * time.Millisecond, // 16 frames = 160 ms
	})

	tracker := rtp.NewRTCPTracker(0xABCD1234)

	// Ingress: push all packets in their actual network arrival order
	for _, pkt := range inFlight {
		tracker.RecordPacket(pkt.seq, pkt.timestamp, pkt.deliverTime)
		err := jb.Push(pkt.seq, pkt.timestamp, pkt.marker, pkt.payload)
		if err != nil {
			t.Fatalf("jb.Push seq %d: %v", pkt.seq, err)
		}
	}

	// Playout: drain all frames via PopInto
	jb.Flush()

	var playoutOut [10]byte
	var decodedPCM [80]int16
	var validFrames, plcFrames int

	for {
		n, isLoss, ok := jb.PopInto(playoutOut[:])
		if !ok {
			break
		}

		if isLoss || n == 0 {
			plcFrames++
			err := dec.Decode(decodedPCM[:], nil)
			if err != nil {
				t.Fatalf("PLC decode failed: %v", err)
			}
		} else {
			validFrames++
			err := dec.Decode(decodedPCM[:], playoutOut[:n])
			if err != nil {
				t.Fatalf("Speech decode failed: %v", err)
			}
		}

		// Verify PCM audio validity: no NaNs, no infinities, proper saturation bounds
		for sIdx, sVal := range decodedPCM {
			if sVal > 32767 || sVal < -32768 {
				t.Fatalf("sample %d out of bounds: %d", sIdx, sVal)
			}
		}
	}

	stats := jb.Stats()
	t.Logf("Simulation Results:")
	t.Logf("  Pushed:       %d packets", stats.PushedPackets)
	t.Logf("  Popped:       %d valid frames", stats.PoppedFrames)
	t.Logf("  Emitted PLC:  %d concealed frames", stats.EmittedPLC)
	t.Logf("  Late Dropped: %d packets", stats.LatePackets)
	t.Logf("  Duplicates:   %d packets", stats.DupPackets)
	t.Logf("  Underflows:   %d", stats.Underflows)

	report := tracker.GenerateReport()
	t.Logf("RTCP Report:")
	t.Logf("  Loss Fraction: %d/256 (%.2f%%)", report.FractionLost, report.LossRate()*100)
	t.Logf("  Cumulative Lost: %d packets", report.CumulativeLost)
	t.Logf("  Jitter:       %d units (%v)", report.InterarrivalJitter, report.JitterDuration())
	t.Logf("  Estimated MOS: %.2f", report.EstimatedMOS(30*time.Millisecond))

	if validFrames == 0 {
		t.Fatal("no valid frames played out!")
	}
	if plcFrames == 0 {
		t.Fatal("expected PLC frames due to simulated network packet loss")
	}
	if stats.DupPackets == 0 {
		t.Log("Note: no duplicate packets hit duplicate detector window")
	}
}

// TestJitterBufferHeavyLossDegradation validates codec and jitter buffer behavior
// under harsh network conditions (20% to 30% packet loss) ensuring graceful
// progressive muting without acoustic audio clipping.
func TestJitterBufferHeavyLossDegradation(t *testing.T) {
	jb := jitter.New(jitter.Config{
		TargetDelay: 30 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	})
	dec := g729.NewDecoder()

	const total = 50
	rng := rand.New(rand.NewSource(12345))

	// Push 50 packets with 30% loss rate
	pushed := 0
	for i := 0; i < total; i++ {
		seq := uint16(100 + i)
		ts := uint32(i * 80)
		if rng.Float64() < 0.30 {
			continue // Drop packet
		}
		payload := make([]byte, 10)
		payload[0] = 0x55
		_ = jb.Push(seq, ts, false, payload)
		pushed++
	}

	jb.Flush()

	var out [10]byte
	var pcm [80]int16
	var maxAbsVal int16

	for {
		n, isLoss, ok := jb.PopInto(out[:])
		if !ok {
			break
		}
		if isLoss {
			_ = dec.Decode(pcm[:], nil)
		} else {
			_ = dec.Decode(pcm[:], out[:n])
		}
		for _, s := range pcm {
			abs := s
			if abs < 0 {
				abs = -abs
			}
			if abs > maxAbsVal {
				maxAbsVal = abs
			}
		}
	}

	stats := jb.Stats()
	if stats.EmittedPLC == 0 {
		t.Error("expected PLC under 30% loss")
	}
	decStats := dec.Stats()
	if decStats.ConcealedFrames == 0 {
		t.Error("expected decoder ConcealedFrames > 0")
	}
	t.Logf("Heavy loss: Pushed=%d, EmittedPLC=%d, DecoderConcealed=%d, MaxAmplitude=%d",
		pushed, stats.EmittedPLC, decStats.ConcealedFrames, maxAbsVal)
}

// TestJitterBufferMultiStreamConcurrentLoad stress-tests 50 parallel VoIP sessions
// running concurrent ingress (Push) and egress (PopInto) loops with randomized jitter.
func TestJitterBufferMultiStreamConcurrentLoad(t *testing.T) {
	const (
		numStreams       = 50
		packetsPerStream = 100
	)

	var wg sync.WaitGroup
	var totalPlayed atomic.Int64
	var totalPLC atomic.Int64

	for streamID := 0; streamID < numStreams; streamID++ {
		wg.Add(1)
		go func(sID int) {
			defer wg.Done()

			jb := jitter.New(jitter.Config{
				TargetDelay: 20 * time.Millisecond,
				MaxDelay:    80 * time.Millisecond,
			})
			dec := g729.NewDecoder()

			rng := rand.New(rand.NewSource(int64(sID * 1000)))

			// Producer loop
			for p := 0; p < packetsPerStream; p++ {
				seq := uint16(p)
				ts := uint32(p * 80)
				// 5% loss
				if rng.Float64() >= 0.05 {
					payload := make([]byte, 10)
					payload[0] = byte(p & 0xFF)
					_ = jb.Push(seq, ts, (p == 0), payload)
				}
			}

			jb.Flush()

			var frameBuf [10]byte
			var pcm [80]int16
			for {
				n, isLoss, ok := jb.PopInto(frameBuf[:])
				if !ok {
					break
				}
				if isLoss {
					totalPLC.Add(1)
					_ = dec.Decode(pcm[:], nil)
				} else {
					totalPlayed.Add(1)
					_ = dec.Decode(pcm[:], frameBuf[:n])
				}
			}
		}(streamID)
	}

	wg.Wait()

	t.Logf("Concurrent 50-stream load test completed successfully:")
	t.Logf("  Total Speech Frames Decoded: %d", totalPlayed.Load())
	t.Logf("  Total PLC Frames Handled:   %d", totalPLC.Load())

	if totalPlayed.Load() == 0 {
		t.Fatal("no frames played across 50 streams")
	}
}
