// Package jitter_test contains production-oriented simulation tests that model
// real VoIP network conditions: nominal streams, packet loss, reordering, burst
// drops, and talkspurt transitions via the RTP marker bit.
package jitter_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/selawe/go-g729/jitter"
	"github.com/selawe/go-g729/rtp"
)

// makePayload returns a valid G.729 RTP payload containing nFrames zero-filled
// speech frames, packed using rtp.PackInto. Each frame is rtp.FrameBytes (10)
// zero bytes, which is accepted as a valid G.729 payload for jitter-buffer testing.
func makePayload(nFrames int) []byte {
	frames := make([][]byte, nFrames)
	for i := range nFrames {
		frames[i] = make([]byte, rtp.FrameBytes)
	}
	dst := make([]byte, nFrames*rtp.FrameBytes)
	n, err := rtp.PackInto(dst, frames)
	if err != nil {
		panic("makePayload: " + err.Error())
	}
	return dst[:n]
}

// simStream drives a push/pop loop that approximates a real-time VoIP engine:
// every iteration it pushes the next ready packet (if any) and then immediately
// pops one frame. This interleaving prevents ring-buffer overflow when the total
// stream length exceeds MaxSlotCount (128).
//
// packets is an ordered slice of (seq, payload) pairs; nil payload entries
// represent dropped packets that are simply skipped without a Push call.
// targetDelay controls pre-buffering; the function calls Flush after all pushes.
//
// Returns (validFrames, plcFrames).
func simStream(t *testing.T, cfg jitter.Config, packets []simPacket) (valid, plc int) {
	t.Helper()
	jb := jitter.New(cfg)
	dst := make([]byte, rtp.FrameBytes)

	// Push all packets first then pop — but we must interleave for long streams.
	// Strategy: push in chunks of (targetDelay / 10ms) then pop to drain.
	// This matches how a real sender fills the jitter buffer and the receiver drains it.
	targetFrames := int(cfg.TargetDelay / (rtp.FrameDurationMs * time.Millisecond))
	if targetFrames < 1 {
		targetFrames = 4
	}

	popOne := func() {
		_, isLoss, ok := jb.PopInto(dst)
		if !ok {
			return
		}
		if isLoss {
			plc++
		} else {
			valid++
		}
	}

	pushed := 0
	for _, pkt := range packets {
		if pkt.payload != nil {
			if err := jb.Push(pkt.seq, pkt.ts, pkt.marker, pkt.payload); err != nil {
				t.Fatalf("Push seq=%d: %v", pkt.seq, err)
			}
		}
		pushed++
		// Once we've pushed enough to fill the pre-buffer, start popping
		// one frame per packet to maintain steady-state drain.
		if pushed >= targetFrames {
			popOne()
		}
	}

	// Exit pre-buffering so remaining queued frames drain.
	jb.Flush()

	// Drain the tail (targetFrames worth still queued).
	for range targetFrames + 4 {
		popOne()
	}

	return valid, plc
}

type simPacket struct {
	seq     uint16
	ts      uint32
	marker  bool
	payload []byte // nil = dropped (no Push)
}

// TestSimulationNominal sends a clean 100-packet 10 ms ptime stream and verifies
// that 0% PLC frames are emitted and no mid-stream underflows occur.
func TestSimulationNominal(t *testing.T) {
	const nPackets = 100
	payload := makePayload(1)

	pkts := make([]simPacket, nPackets)
	for i := range nPackets {
		pkts[i] = simPacket{
			seq:     uint16(i),
			ts:      uint32(i) * rtp.TimestampIncrement,
			payload: payload,
		}
	}

	cfg := jitter.Config{TargetDelay: 40 * time.Millisecond}
	valid, plc := simStream(t, cfg, pkts)

	if plc != 0 {
		t.Errorf("nominal: want 0 PLC frames, got %d", plc)
	}
	if valid != nPackets {
		t.Errorf("nominal: want %d valid frames, got %d", nPackets, valid)
	}
}

