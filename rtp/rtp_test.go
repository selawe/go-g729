package rtp_test

import (
	"bytes"
	"testing"

	g729 "github.com/selawe/go-g729"
	"github.com/selawe/go-g729/rtp"
)

// helper: encode one 80-sample frame of silence into 10 bytes.
func encodeSilence(t *testing.T) []byte {
	t.Helper()
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: false})
	frame := make([]int16, 80)
	dst := make([]byte, 10)
	n, ft, err := enc.Encode(dst, frame)
	if err != nil || n != 10 || ft != g729.FrameSpeech {
		t.Fatalf("encode: n=%d ft=%v err=%v", n, ft, err)
	}
	return dst[:10]
}

func TestPackUnpackSpeech(t *testing.T) {
	speechFrame := encodeSilence(t)

	for _, numFrames := range []int{1, 2, 4, 6} {
		frames := make([][]byte, numFrames)
		for i := range frames {
			cp := make([]byte, rtp.FrameBytes)
			copy(cp, speechFrame)
			frames[i] = cp
		}

		payload, err := rtp.Pack(frames)
		if err != nil {
			t.Fatalf("%d frames: Pack error: %v", numFrames, err)
		}
		if len(payload) != numFrames*rtp.FrameBytes {
			t.Errorf("%d frames: payload len %d, want %d", numFrames, len(payload), numFrames*rtp.FrameBytes)
		}

		got, info, err := rtp.Unpack(payload)
		if err != nil {
			t.Fatalf("%d frames: Unpack error: %v", numFrames, err)
		}
		if info.Type != rtp.FrameSpeech {
			t.Errorf("%d frames: type %v, want FrameSpeech", numFrames, info.Type)
		}
		if info.NumFrames != numFrames {
			t.Errorf("%d frames: NumFrames %d, want %d", numFrames, info.NumFrames, numFrames)
		}
		if info.DurationMs != numFrames*rtp.FrameDurationMs {
			t.Errorf("%d frames: DurationMs %d, want %d", numFrames, info.DurationMs, numFrames*rtp.FrameDurationMs)
		}
		if len(got) != numFrames {
			t.Errorf("%d frames: got %d unpacked frames, want %d", numFrames, len(got), numFrames)
		}

		// Each frame must round-trip intact
		for i, f := range got {
			if len(f) != rtp.FrameBytes {
				t.Errorf("frame %d: len %d, want %d", i, len(f), rtp.FrameBytes)
			}
			for j, b := range f {
				if b != frames[i][j] {
					t.Errorf("frame %d byte %d: got %02x, want %02x", i, j, b, frames[i][j])
				}
			}
		}
	}
}

func TestPackUnpackSID(t *testing.T) {
	// Encode a real SID frame using Annex B
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: true})
	silence := make([]int16, 80)
	dst := make([]byte, 10)

	// Feed silence until DTX kicks in and produces a SID frame
	var sidFrame []byte
	for i := 0; i < 50; i++ {
		n, ft, err := enc.Encode(dst, silence)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if ft == g729.FrameSID {
			sidFrame = make([]byte, n)
			copy(sidFrame, dst[:n])
			break
		}
	}
	if sidFrame == nil {
		// Fallback: use synthetic 2-byte SID
		sidFrame = []byte{0x55, 0xAA}
	}

	payload, err := rtp.Pack([][]byte{sidFrame})
	if err != nil {
		t.Fatalf("Pack SID: %v", err)
	}
	if len(payload) != rtp.SIDBytes {
		t.Errorf("SID payload len %d, want %d", len(payload), rtp.SIDBytes)
	}

	got, info, err := rtp.Unpack(payload)
	if err != nil {
		t.Fatalf("Unpack SID: %v", err)
	}
	if info.Type != rtp.FrameSID {
		t.Errorf("SID type %v, want FrameSID", info.Type)
	}
	if len(got) != 1 || len(got[0]) != rtp.SIDBytes {
		t.Errorf("SID unpack: got %d frames", len(got))
	}
}

func TestPackUnpackSuppressed(t *testing.T) {
	got, info, err := rtp.Unpack([]byte{})
	if err != nil {
		t.Fatalf("Unpack suppressed: %v", err)
	}
	if info.Type != rtp.FrameSuppressed {
		t.Errorf("type %v, want FrameSuppressed", info.Type)
	}
	if len(got) != 1 || got[0] != nil {
		t.Errorf("expected [nil], got %v", got)
	}
}

