package rtp_test

import (
	"sync"
	"testing"
	"time"

	"github.com/selawe/go-g729/rtp"
)

func TestRTCPTracker_Nominal(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x12345678)
	start := time.Now()

	// 50 packets @ 20 ms spacing (160 samples timestamp delta)
	for i := range 50 {
		seq := uint16(100 + i)
		ts := uint32(1000 + i*160)
		arrival := start.Add(time.Duration(i*20) * time.Millisecond)
		tracker.RecordPacket(seq, ts, arrival)
	}

	report := tracker.GenerateReport()
	if report.SSRC != 0x12345678 {
		t.Errorf("SSRC = %x, want 0x12345678", report.SSRC)
	}
	if report.CumulativeLost != 0 {
		t.Errorf("CumulativeLost = %d, want 0", report.CumulativeLost)
	}
	if report.FractionLost != 0 {
		t.Errorf("FractionLost = %d, want 0", report.FractionLost)
	}
	if report.PacketsReceived != 50 {
		t.Errorf("PacketsReceived = %d, want 50", report.PacketsReceived)
	}
	if report.PacketsExpected != 50 {
		t.Errorf("PacketsExpected = %d, want 50", report.PacketsExpected)
	}
	if report.LossRate() != 0.0 {
		t.Errorf("LossRate() = %f, want 0.0", report.LossRate())
	}
	if report.InterarrivalJitter != 0 {
		t.Errorf("InterarrivalJitter = %d, want 0 on uniform arrival", report.InterarrivalJitter)
	}

	mos := report.EstimatedMOS(20 * time.Millisecond)
	if mos < 4.0 || mos > 4.2 {
		t.Errorf("EstimatedMOS = %f, want ~4.09 for clean G.729", mos)
	}
}

func TestRTCPTracker_PacketLoss(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x1111)
	start := time.Now()

	// Send 10 packets, drop every odd sequence (100, 102, 104, 106, 108) -> 5 packets received out of 9 expected
	for i := 0; i < 10; i += 2 {
		seq := uint16(100 + i)
		ts := uint32(1000 + i*160)
		arrival := start.Add(time.Duration(i*20) * time.Millisecond)
		tracker.RecordPacket(seq, ts, arrival)
	}

	report := tracker.GenerateReport()
	if report.PacketsReceived != 5 {
		t.Errorf("PacketsReceived = %d, want 5", report.PacketsReceived)
	}
	if report.PacketsExpected != 9 { // 100 to 108 inclusive = 9 packets
		t.Errorf("PacketsExpected = %d, want 9", report.PacketsExpected)
	}
	if report.CumulativeLost != 4 {
		t.Errorf("CumulativeLost = %d, want 4", report.CumulativeLost)
	}
	if report.FractionLost == 0 {
		t.Errorf("FractionLost should be non-zero on packet loss, got 0")
	}

	mos := report.EstimatedMOS(20 * time.Millisecond)
	if mos >= 4.0 {
		t.Errorf("EstimatedMOS should be degraded under ~44%% loss, got %f", mos)
	}
}

func TestRTCPTracker_SequenceWrap(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x2222)
	now := time.Now()

	// Send sequence wrapping around 65535
	tracker.RecordPacket(65534, 1000, now)
	tracker.RecordPacket(65535, 1160, now.Add(20*time.Millisecond))
	tracker.RecordPacket(0, 1320, now.Add(40*time.Millisecond))
	tracker.RecordPacket(1, 1480, now.Add(60*time.Millisecond))

	report := tracker.GenerateReport()
	if report.ExtendedHighestSeq != (1<<16)|1 {
		t.Errorf("ExtendedHighestSeq = %d, want %d", report.ExtendedHighestSeq, (1<<16)|1)
	}
	if report.PacketsReceived != 4 {
		t.Errorf("PacketsReceived = %d, want 4", report.PacketsReceived)
	}
	if report.CumulativeLost != 0 {
		t.Errorf("CumulativeLost = %d, want 0 across wrap", report.CumulativeLost)
	}
}

