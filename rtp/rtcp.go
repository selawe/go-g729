package rtp

import (
	"math"
	"sync"
	"time"
)

// ReceptionReport captures RFC 3550 §6.4.1 reception statistics for a G.729 RTP stream.
type ReceptionReport struct {
	// SSRC is the synchronization source identifier of the data source.
	SSRC uint32

	// FractionLost is the fraction of RTP data packets lost since the previous report,
	// expressed as an 8-bit fixed point number (0..255), where 256 corresponds to 100% loss.
	FractionLost uint8

	// CumulativeLost is the total number of RTP data packets from source SSRC that have
	// been lost since the beginning of reception.
	// NOTE: RFC 3550 §6.4.1 defines this field as 24-bit signed in the on-wire RTCP
	// packet. When serialising into a real RTCP RR, callers must clamp this to 0x7FFFFF
	// (8 388 607) and handle the 24-bit signed encoding.
	CumulativeLost uint32

	// ExtendedHighestSeq is the extended highest sequence number received,
	// with the low 16 bits containing the highest sequence number received
	// and the high 16 bits containing the count of sequence number cycles.
	ExtendedHighestSeq uint32

	// InterarrivalJitter is the estimated statistical variance of the RTP data packet
	// interarrival time, measured in timestamp units (samples @ 8000 Hz).
	InterarrivalJitter uint32

	// PacketsReceived is the total count of valid RTP packets received in this session.
	PacketsReceived uint32

	// PacketsExpected is the total count of RTP packets expected from this source.
	PacketsExpected uint32
}

// LossRate returns the fractional packet loss as a float64 in the range [0.0, 1.0].
func (r *ReceptionReport) LossRate() float64 {
	return float64(r.FractionLost) / 256.0
}

// JitterDuration returns the interarrival jitter as a time.Duration.
func (r *ReceptionReport) JitterDuration() time.Duration {
	// G.729 clock rate is 8000 Hz (1 sample = 125 µs)
	return time.Duration(r.InterarrivalJitter) * time.Second / ClockRate
}

// EstimatedMOS computes the Mean Opinion Score (MOS-CQO) estimate for the G.729 stream
// using the ITU-T G.107 E-model based on the current packet loss and jitter.
// Returns a value between 1.0 (bad) and 4.5 (optimal for toll-quality speech).
func (r *ReceptionReport) EstimatedMOS(oneWayDelay time.Duration) float64 {
	// Base G.729 E-model constants per ITU-T G.107 / G.113
	const (
		r0  = 93.2 // Basic signal-to-noise ratio
		ie  = 11.0 // Equipment impairment for G.729A
		bpl = 19.0 // Packet loss robustness factor for G.729A with PLC
	)

	// One-way delay impairment (Id) in ms
	dMs := float64(oneWayDelay / time.Millisecond)
	if dMs <= 0 {
		dMs = 20.0 // Default nominal delay (20 ms)
	}

	var id float64
	if dMs > 100.0 {
		id = 0.024*dMs + 0.11*(dMs-100.0)
	} else {
		id = 0.024 * dMs
	}

	// Packet loss impairment (Ie,eff)
	pLoss := r.LossRate() * 100.0 // Percentage 0..100
	ieEff := ie + (95.0-ie)*(pLoss/(pLoss+bpl))

	// R-factor calculation
	rFactor := r0 - id - ieEff
	if rFactor < 0.0 {
		rFactor = 0.0
	} else if rFactor > 100.0 {
		rFactor = 100.0
	}

	// Convert R-factor to MOS per ITU-T G.107 formula
	if rFactor <= 0.0 {
		return 1.0
	}
	if rFactor >= 100.0 {
		return 4.5
	}
	mos := 1.0 + 0.035*rFactor + rFactor*(rFactor-60.0)*(100.0-rFactor)*7.0e-6
	if mos < 1.0 {
		return 1.0
	}
	if mos > 4.5 {
		return 4.5
	}
	return mos
}

