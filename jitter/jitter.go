// Package jitter provides an adaptive jitter buffer for G.729 VoIP RTP audio streams.
//
// Key features:
//   - Packet reordering by 16-bit RTP sequence number with wrap-around support (RFC 3550).
//   - Multi-frame packet unpacking (20 ms, 30 ms, 40 ms payloads).
//   - Automatic packet loss detection: emits nil frames to trigger G.729 Packet Loss Concealment (PLC).
//   - Configurable target playout delay and maximum retention buffer.
//   - Thread-safe for concurrent RTP network ingress and audio playback threads.
package jitter

import (
	"errors"
	"sync"
	"time"

	"github.com/selawe/go-g729/rtp"
)

// Default settings.
const (
	// DefaultTargetDelay is the default playout delay buffering (40 ms = 4 G.729 frames).
	DefaultTargetDelay = 40 * time.Millisecond

	// DefaultMaxDelay is the default maximum packet retention window (200 ms).
	DefaultMaxDelay = 200 * time.Millisecond

	// MaxSlotCount is the size of the ring buffer (must be power of 2 for fast modular arithmetic).
	MaxSlotCount = 128
	slotMask     = MaxSlotCount - 1
)

// Sentinel errors.
var (
	// ErrBufferFull is returned when incoming packets exceed the ring buffer window.
	ErrBufferFull = errors.New("jitter: buffer capacity exceeded")

	// ErrDstTooSmall is returned by PopInto when dst has insufficient capacity
	// to hold a full G.729 speech frame (10 bytes).
	ErrDstTooSmall = errors.New("jitter: destination buffer must be at least 10 bytes")
)

// Config configures the operating parameters of the jitter buffer.
type Config struct {
	// TargetDelay is the initial playout buffering target (default: 40ms = 4 G.729 frames).
	TargetDelay time.Duration

	// MaxDelay is the maximum allowable playout delay before late packets are discarded (default: 200ms).
	MaxDelay time.Duration
}

// Stats captures observability metrics for the jitter buffer.
type Stats struct {
	PushedPackets   int // Total RTP packets pushed
	PoppedFrames    int // Valid G.729 frames returned
	EmittedPLC      int // Loss concealment (nil) frames emitted
	LatePackets     int // Packets discarded because they arrived after playout
	DupPackets      int // Duplicate packets discarded
	DroppedByWrap   int // Slots overwritten due to ring buffer wraparound
	Underflows      int // Playout buffer starvation occurrences
	CurrentBuffered int // Current number of frames queued
}

type frameSlot struct {
	seq       uint16
	timestamp uint32
	valid     bool
	data      [rtp.FrameBytes]byte
	dataLen   int
}

// Buffer implements a concurrent-safe adaptive jitter buffer for G.729 RTP audio.
type Buffer struct {
	mu sync.Mutex

	targetDelay time.Duration
	maxDelay    time.Duration
	maxSlots    int

	initialized bool
	buffering   bool
	playoutSeq  uint16

	slots [MaxSlotCount]frameSlot
	stats Stats

	// unpackBuf sized for the largest realistic RTP payload: 8 frames = 80 ms.
	// Common packetization intervals are 10, 20, 30, 40 ms (1-4 frames), but
	// some legacy PBX gateways bundle up to 80 ms.
	unpackBuf [8][10]byte
	dstSlices [8][]byte
}

// New creates a new jitter buffer with the given configuration.
func New(cfg Config) *Buffer {
	target := cfg.TargetDelay
	if target <= 0 {
		target = DefaultTargetDelay
	}
	max := cfg.MaxDelay
	if max <= 0 {
		max = DefaultMaxDelay
	}
	if max < target {
		max = target
	}

	// Convert MaxDelay to a slot count, capped at ring buffer size.
	maxSlots := int(max / (rtp.FrameDurationMs * time.Millisecond))
	if maxSlots < 1 {
		maxSlots = 1
	}
	if maxSlots > MaxSlotCount {
		maxSlots = MaxSlotCount
	}

	b := &Buffer{
		targetDelay: target,
		maxDelay:    max,
		maxSlots:    maxSlots,
		buffering:   true,
	}
	for i := range b.unpackBuf {
		b.dstSlices[i] = b.unpackBuf[i][:]
	}
	return b
}

// Reset clears all queued frames and resets playout state.
func (b *Buffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.initialized = false
	b.buffering = true
	b.playoutSeq = 0
	b.stats = Stats{}
	for i := range b.slots {
		b.slots[i].valid = false
	}
}