func TestRTCPTracker_JitterCalculation(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x3333)
	now := time.Now()

	// Packet 1: on time
	tracker.RecordPacket(1, 1000, now)

	// Packet 2: delayed by 30 ms excess (50 ms arrival delta vs 20 ms packet ptime:
	// delta transit = 50 ms - 20 ms = 30 ms = 240 units @ 8 kHz.
	// J = 0 + (240 - 0) / 16 = 15 samples.
	tracker.RecordPacket(2, 1160, now.Add(50*time.Millisecond))

	report := tracker.GenerateReport()
	if report.InterarrivalJitter != 15 {
		t.Errorf("InterarrivalJitter = %d, want 15", report.InterarrivalJitter)
	}
	if report.JitterDuration() != 15*time.Second/8000 {
		t.Errorf("JitterDuration = %v, want %v", report.JitterDuration(), 15*time.Second/8000)
	}
}

// TestRTCPTracker_TimestampWrap verifies that interarrival jitter remains near-zero
// after a 32-bit RTP timestamp wraparound (~6.2 days at 8000 Hz).
// With int64 transit arithmetic the wrap causes a ~4.3 billion sample spike;
// with uint32→int32 modular arithmetic it rounds to zero.
func TestRTCPTracker_TimestampWrap(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x9999)
	now := time.Now()

	// Packet near rollover: ts just before 0xFFFFFFFF
	tsA := uint32(0xFFFFFF00)
	tracker.RecordPacket(1, tsA, now)

	// Next packet: ts rolls over (160 units = 20 ms @ 8000 Hz)
	tsB := tsA + 160 // wraps to 0x0000005F
	tracker.RecordPacket(2, tsB, now.Add(20*time.Millisecond))

	report := tracker.GenerateReport()
	if report.InterarrivalJitter != 0 {
		t.Errorf("InterarrivalJitter = %d after 32-bit wrap, want 0 (uniform delivery)",
			report.InterarrivalJitter)
	}
}

// TestRTCPTracker_NoOverflow verifies that RecordPacket does not overflow int64
// when called with real wall-clock timestamps (UnixNano ~ 1.76e18 in 2026,
// which would overflow if multiplied by ClockRate=8000 before dividing).
func TestRTCPTracker_NoOverflow(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x4444)
	now := time.Now()

	// Two packets 20 ms apart — jitter should be 0 on perfectly uniform arrival.
	tracker.RecordPacket(1, 1000, now)
	tracker.RecordPacket(2, 1160, now.Add(20*time.Millisecond))

	report := tracker.GenerateReport()
	if report.InterarrivalJitter != 0 {
		t.Errorf("InterarrivalJitter = %d with uniform arrival, want 0 (possible overflow in transit calc)",
			report.InterarrivalJitter)
	}
	if report.PacketsReceived != 2 {
		t.Errorf("PacketsReceived = %d, want 2", report.PacketsReceived)
	}
}

// TestRTCPTracker_Concurrent verifies that RecordPacket and GenerateReport are
// safe for concurrent use from separate goroutines (network ingress vs RTCP scheduler).
// Run with -race to detect data races.
func TestRTCPTracker_Concurrent(t *testing.T) {
	tracker := rtp.NewRTCPTracker(0x5555)
	now := time.Now()

	const n = 200
	var wg sync.WaitGroup

	// Writer goroutine: records packets continuously.
	wg.Go(func() {
		for i := range n {
			tracker.RecordPacket(uint16(i), uint32(i*160), now.Add(time.Duration(i*20)*time.Millisecond))
		}
	})

	// Reader goroutine: generates reports while packets are being recorded.
	wg.Go(func() {
		for range 10 {
			_ = tracker.GenerateReport()
			time.Sleep(time.Millisecond)
		}
	})

	wg.Wait()

	report := tracker.GenerateReport()
	if report.PacketsReceived == 0 {
		t.Error("no packets recorded after concurrent run")
	}
}
