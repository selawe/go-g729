package jitter_test

import (
	"sync"
	"testing"
	"time"

	"github.com/selawe/go-g729/jitter"
)

func TestJitterBufferInOrder(t *testing.T) {
	jb := jitter.New(jitter.Config{
		TargetDelay: 40 * time.Millisecond, // 4 frames prebuffering
	})

	// Push 10 packets (1 frame per packet)
	for i := range 10 {
		payload := []byte{byte(i + 1), 2, 3, 4, 5, 6, 7, 8, 9, 10}
		err := jb.Push(uint16(100+i), uint32(i*80), false, payload)
		if err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Pop all frames
	var popped [][]byte
	for {
		frame, ok := jb.Pop()
		if !ok {
			break
		}
		popped = append(popped, frame)
	}

	if len(popped) != 10 {
		t.Fatalf("expected 10 popped frames, got %d", len(popped))
	}
	for i, f := range popped {
		if f[0] != byte(i+1) {
			t.Errorf("frame %d tag = %d, want %d", i, f[0], i+1)
		}
	}
}

func TestJitterBufferReordering(t *testing.T) {
	jb := jitter.New(jitter.Config{
		TargetDelay: 40 * time.Millisecond,
	})

	// Push packets out of order: 102, 100, 101, 104, 103
	seqs := []uint16{102, 100, 101, 104, 103}
	for _, s := range seqs {
		payload := []byte{byte(s), 0, 0, 0, 0, 0, 0, 0, 0, 0}
		if err := jb.Push(s, uint32(s)*80, false, payload); err != nil {
			t.Fatalf("Push %d: %v", s, err)
		}
	}

	// Should play out in strict sequence: 100, 101, 102, 103, 104
	for expectedSeq := uint16(100); expectedSeq <= 104; expectedSeq++ {
		frame, ok := jb.Pop()
		if !ok {
			t.Fatalf("expected frame for seq %d, got ok=false", expectedSeq)
		}
		if frame == nil || frame[0] != byte(expectedSeq) {
			t.Fatalf("expected frame seq %d, got %v", expectedSeq, frame)
		}
	}
}

func TestJitterBufferLossConcealment(t *testing.T) {
	jb := jitter.New(jitter.Config{
		TargetDelay: 30 * time.Millisecond, // 3 frames
	})

	// Push packets with a gap: seq 10, 11, [12 missing], 13, 14
	jb.Push(10, 800, false, make([]byte, 10))
	jb.Push(11, 880, false, make([]byte, 10))
	jb.Push(13, 1040, false, make([]byte, 10))
	jb.Push(14, 1120, false, make([]byte, 10))

	// Frame 10: good
	f10, ok := jb.Pop()
	if !ok || f10 == nil {
		t.Fatalf("frame 10 missing")
	}

	// Frame 11: good
	f11, ok := jb.Pop()
	if !ok || f11 == nil {
		t.Fatalf("frame 11 missing")
	}

	// Frame 12: lost! Must return nil, ok=true (trigger PLC)
	f12, ok := jb.Pop()
	if !ok {
		t.Fatalf("expected ok=true for PLC slot")
	}
	if f12 != nil {
		t.Fatalf("expected nil frame for lost seq 12, got %v", f12)
	}

	// Frame 13: good
	f13, ok := jb.Pop()
	if !ok || f13 == nil {
		t.Fatalf("frame 13 missing")
	}

	st := jb.Stats()
	if st.EmittedPLC != 1 {
		t.Errorf("expected 1 EmittedPLC, got %d", st.EmittedPLC)
	}
}

func TestJitterBufferWrapAround(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	// Push near sequence wrap: 65534, 65535, 0, 1
	seqs := []uint16{65534, 65535, 0, 1}
	for _, s := range seqs {
		payload := []byte{byte(s & 0xFF), 0, 0, 0, 0, 0, 0, 0, 0, 0}
		if err := jb.Push(s, 0, false, payload); err != nil {
			t.Fatalf("Push %d: %v", s, err)
		}
	}

	for _, s := range seqs {
		frame, ok := jb.Pop()
		if !ok || frame == nil || frame[0] != byte(s&0xFF) {
			t.Fatalf("failed wrap around at seq %d (got %v, ok=%v)", s, frame, ok)
		}
	}
}

func TestJitterBufferMultiFramePacket(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	// 2 frames in 1 packet (20 ms payload)
	payload := make([]byte, 20)
	payload[0] = 0xAA
	payload[10] = 0xBB

	if err := jb.Push(50, 4000, false, payload); err != nil {
		t.Fatalf("Push 20ms: %v", err)
	}

	f1, ok1 := jb.Pop()
	if !ok1 || f1 == nil || f1[0] != 0xAA {
		t.Fatalf("frame 1 mismatch: %v", f1)
	}

	f2, ok2 := jb.Pop()
	if !ok2 || f2 == nil || f2[0] != 0xBB {
		t.Fatalf("frame 2 mismatch: %v", f2)
	}
}

func TestJitterBufferConsecutiveMultiFramePackets(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	// Push 5 consecutive 20-ms packets (2 frames each = 10 frames total)
	// per RFC 3550 §5.1, sequence numbers increment by 1 per packet (100, 101, 102, 103, 104)
	for p := range 5 {
		payload := make([]byte, 20)
		payload[0] = byte(p*2 + 1)
		payload[10] = byte(p*2 + 2)
		seq := uint16(100 + p)
		ts := uint32(p * 160)
		if err := jb.Push(seq, ts, false, payload); err != nil {
			t.Fatalf("Push %d: %v", p, err)
		}
	}

	for f := range 10 {
		frame, ok := jb.Pop()
		if !ok || frame == nil {
			t.Fatalf("frame %d: ok=%v frame=%v", f, ok, frame)
		}
		expectedTag := byte(f + 1)
		if frame[0] != expectedTag {
			t.Errorf("frame %d tag = %d, want %d", f, frame[0], expectedTag)
		}
	}

	stats := jb.Stats()
	if stats.DupPackets != 0 {
		t.Errorf("expected 0 DupPackets, got %d", stats.DupPackets)
	}
	if stats.DroppedByWrap != 0 {
		t.Errorf("expected 0 DroppedByWrap, got %d", stats.DroppedByWrap)
	}
}

func TestJitterBufferConcurrency(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	var wg sync.WaitGroup
	const totalPackets = 100

	producerDone := make(chan struct{})

	// Producer goroutine
	wg.Go(func() {
		defer close(producerDone)
		for i := range totalPackets {
			p := make([]byte, 10)
			p[0] = byte(i)
			_ = jb.Push(uint16(i), uint32(i*80), false, p)
			time.Sleep(100 * time.Microsecond)
		}
	})

	// Consumer goroutine: pops until either target count reached, or producer
	// finished and no more frames arrive within a drain grace period, or hard timeout.
	poppedCount := 0
	wg.Go(func() {
		hardDeadline := time.After(5 * time.Second)
		var producerFinished bool
		var drainDeadline <-chan time.Time
		for poppedCount < totalPackets {
			select {
			case <-hardDeadline:
				return
			case <-producerDone:
				if !producerFinished {
					producerFinished = true
					// Flush prebuffering so any remaining slots drain, then give
					// a short grace period for the consumer to drain them.
					jb.Flush()
					drainDeadline = time.After(200 * time.Millisecond)
				}
			case <-drainDeadline:
				return
			default:
			}
			_, ok := jb.Pop()
			if ok {
				poppedCount++
			} else {
				time.Sleep(200 * time.Microsecond)
			}
		}
	})

	wg.Wait()
	// Under contention some packets may pop as PLC and some real Push events may be
	// late-dropped; require substantial forward progress rather than an exact count.
	if poppedCount < totalPackets/2 {
		t.Errorf("expected at least %d popped events, got %d", totalPackets/2, poppedCount)
	}
	stats := jb.Stats()
	if stats.PushedPackets != totalPackets {
		t.Errorf("expected %d PushedPackets, got %d", totalPackets, stats.PushedPackets)
	}
}

func TestLargeMultiFramePacket(t *testing.T) {
	// 8-frame (80 ms) bundled packet — must not be rejected as too-large.
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	payload := make([]byte, 80)
	for i := range 8 {
		payload[i*10] = byte(0xA0 + i)
	}
	if err := jb.Push(50, 4000, false, payload); err != nil {
		t.Fatalf("Push 80-byte payload: %v", err)
	}

	jb.Flush()
	for i := range 8 {
		f, ok := jb.Pop()
		if !ok || f == nil {
			t.Fatalf("frame %d missing (ok=%v)", i, ok)
		}
		if f[0] != byte(0xA0+i) {
			t.Errorf("frame %d tag = %#x, want %#x", i, f[0], 0xA0+i)
		}
	}
}

func TestMaxDelayEnforcement(t *testing.T) {
	// MaxDelay = 30ms → 3 slots ahead of playout is the limit.
	jb := jitter.New(jitter.Config{
		TargetDelay: 10 * time.Millisecond,
		MaxDelay:    30 * time.Millisecond,
	})

	// Push seq=100 → establishes playoutSeq=100 and initializes.
	if err := jb.Push(100, 0, false, make([]byte, 10)); err != nil {
		t.Fatalf("Push 100: %v", err)
	}

	// Drain the prebuffer so we exit buffering mode.
	jb.Flush()
	_, _ = jb.Pop() // pop seq=100, playoutSeq=101

	// seq=103 → diff=2 (< 3), accepted.
	if err := jb.Push(103, 240, false, make([]byte, 10)); err != nil {
		t.Fatalf("Push 103: %v", err)
	}
	// seq=110 → diff=9 (>= 3), rejected as too-far-in-future.
	if err := jb.Push(110, 800, false, make([]byte, 10)); err != nil {
		t.Fatalf("Push 110 returned err: %v", err)
	}

	stats := jb.Stats()
	if stats.LatePackets != 1 {
		t.Errorf("expected 1 LatePackets (from seq=110 rejection), got %d", stats.LatePackets)
	}
}

func TestPopIntoBoundsCheck(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 10 * time.Millisecond})
	_ = jb.Push(0, 0, false, make([]byte, 10))

	// dst too small: must return ok=false without touching playout state.
	small := make([]byte, 5)
	n, isLoss, ok := jb.PopInto(small)
	if ok || isLoss || n != 0 {
		t.Fatalf("expected (0,false,false) for small dst, got (%d,%v,%v)", n, isLoss, ok)
	}

	// State should be intact — a subsequent PopInto with adequate dst must succeed.
	dst := make([]byte, 10)
	n, _, ok = jb.PopInto(dst)
	if !ok {
		t.Fatalf("expected playout to advance with adequate dst, got ok=false")
	}
	if n != 10 {
		t.Errorf("expected 10 bytes copied, got %d", n)
	}
}

