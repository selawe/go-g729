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
	for i := 0; i < 10; i++ {
		payload := []byte{byte(i + 1), 2, 3, 4, 5, 6, 7, 8, 9, 10}
		err := jb.Push(uint16(100+i), uint32(i*80), payload)
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
		if err := jb.Push(s, uint32(s)*80, payload); err != nil {
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
	jb.Push(10, 800, make([]byte, 10))
	jb.Push(11, 880, make([]byte, 10))
	jb.Push(13, 1040, make([]byte, 10))
	jb.Push(14, 1120, make([]byte, 10))

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
		if err := jb.Push(s, 0, payload); err != nil {
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

	if err := jb.Push(50, 4000, payload); err != nil {
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

func TestJitterBufferConcurrency(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 20 * time.Millisecond})

	var wg sync.WaitGroup
	const totalPackets = 100

	producerDone := make(chan struct{})

	// Producer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(producerDone)
		for i := 0; i < totalPackets; i++ {
			p := make([]byte, 10)
			p[0] = byte(i)
			_ = jb.Push(uint16(i), uint32(i*80), p)
			time.Sleep(100 * time.Microsecond)
		}
	}()

	// Consumer goroutine: pops until either target count reached, or producer
	// finished and no more frames arrive within a drain grace period, or hard timeout.
	wg.Add(1)
	poppedCount := 0
	go func() {
		defer wg.Done()
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
	}()

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

func TestMaxDelayEnforcement(t *testing.T) {
	// MaxDelay = 30ms → 3 slots ahead of playout is the limit.
	jb := jitter.New(jitter.Config{
		TargetDelay: 10 * time.Millisecond,
		MaxDelay:    30 * time.Millisecond,
	})

	// Push seq=100 → establishes playoutSeq=100 and initializes.
	if err := jb.Push(100, 0, make([]byte, 10)); err != nil {
		t.Fatalf("Push 100: %v", err)
	}

	// Drain the prebuffer so we exit buffering mode.
	jb.Flush()
	_, _ = jb.Pop() // pop seq=100, playoutSeq=101

	// seq=103 → diff=2 (< 3), accepted.
	if err := jb.Push(103, 240, make([]byte, 10)); err != nil {
		t.Fatalf("Push 103: %v", err)
	}
	// seq=110 → diff=9 (>= 3), rejected as too-far-in-future.
	if err := jb.Push(110, 800, make([]byte, 10)); err != nil {
		t.Fatalf("Push 110 returned err: %v", err)
	}

	stats := jb.Stats()
	if stats.LatePackets != 1 {
		t.Errorf("expected 1 LatePackets (from seq=110 rejection), got %d", stats.LatePackets)
	}
}

func TestPopIntoBoundsCheck(t *testing.T) {
	jb := jitter.New(jitter.Config{TargetDelay: 10 * time.Millisecond})
	_ = jb.Push(0, 0, make([]byte, 10))

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

func BenchmarkJitterBufferPushPop(b *testing.B) {
	jb := jitter.New(jitter.Config{TargetDelay: 10 * time.Millisecond})
	payload := make([]byte, 10)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		seq := uint16(i & 0xFFFF)
		_ = jb.Push(seq, uint32(i*80), payload)
		_, _ = jb.Pop()
	}
}

func BenchmarkJitterBufferPushPopInto(b *testing.B) {
	jb := jitter.New(jitter.Config{TargetDelay: 10 * time.Millisecond})
	payload := make([]byte, 10)
	var out [10]byte

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		seq := uint16(i & 0xFFFF)
		_ = jb.Push(seq, uint32(i*80), payload)
		_, _, _ = jb.PopInto(out[:])
	}
}