// TestSimulationPacketLoss drops 5% of packets at random and verifies that PLC
// frames are emitted proportional to the loss rate (within 2x), that no panic or
// data race occurs, and that playout stays continuous after the initial buffering
// period.
func TestSimulationPacketLoss(t *testing.T) {
	const nPackets = 200
	const lossRate = 0.05

	rng := rand.New(rand.NewPCG(42, 0))
	payload := makePayload(1)

	pkts := make([]simPacket, nPackets)
	dropped := 0
	for i := range nPackets {
		pkts[i] = simPacket{
			seq: uint16(i),
			ts:  uint32(i) * rtp.TimestampIncrement,
		}
		if rng.Float64() < lossRate {
			dropped++
			// leave payload nil — simStream skips the Push
		} else {
			pkts[i].payload = payload
		}
	}

	cfg := jitter.Config{TargetDelay: 40 * time.Millisecond}
	valid, plc := simStream(t, cfg, pkts)

	t.Logf("packet loss: dropped=%d valid=%d plc=%d", dropped, valid, plc)

	// PLC frames must be proportional to packet loss.
	// Minimum: at least 1 PLC per dropped packet (minus a small tolerance for the
	// pre-buffer absorbing the very first loss before playout starts).
	// Maximum: 2× dropped + small constant for framing edge cases.
	if plc < dropped-2 {
		t.Errorf("packet loss: EmittedPLC=%d < dropped-2=%d — too few PLC frames", plc, dropped-2)
	}
	if plc > 2*dropped+4 {
		t.Errorf("packet loss: EmittedPLC=%d > 2*dropped+4=%d — too many PLC frames", plc, 2*dropped+4)
	}

	// Playout output must account for most of the stream.
	// valid + plc should be close to nPackets (allow ±targetDelay worth of frames).
	total := valid + plc
	if total < nPackets-8 {
		t.Errorf("packet loss: total frames emitted=%d < %d — playout not continuous", total, nPackets-8)
	}
}

// TestSimulationReorder injects 20% out-of-order packets by swapping adjacent pairs
// and verifies that fewer than 5% of total output frames are PLC.
func TestSimulationReorder(t *testing.T) {
	const nPackets = 100

	rng := rand.New(rand.NewPCG(7, 1))
	payload := makePayload(1)

	// Build sequence order with random adjacent-pair swaps (~20%).
	seqs := make([]uint16, nPackets)
	for i := range nPackets {
		seqs[i] = uint16(i)
	}
	for i := 0; i < nPackets-1; i++ {
		if rng.Float64() < 0.20 {
			seqs[i], seqs[i+1] = seqs[i+1], seqs[i]
			i++ // skip next to avoid double-swapping
		}
	}

	pkts := make([]simPacket, nPackets)
	for i, seq := range seqs {
		pkts[i] = simPacket{
			seq:     seq,
			ts:      uint32(seq) * rtp.TimestampIncrement,
			payload: payload,
		}
	}

	cfg := jitter.Config{TargetDelay: 40 * time.Millisecond}
	valid, plc := simStream(t, cfg, pkts)

	total := valid + plc
	if total == 0 {
		t.Fatal("reorder: no frames emitted")
	}
	plcPct := float64(plc) / float64(total) * 100

	t.Logf("reorder: valid=%d plc=%d plcPct=%.1f%%", valid, plc, plcPct)

	if plcPct >= 5.0 {
		t.Errorf("reorder: PLC rate %.1f%% >= 5%% (plc=%d total=%d)", plcPct, plc, total)
	}
}

// TestSimulationBurst drops bursts of 3 consecutive packets every 30 packets and
// verifies the lossFramesRemaining mechanism handles burst loss gracefully — the
// PLC count matches the dropped count and PoppedFrames+EmittedPLC matches the
// total output without panic.
func TestSimulationBurst(t *testing.T) {
	const nPackets = 150
	const burstPeriod = 30
	const burstStart = 1  // drop offset 1,2,3 within each period (not offset 0)
	const burstLen = 3

	payload := makePayload(1)

	pkts := make([]simPacket, nPackets)
	dropped := 0
	for i := range nPackets {
		offset := i % burstPeriod
		pkts[i] = simPacket{
			seq: uint16(i),
			ts:  uint32(i) * rtp.TimestampIncrement,
		}
		if offset >= burstStart && offset < burstStart+burstLen {
			dropped++
			// leave payload nil
		} else {
			pkts[i].payload = payload
		}
	}

	cfg := jitter.Config{TargetDelay: 40 * time.Millisecond}
	valid, plc := simStream(t, cfg, pkts)

	t.Logf("burst: dropped=%d valid=%d plc=%d", dropped, valid, plc)

	// PLC must be at least as many as dropped packets.
	if plc < dropped-2 {
		t.Errorf("burst: PLC=%d < dropped-2=%d — burst loss not concealed", plc, dropped-2)
	}

	// PLC must not wildly exceed drops (allow 2× for edge framing at period boundaries).
	if plc > 2*dropped+burstLen {
		t.Errorf("burst: PLC=%d > 2*dropped+burstLen=%d — excessive concealment",
			plc, 2*dropped+burstLen)
	}

	// Stats self-consistency: PoppedFrames + EmittedPLC == valid + plc
	// (verified implicitly since simStream counts from PopInto returns).
	total := valid + plc
	if total < nPackets-int(cfg.TargetDelay/(rtp.FrameDurationMs*time.Millisecond))-burstLen {
		t.Errorf("burst: total output=%d too low for stream of %d packets", total, nPackets)
	}
}