func TestJitterBufferMarkerBitResync(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	// First talkspurt: seq 100..102
	for i := range 3 {
		payload := []byte{byte(100 + i), 0, 0, 0, 0, 0, 0, 0, 0, 0}
		if err := jb.Push(uint16(100+i), uint32(i*80), false, payload); err != nil {
			t.Fatalf("first talkspurt push %d: %v", i, err)
		}
	}
	jb.Flush()
	for range 3 {
		_, _ = jb.Pop()
	}

	// Silence gap; second talkspurt at seq 200 with a large jump.
	// Without marker bit, playoutSeq=103 and seq=200 triggers MaxDelay rejection
	// or bursts of PLC. With marker bit the buffer resyncs to seq 200 cleanly.
	payload200 := []byte{200, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := jb.Push(200, 16000, true, payload200); err != nil {
		t.Fatalf("second talkspurt (marker=true) push: %v", err)
	}
	payload201 := []byte{201, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if err := jb.Push(201, 16080, false, payload201); err != nil {
		t.Fatalf("second talkspurt push 201: %v", err)
	}

	jb.Flush()

	f1, ok1 := jb.Pop()
	if !ok1 || f1 == nil || f1[0] != 200 {
		t.Fatalf("expected frame tag=200 after marker resync, got %v (ok=%v)", f1, ok1)
	}
	f2, ok2 := jb.Pop()
	if !ok2 || f2 == nil || f2[0] != 201 {
		t.Fatalf("expected frame tag=201, got %v (ok=%v)", f2, ok2)
	}

	if stats := jb.Stats(); stats.EmittedPLC > 0 {
		t.Errorf("marker resync should emit 0 PLC frames, got %d", stats.EmittedPLC)
	}
}

func TestDroppedByWrap(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 50 * time.Millisecond})
	payload := make([]byte, 10)

	// Push packet 0
	if err := jb.Push(0, 0, false, payload); err != nil {
		t.Fatalf("push 0: %v", err)
	}

	// Push packet 128 during buffering (which maps to slot 0: 128 & 127 == 0)
	if err := jb.Push(128, 128*80, false, payload); err != nil {
		t.Fatalf("push 128: %v", err)
	}

	st := jb.Stats()
	if st.DroppedByWrap != 1 {
		t.Errorf("expected DroppedByWrap=1, got %d", st.DroppedByWrap)
	}
}