// RTCPTracker computes real-time RFC 3550 §6.4.1 reception statistics and interarrival jitter
// for an incoming G.729 RTP stream.
//
// RTCPTracker is safe for concurrent use: RecordPacket and GenerateReport may be
// called from different goroutines (e.g. network ingress and RTCP scheduler).
type RTCPTracker struct {
	mu sync.Mutex

	ssrc uint32

	initialized bool
	baseSeq     uint16
	maxSeq      uint16
	cycles      uint32
	received    uint32

	priorExpected uint32
	priorReceived uint32

	// RFC 3550 Interarrival Jitter state
	jitter      float64 // in timestamp units (1/8000 s)
	lastTransit int64
}

// NewRTCPTracker creates a new RTCPTracker for the given SSRC.
func NewRTCPTracker(ssrc uint32) *RTCPTracker {
	return &RTCPTracker{
		ssrc: ssrc,
	}
}

// RecordPacket records the arrival of an RTP packet with sequence number seq,
// RTP timestamp ts, and local arrival time arrival.
// Safe for concurrent use with GenerateReport and Reset.
func (t *RTCPTracker) RecordPacket(seq uint16, ts uint32, arrival time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Convert arrival time to RTP timestamp clock units (8000 Hz).
	// Divide first to avoid int64 overflow: at 8 kHz, 1 sample = 125_000 ns.
	// arrival.UnixNano() ~ 1.76e18 in 2026; multiplying by 8000 first would overflow.
	arrivalUnits := arrival.UnixNano() / (1_000_000_000 / ClockRate)

	if !t.initialized {
		t.baseSeq = seq
		t.maxSeq = seq
		t.cycles = 0
		t.received = 1
		t.lastTransit = arrivalUnits - int64(ts)
		t.jitter = 0.0
		t.initialized = true
		return
	}

	// 1. Update sequence number tracking & 16-bit wrap-around cycles
	diff := int16(seq - t.maxSeq)
	if diff > 0 {
		if seq < t.maxSeq {
			// Sequence wrapped around 65535 -> 0
			t.cycles += 1 << 16
		}
		t.maxSeq = seq
	}
	t.received++

	// 2. RFC 3550 §6.4.1 Interarrival Jitter calculation:
	// D(i-1, i) = (R_i - S_i) - (R_{i-1} - S_{i-1})
	transit := arrivalUnits - int64(ts)
	d := transit - t.lastTransit
	t.lastTransit = transit
	if d < 0 {
		d = -d
	}

	// J = J + (|D| - J) / 16.0
	t.jitter += (float64(d) - t.jitter) / 16.0
}

// GenerateReport produces an RFC 3550 ReceptionReport reflecting packets received
// since the tracker started or since the last interval reset.
// Safe for concurrent use with RecordPacket and Reset.
func (t *RTCPTracker) GenerateReport() ReceptionReport {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.initialized {
		return ReceptionReport{SSRC: t.ssrc}
	}

	extendedMax := t.cycles + uint32(t.maxSeq)
	expected := extendedMax - uint32(t.baseSeq) + 1

	var cumulativeLost uint32
	if expected > t.received {
		cumulativeLost = expected - t.received
	}

	// Interval calculation for FractionLost
	expectedInterval := expected - t.priorExpected
	receivedInterval := t.received - t.priorReceived

	var fractionLost uint8
	if expectedInterval > 0 && expectedInterval > receivedInterval {
		lostInterval := uint64(expectedInterval - receivedInterval)
		frac := min((lostInterval<<8)/uint64(expectedInterval), 255)
		fractionLost = uint8(frac)
	}

	t.priorExpected = expected
	t.priorReceived = t.received

	jitterInt := uint32(math.Round(t.jitter))

	return ReceptionReport{
		SSRC:               t.ssrc,
		FractionLost:       fractionLost,
		CumulativeLost:     cumulativeLost,
		ExtendedHighestSeq: extendedMax,
		InterarrivalJitter: jitterInt,
		PacketsReceived:    t.received,
		PacketsExpected:    expected,
	}
}

// Reset clears the tracker to its uninitialized state.
// Safe for concurrent use.
func (t *RTCPTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initialized = false
	t.baseSeq = 0
	t.maxSeq = 0
	t.cycles = 0
	t.received = 0
	t.priorExpected = 0
	t.priorReceived = 0
	t.jitter = 0
	t.lastTransit = 0
}
