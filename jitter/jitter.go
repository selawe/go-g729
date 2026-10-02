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

	// MaxFramesPerPacket is the maximum number of G.729 frames bundled per RTP packet.
	// 16 frames = 160 ms (supports standard 10, 20, 30, 40 ms up to 80 and 160 ms).
	MaxFramesPerPacket = 16
)

// Sentinel errors.
var (
	// ErrBufferFull is returned when incoming packets exceed the ring buffer window.
	ErrBufferFull = errors.New("jitter: buffer capacity exceeded")

	// Deprecated: ErrDstTooSmall was intended to be returned by PopInto for
	// insufficient dst, but PopInto's signature has no error return.  PopInto
	// returns (0, false, false) when len(dst) < rtp.FrameBytes instead.
	// This sentinel is never returned by any function and will be removed in the
	// next major version.
	ErrDstTooSmall = errors.New("jitter: destination buffer must be at least 10 bytes")
)

// Config configures the operating parameters of the jitter buffer.
type Config struct {
	// TargetDelay is the initial playout buffering target (default: 40ms = 4 G.729 frames).
	TargetDelay time.Duration

	// MaxDelay is the maximum allowable playout delay before late packets are discarded (default: 200ms).
	// The value is truncated down to a whole number of 10 ms G.729 frames internally, so for example
	// MaxDelay=25ms behaves identically to MaxDelay=20ms (both map to 2 slots). Use multiples of 10ms
	// to get the exact window you intend.
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

type packetSlot struct {
	seq       uint16
	timestamp uint32
	valid     bool
	nFrames   int
	frames    [MaxFramesPerPacket][rtp.FrameBytes]byte
	frameLens [MaxFramesPerPacket]int
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

	playoutFrameIdx     int
	lastPacketFrames    int
	lossFramesRemaining int

	slots [MaxSlotCount]packetSlot
	stats Stats

	// bufferedCount tracks the total number of frames currently queued in the buffer.
	bufferedCount int

	unpackBuf [MaxFramesPerPacket][rtp.FrameBytes]byte
	dstSlices [MaxFramesPerPacket][]byte
}

// slotIsCurrent reports whether a slot's stored sequence number lies within
// the current playout window [playoutSeq, playoutSeq+MaxSlotCount). Uses
// signed 16-bit subtraction so it handles seq-number wraparound correctly.
func (b *Buffer) slotIsCurrent(slot *packetSlot) bool {
	if !slot.valid {
		return false
	}
	diff := int16(slot.seq - b.playoutSeq)
	return diff >= 0 && int(diff) < MaxSlotCount
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

	// Convert MaxDelay to a slot count using integer truncation (rounded down to whole frames).
	maxSlots := int(max / (rtp.FrameDurationMs * time.Millisecond))
	if maxSlots < 1 {
		maxSlots = 1
	}
	if maxSlots > MaxSlotCount {
		maxSlots = MaxSlotCount
	}

	b := &Buffer{
		targetDelay:      target,
		maxDelay:         max,
		maxSlots:         maxSlots,
		buffering:        true,
		lastPacketFrames: 1,
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
	b.playoutFrameIdx = 0
	b.lastPacketFrames = 1
	b.lossFramesRemaining = 0
	b.stats = Stats{}
	b.bufferedCount = 0
	for i := range b.slots {
		b.slots[i].valid = false
	}
}

// Push inserts an RTP packet containing G.729 payload into the jitter buffer.
// The packet is slotted by its 16-bit RTP sequence number (RFC 3550 §5.1),
// and unpacked into individual 10 ms frames for sequential playout.
func (b *Buffer) Push(seq uint16, timestamp uint32, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.stats.PushedPackets++

	nFrames, _, err := rtp.UnpackInto(b.dstSlices[:], payload)
	if err != nil {
		return err
	}
	if nFrames > MaxFramesPerPacket {
		nFrames = MaxFramesPerPacket
	}

	if !b.initialized {
		b.initialized = true
		b.playoutSeq = seq
		b.playoutFrameIdx = 0
		b.buffering = true
		b.lastPacketFrames = nFrames
	} else if b.buffering {
		diff := int16(seq - b.playoutSeq)
		if diff < 0 && int16(b.playoutSeq-seq) < MaxSlotCount {
			b.playoutSeq = seq
			b.playoutFrameIdx = 0
		}
	} else {
		// Check for late packet: if seq is older than current playoutSeq
		diff := int16(seq - b.playoutSeq)
		if diff < 0 {
			b.stats.LatePackets++
			return nil
		}
		// Enforce MaxDelay: reject packets more than maxSlots ahead of playout
		if int(diff) >= b.maxSlots {
			b.stats.LatePackets++
			return nil
		}
	}

	slotIdx := int(seq & slotMask)
	slot := &b.slots[slotIdx]

	if slot.valid {
		if slot.seq == seq {
			b.stats.DupPackets++
			return nil
		}
		// Overwriting a different-seq entry: adjust bufferedCount
		if b.slotIsCurrent(slot) {
			b.bufferedCount -= slot.nFrames
			if b.bufferedCount < 0 {
				b.bufferedCount = 0
			}
		}
		b.stats.DroppedByWrap++
	}

	slot.seq = seq
	slot.timestamp = timestamp
	slot.valid = true
	slot.nFrames = nFrames
	for i := 0; i < nFrames; i++ {
		flen := len(b.dstSlices[i])
		slot.frameLens[i] = flen
		if flen > 0 {
			copy(slot.frames[i][:flen], b.dstSlices[i])
		}
	}
	b.bufferedCount += nFrames
	b.stats.CurrentBuffered = b.bufferedCount

	return nil
}

// PopInto retrieves the next chronological 10 ms G.729 frame into dst without heap allocation.
// dst must have length >= rtp.FrameBytes (10) to accommodate any valid frame size (speech or SID).
//
// Prefer PopInto over Pop in hot paths: Pop allocates a new []byte per non-loss frame.
//
// Returns:
//   - n: number of bytes copied into dst (10 for speech, 2 for SID, 0 for loss/DTX suppressed frame),
//   - isLoss: true if this slot represents packet loss (trigger PLC in Decoder),
//   - ok: true if playout slot was ready, false if buffer is buffering or starved (underflow).
//
// If len(dst) < rtp.FrameBytes, PopInto returns (0, false, false) without advancing playout state.
// No error is returned in this case; callers must ensure dst is large enough before calling.
func (b *Buffer) PopInto(dst []byte) (n int, isLoss bool, ok bool) {
	if len(dst) < rtp.FrameBytes {
		return 0, false, false
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.initialized {
		return 0, false, false
	}

	b.stats.CurrentBuffered = b.bufferedCount

	targetFrames := int(b.targetDelay / (rtp.FrameDurationMs * time.Millisecond))
	if targetFrames < 1 {
		targetFrames = 1
	}

	if b.buffering {
		if b.bufferedCount >= targetFrames {
			b.buffering = false
		} else {
			return 0, false, false
		}
	}

	if b.bufferedCount <= 0 && b.lossFramesRemaining <= 0 {
		b.bufferedCount = 0 // defensive clamp: guard against any undercount bug
		b.buffering = true
		b.stats.Underflows++
		return 0, false, false
	}

	// Conceal remaining frames of a multi-frame lost packet
	if b.lossFramesRemaining > 0 {
		b.lossFramesRemaining--
		b.stats.EmittedPLC++
		b.stats.CurrentBuffered = b.bufferedCount
		return 0, true, true
	}

	slotIdx := int(b.playoutSeq & slotMask)
	slot := &b.slots[slotIdx]

	if slot.valid && slot.seq == b.playoutSeq {
		if b.playoutFrameIdx < slot.nFrames {
			flen := slot.frameLens[b.playoutFrameIdx]
			if flen > 0 {
				n = copy(dst, slot.frames[b.playoutFrameIdx][:flen])
			} else {
				n = 0
			}
			b.playoutFrameIdx++
			b.bufferedCount--
			b.stats.PoppedFrames++
			b.stats.CurrentBuffered = b.bufferedCount

			if b.playoutFrameIdx >= slot.nFrames {
				// Packet completed
				b.lastPacketFrames = slot.nFrames
				slot.valid = false
				b.playoutSeq++
				b.playoutFrameIdx = 0
			}
			return n, false, true
		}
	}

	// Gap detected: packet loss! Advance playoutSeq and signal PLC.
	lossFrames := b.lastPacketFrames
	if lossFrames < 1 {
		lossFrames = 1
	}
	if lossFrames > MaxFramesPerPacket {
		lossFrames = MaxFramesPerPacket
	}

	b.playoutSeq++
	b.playoutFrameIdx = 0
	b.lossFramesRemaining = lossFrames - 1

	// Drop stale slot at the new window boundary if it held an unpopped entry
	agedIdx := int((b.playoutSeq + uint16(MaxSlotCount-1)) & slotMask)
	agedSlot := &b.slots[agedIdx]
	if agedSlot.valid && !b.slotIsCurrent(agedSlot) {
		b.bufferedCount -= agedSlot.nFrames
		if b.bufferedCount < 0 {
			b.bufferedCount = 0
		}
		agedSlot.valid = false
	}

	b.stats.EmittedPLC++
	b.stats.CurrentBuffered = b.bufferedCount
	return 0, true, true
}

// Pop retrieves the next chronological 10 ms G.729 frame.
// It allocates a new []byte for each non-loss frame; use PopInto to avoid allocation.
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
	b.stats.CurrentBuffered = b.bufferedCount
	return b.stats
}