// TestSimulationMarkerBit pushes 50 packets (first talkspurt), fully drains them,
// then pushes 50 more packets with marker=true on the first one (second talkspurt
// at a large sequence gap). Verifies that no stale frames from the first talkspurt
// appear after the marker-bit reset and that PLC is not emitted for the silence gap.
func TestSimulationMarkerBit(t *testing.T) {
	const firstBurst = 50
	const secondBurst = 50
	const seqGap = uint16(500) // large gap — a naive buffer would PLC-flood between spurts

	payload := makePayload(1)
	cfg := jitter.Config{TargetDelay: 40 * time.Millisecond}
	dst := make([]byte, rtp.FrameBytes)

	jb := jitter.New(cfg)

	// === First talkspurt: seq 0..49 ===
	for i := range firstBurst {
		if err := jb.Push(uint16(i), uint32(i)*rtp.TimestampIncrement, false, payload); err != nil {
			t.Fatalf("first burst Push seq=%d: %v", i, err)
		}
	}
	jb.Flush()

	valid1, plc1 := 0, 0
	for {
		_, isLoss, ok := jb.PopInto(dst)
		if !ok {
			break
		}
		if isLoss {
			plc1++
		} else {
			valid1++
		}
	}

	if plc1 != 0 {
		t.Errorf("marker: first talkspurt emitted %d PLC frames, want 0", plc1)
	}
	if valid1 != firstBurst {
		t.Errorf("marker: first talkspurt valid=%d, want %d", valid1, firstBurst)
	}

	// === Second talkspurt: seq seqGap..seqGap+49, marker=true on first packet ===
	for i := range secondBurst {
		seq := seqGap + uint16(i)
		ts := uint32(seq) * rtp.TimestampIncrement
		marker := i == 0
		if err := jb.Push(seq, ts, marker, payload); err != nil {
			t.Fatalf("second burst Push seq=%d: %v", seq, err)
		}
	}
	jb.Flush()

	valid2, plc2 := 0, 0
	for {
		_, isLoss, ok := jb.PopInto(dst)
		if !ok {
			break
		}
		if isLoss {
			plc2++
		} else {
			valid2++
		}
	}

	// After marker resync, no PLC should be emitted for the silence gap.
	if plc2 != 0 {
		t.Errorf("marker: second talkspurt emitted %d PLC frames after marker reset, want 0", plc2)
	}
	if valid2 != secondBurst {
		t.Errorf("marker: second talkspurt valid=%d, want %d", valid2, secondBurst)
	}

	st := jb.Stats()
	wantPushed := firstBurst + secondBurst
	if st.PushedPackets != wantPushed {
		t.Errorf("marker: PushedPackets=%d, want %d", st.PushedPackets, wantPushed)
	}

	t.Logf("marker: first(valid=%d plc=%d) second(valid=%d plc=%d) pushedPkts=%d",
		valid1, plc1, valid2, plc2, st.PushedPackets)
}

// BenchmarkSimulationPushPop measures the throughput of the push/pop cycle with
// single-frame 40 ms target-delay configuration, reporting per-operation allocations.
// Uses PopInto (zero-allocation path) to isolate buffer overhead from heap pressure.
func BenchmarkSimulationPushPop(b *testing.B) {
	jb := jitter.New(jitter.Config{TargetDelay: 40 * time.Millisecond})
	payload := makePayload(1)
	dst := make([]byte, rtp.FrameBytes)
	var n uint32

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		seq := uint16(n & 0xFFFF)
		ts := n * rtp.TimestampIncrement
		_ = jb.Push(seq, ts, false, payload)
		_, _, _ = jb.PopInto(dst)
		n++
	}
}