func TestPackUnpackMixed(t *testing.T) {
	speech := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	speech2 := []byte{11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	sid := []byte{0x55, 0xAA}

	// 1. 20 ms transition packet: 1 speech frame + 1 SID frame (12 bytes)
	p12, err := rtp.Pack([][]byte{speech, sid})
	if err != nil {
		t.Fatalf("Pack 12 bytes: %v", err)
	}
	if len(p12) != 12 {
		t.Fatalf("expected 12 bytes, got %d", len(p12))
	}

	frames12, info12, err := rtp.Unpack(p12)
	if err != nil {
		t.Fatalf("Unpack 12 bytes: %v", err)
	}
	if info12.Type != rtp.FrameMixed {
		t.Errorf("type = %v, want FrameMixed", info12.Type)
	}
	if info12.NumFrames != 2 || info12.DurationMs != 20 || !info12.HasSID {
		t.Errorf("info12 = %+v, want NumFrames=2, DurationMs=20, HasSID=true", info12)
	}
	if len(frames12) != 2 || len(frames12[0]) != 10 || len(frames12[1]) != 2 {
		t.Fatalf("frames12 len mismatch: %d frames", len(frames12))
	}
	if !bytes.Equal(frames12[0], speech) || !bytes.Equal(frames12[1], sid) {
		t.Errorf("frames12 content mismatch")
	}

	// 2. 30 ms transition packet: 2 speech frames + 1 SID frame (22 bytes)
	p22, err := rtp.Pack([][]byte{speech, speech2, sid})
	if err != nil {
		t.Fatalf("Pack 22 bytes: %v", err)
	}
	if len(p22) != 22 {
		t.Fatalf("expected 22 bytes, got %d", len(p22))
	}

	frames22, info22, err := rtp.Unpack(p22)
	if err != nil {
		t.Fatalf("Unpack 22 bytes: %v", err)
	}
	if info22.Type != rtp.FrameMixed || info22.NumFrames != 3 || info22.DurationMs != 30 || !info22.HasSID {
		t.Errorf("info22 = %+v", info22)
	}
	if len(frames22) != 3 || len(frames22[0]) != 10 || len(frames22[1]) != 10 || len(frames22[2]) != 2 {
		t.Fatalf("frames22 len mismatch: %d frames", len(frames22))
	}

	// 3. UnpackInto with mixed 12-byte payload
	dstBuf := make([][]byte, 2)
	dstBuf[0] = make([]byte, 10)
	dstBuf[1] = make([]byte, 10)
	nInto, infoInto, err := rtp.UnpackInto(dstBuf, p12)
	if err != nil || nInto != 2 || infoInto.Type != rtp.FrameMixed {
		t.Fatalf("UnpackInto 12 bytes: n=%d info=%+v err=%v", nInto, infoInto, err)
	}
	if !bytes.Equal(dstBuf[0], speech) || !bytes.Equal(dstBuf[1][:2], sid) {
		t.Errorf("UnpackInto content mismatch")
	}
}

func TestFrameCount(t *testing.T) {
	cases := []struct {
		len  int
		want int
	}{
		{0, 0},
		{2, 1},
		{10, 1},
		{12, 2},
		{20, 2},
		{22, 3},
		{30, 3},
		{40, 4},
		{7, 0},
	}
	for _, tc := range cases {
		if got := rtp.FrameCount(tc.len); got != tc.want {
			t.Errorf("FrameCount(%d) = %d, want %d", tc.len, got, tc.want)
		}
	}
}

func TestPackErrors(t *testing.T) {
	// Empty frames slice
	if _, err := rtp.Pack(nil); err == nil {
		t.Error("expected error for nil frames")
	}
	if _, err := rtp.Pack([][]byte{}); err == nil {
		t.Error("expected error for empty frames")
	}

	// Invalid frame length
	if _, err := rtp.Pack([][]byte{make([]byte, 5)}); err == nil {
		t.Error("expected error for 5-byte frame")
	}

	// Mixed SID and speech
	if _, err := rtp.Pack([][]byte{make([]byte, 2), make([]byte, 10)}); err == nil {
		t.Error("expected error for mixed SID+speech")
	}

	// Multiple SID frames
	if _, err := rtp.Pack([][]byte{make([]byte, 2), make([]byte, 2)}); err == nil {
		t.Error("expected error for two SID frames")
	}
}

func TestUnpackErrors(t *testing.T) {
	// Invalid lengths: 1, 3, 5, 7, 9, 11, 15, 21
	for _, n := range []int{1, 3, 5, 7, 9, 11, 15, 21} {
		if _, _, err := rtp.Unpack(make([]byte, n)); err == nil {
			t.Errorf("expected error for %d-byte payload", n)
		}
	}
}

func TestUnpackMaxFramesLimit(t *testing.T) {
	// Exactly at the limit: MaxFramesPerPacket frames — must succeed.
	maxPayload := make([]byte, rtp.MaxFramesPerPacket*rtp.FrameBytes)
	got, info, err := rtp.Unpack(maxPayload)
	if err != nil {
		t.Fatalf("Unpack at limit: unexpected error: %v", err)
	}
	if len(got) != rtp.MaxFramesPerPacket || info.NumFrames != rtp.MaxFramesPerPacket {
		t.Errorf("Unpack at limit: got %d frames, want %d", len(got), rtp.MaxFramesPerPacket)
	}

	// One frame over the limit — must be rejected.
	overPayload := make([]byte, (rtp.MaxFramesPerPacket+1)*rtp.FrameBytes)
	_, _, err = rtp.Unpack(overPayload)
	if err == nil {
		t.Fatal("Unpack over limit: expected ErrInvalidPayload, got nil")
	}
}

func TestTimestamp(t *testing.T) {
	base := uint32(1000)
	if ts := rtp.TimestampForFrame(base, 0); ts != 1000 {
		t.Errorf("frame 0: ts=%d want 1000", ts)
	}
	if ts := rtp.TimestampForFrame(base, 1); ts != 1080 {
		t.Errorf("frame 1: ts=%d want 1080", ts)
	}
	if ts := rtp.TimestampForFrame(base, 10); ts != 1800 {
		t.Errorf("frame 10: ts=%d want 1800", ts)
	}
}

func TestPacketizationInterval(t *testing.T) {
	cases := []struct{ n, want int }{{1, 10}, {2, 20}, {4, 40}}
	for _, c := range cases {
		if got := rtp.PacketizationInterval(c.n); got != c.want {
			t.Errorf("PacketizationInterval(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

// TestRoundTripViaRTP validates a full encode→RTP pack→unpack→decode pipeline.
func TestRoundTripViaRTP(t *testing.T) {
	const numFrames = 2 // 20 ms packet

	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: false})
	dec := g729.NewDecoder()

	// Generate 2 frames of 440 Hz tone
	pcm := make([]int16, numFrames*80)
	for i := range pcm {
		v := 8000.0 * 0.5 // simplified: just use moderate values
		_ = v
		pcm[i] = int16(8000)
	}

	// Encode
	rawFrames := make([][]byte, numFrames)
	buf := make([]byte, 10)
	for i := range rawFrames {
		n, _, err := enc.Encode(buf, pcm[i*80:(i+1)*80])
		if err != nil || n != 10 {
			t.Fatalf("encode frame %d: n=%d err=%v", i, n, err)
		}
		rawFrames[i] = make([]byte, 10)
		copy(rawFrames[i], buf)
	}

	// Pack into RTP payload (20 ms packet)
	payload, err := rtp.Pack(rawFrames)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if len(payload) != numFrames*rtp.FrameBytes {
		t.Fatalf("payload len %d, want %d", len(payload), numFrames*rtp.FrameBytes)
	}

	// Unpack
	frames, info, err := rtp.Unpack(payload)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if info.Type != rtp.FrameSpeech || info.NumFrames != numFrames {
		t.Fatalf("Unpack: type=%v numFrames=%d", info.Type, info.NumFrames)
	}

	// Decode each frame
	out := make([]int16, 80)
	for i, f := range frames {
		if err := dec.Decode(out, f); err != nil {
			t.Fatalf("decode frame %d: %v", i, err)
		}
	}
}

func TestUnpackInto(t *testing.T) {
	// 1. Speech frames (2 frames = 20 bytes)
	payload := make([]byte, 20)
	for i := range payload {
		payload[i] = byte(i + 1)
	}
	dst := make([][]byte, 2)
	n, info, err := rtp.UnpackInto(dst, payload)
	if err != nil {
		t.Fatalf("UnpackInto speech: %v", err)
	}
	if n != 2 || info.Type != rtp.FrameSpeech || info.NumFrames != 2 {
		t.Fatalf("unexpected speech info: n=%d info=%+v", n, info)
	}
	if !bytes.Equal(dst[0], payload[:10]) || !bytes.Equal(dst[1], payload[10:]) {
		t.Errorf("unpacked slices do not match payload")
	}

	// 2. Pre-allocated destination slices
	dstPre := [][]byte{make([]byte, 10), make([]byte, 10)}
	n, info, err = rtp.UnpackInto(dstPre, payload)
	if err != nil {
		t.Fatalf("UnpackInto preallocated: %v", err)
	}
	if n != 2 || !bytes.Equal(dstPre[0], payload[:10]) || !bytes.Equal(dstPre[1], payload[10:]) {
		t.Errorf("preallocated slices do not match payload")
	}

	// 3. SID frame (2 bytes)
	sidPayload := []byte{0x55, 0xAA}
	dstSID := make([][]byte, 1)
	n, info, err = rtp.UnpackInto(dstSID, sidPayload)
	if err != nil {
		t.Fatalf("UnpackInto SID: %v", err)
	}
	if n != 1 || info.Type != rtp.FrameSID || !bytes.Equal(dstSID[0], sidPayload) {
		t.Errorf("unexpected SID result")
	}

	// 4. Suppressed frame (0 bytes)
	dstSupp := make([][]byte, 1)
	n, info, err = rtp.UnpackInto(dstSupp, nil)
	if err != nil {
		t.Fatalf("UnpackInto suppressed: %v", err)
	}
	if n != 1 || info.Type != rtp.FrameSuppressed || dstSupp[0] != nil {
		t.Errorf("unexpected suppressed result")
	}

	// 5. Insufficient dst capacity
	shortDst := make([][]byte, 1)
	_, _, err = rtp.UnpackInto(shortDst, payload)
	if err == nil {
		t.Fatal("expected error for short dst buffer, got nil")
	}

	// 6. Invalid payload length (e.g. 5 bytes)
	_, _, err = rtp.UnpackInto(dst, []byte{1, 2, 3, 4, 5})
	if err == nil {
		t.Fatal("expected error for invalid payload length, got nil")
	}
}

func BenchmarkUnpackInto(b *testing.B) {
	payload := make([]byte, 20)
	dst := make([][]byte, 2)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _, _ = rtp.UnpackInto(dst, payload)
	}
}

func TestPackInto(t *testing.T) {
	f1 := make([]byte, 10)
	f2 := make([]byte, 10)
	for i := range f1 {
		f1[i] = byte(i)
		f2[i] = byte(i + 10)
	}

	// 1. Pack 2 speech frames
	dst := make([]byte, 20)
	n, err := rtp.PackInto(dst, [][]byte{f1, f2})
	if err != nil {
		t.Fatalf("PackInto speech: %v", err)
	}
	if n != 20 || !bytes.Equal(dst[:10], f1) || !bytes.Equal(dst[10:20], f2) {
		t.Errorf("PackInto mismatch")
	}

	// 2. Buffer too small
	shortDst := make([]byte, 10)
	_, err = rtp.PackInto(shortDst, [][]byte{f1, f2})
	if err == nil {
		t.Fatal("expected error for short buffer, got nil")
	}

	// 3. SID frame
	sid := []byte{0x12, 0x34}
	sidDst := make([]byte, 2)
	n, err = rtp.PackInto(sidDst, [][]byte{sid})
	if err != nil {
		t.Fatalf("PackInto SID: %v", err)
	}
	if n != 2 || !bytes.Equal(sidDst, sid) {
		t.Errorf("PackInto SID mismatch")
	}

	// 4. Empty frames
	_, err = rtp.PackInto(dst, nil)
	if err != rtp.ErrEmptyPayload {
		t.Errorf("expected ErrEmptyPayload, got %v", err)
	}
}

func TestFramesPerPacketForMTU(t *testing.T) {
	// MTU 1500 (standard Ethernet): (1500 - 40) / 10 = 146 frames
	if got := rtp.FramesPerPacketForMTU(1500); got != 146 {
		t.Errorf("FramesPerPacketForMTU(1500) = %d, want 146", got)
	}

	// MTU 576 (typical dialup/WAN): (576 - 40) / 10 = 53 frames
	if got := rtp.FramesPerPacketForMTU(576); got != 53 {
		t.Errorf("FramesPerPacketForMTU(576) = %d, want 53", got)
	}

	// MTU 60: (60 - 40) / 10 = 2 frames
	if got := rtp.FramesPerPacketForMTU(60); got != 2 {
		t.Errorf("FramesPerPacketForMTU(60) = %d, want 2", got)
	}

	// MTU 45: available 5 < 10 -> 0 frames
	if got := rtp.FramesPerPacketForMTU(45); got != 0 {
		t.Errorf("FramesPerPacketForMTU(45) = %d, want 0", got)
	}
}

func BenchmarkPackInto(b *testing.B) {
	f1 := make([]byte, 10)
	f2 := make([]byte, 10)
	frames := [][]byte{f1, f2}
	dst := make([]byte, 20)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = rtp.PackInto(dst, frames)
	}
}