func BenchmarkJitterBufferPushPop(b *testing.B) {
	jb := jitter.New(jitter.Config{TargetDelay: 10 * time.Millisecond})
	payload := make([]byte, 10)
	var n uint32

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		seq := uint16(n & 0xFFFF)
		_ = jb.Push(seq, n*80, false, payload)
		_, _ = jb.Pop()
		n++
	}
}

func BenchmarkJitterBufferPushPopInto(b *testing.B) {
	jb := jitter.New(jitter.Config{TargetDelay: 10 * time.Millisecond})
	payload := make([]byte, 10)
	var out [10]byte
	var n uint32

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		seq := uint16(n & 0xFFFF)
		_ = jb.Push(seq, n*80, false, payload)
		_, _, _ = jb.PopInto(out[:])
		n++
	}
}

// TestCurrentBufferedIncremental verifies that Stats().CurrentBuffered tracks
// the push/pop cycle correctly without requiring an O(N) slot scan. This test
// would expose drift if the incremental counter were mis-maintained.
func TestCurrentBufferedIncremental(t *testing.T) {
	jb := jitter.New(jitter.Config{
		TargetDelay: 20 * time.Millisecond, // 2 frames prebuffering
		MaxDelay:    200 * time.Millisecond,
	})

	makePayload := func(seq uint16) []byte {
		p := make([]byte, 10)
		p[0] = byte(seq)
		return p
	}
	out := make([]byte, 10)

	// Push 4 frames — buffer should be 4.
	for i := range uint16(4) {
		if err := jb.Push(i, uint32(i)*80, false, makePayload(i)); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}
	if got := jb.Stats().CurrentBuffered; got != 4 {
		t.Errorf("after 4 pushes: CurrentBuffered=%d, want 4", got)
	}

	// Pop 2 frames (prebuffer met at 2 frames, playout starts).
	popped := 0
	for popped < 2 {
		_, _, ok := jb.PopInto(out)
		if ok {
			popped++
		} else {
			break
		}
	}
	if got := jb.Stats().CurrentBuffered; got != 2 {
		t.Errorf("after 2 pops: CurrentBuffered=%d, want 2", got)
	}

	// Push a duplicate of seq 2 — should not change count.
	_ = jb.Push(2, 160, false, makePayload(2))
	if got := jb.Stats().CurrentBuffered; got != 2 {
		t.Errorf("after dup push: CurrentBuffered=%d, want 2 (dup must not increment)", got)
	}

	// Pop remaining 2 frames.
	for range 2 {
		jb.PopInto(out)
	}
	if got := jb.Stats().CurrentBuffered; got != 0 {
		t.Errorf("after draining: CurrentBuffered=%d, want 0", got)
	}

	// Reset must also zero the counter.
	_ = jb.Push(100, 8000, false, makePayload(100))
	jb.Reset()
	if got := jb.Stats().CurrentBuffered; got != 0 {
		t.Errorf("after Reset: CurrentBuffered=%d, want 0", got)
	}
}

// TestBufferedCountNonNegativeUnderPLC verifies that CurrentBuffered never
// drops below zero even when Pop is called many more times than frames available.
func TestBufferedCountNonNegativeUnderPLC(t *testing.T) {
	jb := jitter.New(jitter.Config{
		TargetDelay: 10 * time.Millisecond, // 1-frame prebuffer
		MaxDelay:    50 * time.Millisecond,
	})

	// Push a single frame, then immediately exit prebuffering.
	payload := make([]byte, 10)
	if err := jb.Push(1, 80, false, payload); err != nil {
		t.Fatalf("Push: %v", err)
	}
	jb.Flush()

	dst := make([]byte, 10)
	// Pop far more frames than available — exercises PLC and underflow paths.
	for i := range 50 {
		jb.PopInto(dst)
		if got := jb.Stats().CurrentBuffered; got < 0 {
			t.Fatalf("iteration %d: CurrentBuffered went negative: %d", i, got)
		}
	}
}