// Push inserts an RTP packet containing G.729 payload into the jitter buffer.
// The payload is unpacked into individual 10 ms frames and slotted by sequence number.
func (b *Buffer) Push(seq uint16, timestamp uint32, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stats.PushedPackets++

	nFrames, _, err := rtp.UnpackInto(b.dstSlices[:], payload)
	if err != nil {
		return err
	}

	if !b.initialized {
		b.initialized = true
		b.playoutSeq = seq
		b.buffering = true
	} else if b.buffering {
		diff := int16(seq - b.playoutSeq)
		if diff < 0 && int16(b.playoutSeq-seq) < MaxSlotCount {
			b.playoutSeq = seq
		}
	} else {
		// Check for late packet: if seq is older than current playoutSeq
		diff := int16(seq - b.playoutSeq)
		if diff < 0 {
			b.stats.LatePackets++
			return nil
		}
		// Enforce MaxDelay: reject packets more than maxSlots frames ahead of
		// playout (they would push effective playout latency past MaxDelay).
		if int(diff) >= b.maxSlots {
			b.stats.LatePackets++
			return nil
		}
	}

	// Slot each frame contained in this packet
	for i := 0; i < nFrames; i++ {
		slotSeq := seq + uint16(i)
		slotIdx := int(slotSeq & slotMask)
		slot := &b.slots[slotIdx]

		if slot.valid {
			if slot.seq == slotSeq {
				b.stats.DupPackets++
				continue
			}
			b.stats.DroppedByWrap++
		}

		slot.seq = slotSeq
		slot.timestamp = timestamp + uint32(i*rtp.TimestampIncrement)
		slot.valid = true
		if b.dstSlices[i] != nil {
			slot.dataLen = len(b.dstSlices[i])
			copy(slot.data[:slot.dataLen], b.dstSlices[i])
		} else {
			slot.dataLen = 0
		}
	}

	return nil
}

// PopInto retrieves the next chronological 10 ms G.729 frame into dst without heap allocation.
// dst must have length >= rtp.FrameBytes (10) to accommodate any valid frame size (speech or SID).
//
// Returns:
//   - n: number of bytes copied into dst (10 for speech, 2 for SID, 0 for loss/DTX suppressed frame),
//   - isLoss: true if this slot represents packet loss (trigger PLC in Decoder),
//   - ok: true if playout slot was ready, false if buffer is buffering or starved (underflow).
//
// If dst is too small, PopInto returns (0, false, false) without advancing playout state.
func (b *Buffer) PopInto(dst []byte) (n int, isLoss bool, ok bool) {
	if len(dst) < rtp.FrameBytes {
		return 0, false, false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.initialized {
		return 0, false, false
	}

	bufferedCount := 0
	for i := 0; i < MaxSlotCount; i++ {
		slotIdx := int((b.playoutSeq + uint16(i)) & slotMask)
		if b.slots[slotIdx].valid && b.slots[slotIdx].seq == b.playoutSeq+uint16(i) {
			bufferedCount++
		}
	}
	b.stats.CurrentBuffered = bufferedCount

	targetFrames := int(b.targetDelay / (rtp.FrameDurationMs * time.Millisecond))
	if targetFrames < 1 {
		targetFrames = 1
	}

	if b.buffering {
		if bufferedCount >= targetFrames {
			b.buffering = false
		} else {
			return 0, false, false
		}
	}

	if bufferedCount == 0 {
		b.buffering = true
		b.stats.Underflows++
		return 0, false, false
	}

	slotIdx := int(b.playoutSeq & slotMask)
	slot := &b.slots[slotIdx]

	if slot.valid && slot.seq == b.playoutSeq {
		slot.valid = false
		b.playoutSeq++
		b.stats.PoppedFrames++
		if slot.dataLen > 0 {
			n = copy(dst, slot.data[:slot.dataLen])
			return n, false, true
		}
		// DTX suppressed frame
		return 0, false, true
	}

	// Gap detected: packet loss! Advance playoutSeq and signal PLC.
	b.playoutSeq++
	b.stats.EmittedPLC++
	return 0, true, true
}

// Pop retrieves the next chronological 10 ms G.729 frame.
//
// Returns:
//   - frame: byte slice (10-byte speech or 2-byte SID) if a valid frame is ready,
//     or nil if a packet loss occurred (caller must feed nil to Decoder.Decode for PLC),
//   - ok: true if playout is active (frame or loss concealment ready), false if buffer
//     is currently prebuffering or completely empty.
func (b *Buffer) Pop() (frame []byte, ok bool) {
	var buf [10]byte
	n, isLoss, ready := b.PopInto(buf[:])
	if !ready {
		return nil, false
	}
	if isLoss || n == 0 {
		return nil, true
	}
	res := make([]byte, n)
	copy(res, buf[:n])
	return res, true
}

// Flush exits prebuffering mode so any buffered frames can be drained immediately.
func (b *Buffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffering = false
}

// Stats returns a snapshot of current jitter buffer performance metrics.
func (b *Buffer) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stats
}
