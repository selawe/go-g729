package g729_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"testing"

	g729 "github.com/selawe/go-g729"
)

// TestNewWriterRejectsVAD ensures NewWriter returns ErrVADNotSupportedInStream
// when EnableVAD=true, preventing silent stream corruption.
func TestNewWriterRejectsVAD(t *testing.T) {
	_, err := g729.NewWriter(io.Discard, g729.ProfileCore()) // Core has EnableVAD=true
	if err == nil {
		t.Fatal("expected error for EnableVAD=true, got nil")
	}
	if err != g729.ErrVADNotSupportedInStream {
		t.Fatalf("expected ErrVADNotSupportedInStream, got: %v", err)
	}

	// ProfileFast (EnableVAD=false) must succeed
	w, err := g729.NewWriter(io.Discard, g729.ProfileFast())
	if err != nil {
		t.Fatalf("unexpected error for EnableVAD=false: %v", err)
	}
	_ = w
}

func TestStreamRoundTrip(t *testing.T) {
	// Generate 10 frames (100 ms = 800 samples = 1600 bytes) of 440 Hz tone
	const numSamples = 800
	pcmInput := make([]byte, numSamples*2)
	for i := 0; i < numSamples; i++ {
		s := int16(10000.0 * math.Sin(2*math.Pi*440.0*float64(i)/8000.0))
		binary.LittleEndian.PutUint16(pcmInput[i*2:(i+1)*2], uint16(s))
	}

	// Test with various arbitrary chunk sizes: 1B, 7B, 50B, 160B, 500B, 1600B
	chunkSizes := []int{1, 7, 50, 160, 500, 1600}

	for _, chunkSize := range chunkSizes {
		var bitstreamBuf bytes.Buffer
		writer, err := g729.NewWriter(&bitstreamBuf, g729.ProfileFast())
		if err != nil {
			t.Fatalf("chunkSize %d: NewWriter: %v", chunkSize, err)
		}

		// Stream write in chunks
		for offset := 0; offset < len(pcmInput); offset += chunkSize {
			end := offset + chunkSize
			if end > len(pcmInput) {
				end = len(pcmInput)
			}
			n, err := writer.Write(pcmInput[offset:end])
			if err != nil {
				t.Fatalf("chunkSize %d: Write failed: %v", chunkSize, err)
			}
			if n != end-offset {
				t.Fatalf("chunkSize %d: wrote %d bytes, want %d", chunkSize, n, end-offset)
			}
		}

		if err := writer.Close(); err != nil {
			t.Fatalf("chunkSize %d: Close failed: %v", chunkSize, err)
		}

		// 10 frames of G.729A CBR = 100 bytes of bitstream
		if bitstreamBuf.Len() != 100 {
			t.Fatalf("chunkSize %d: bitstream len %d, want 100", chunkSize, bitstreamBuf.Len())
		}

		// Stream read back into PCM
		reader := g729.NewReader(&bitstreamBuf)
		var decodedPCM bytes.Buffer
		readBuf := make([]byte, chunkSize)

		for {
			n, err := reader.Read(readBuf)
			if n > 0 {
				decodedPCM.Write(readBuf[:n])
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("chunkSize %d: Read failed: %v", chunkSize, err)
			}
		}

		if decodedPCM.Len() != len(pcmInput) {
			t.Fatalf("chunkSize %d: decoded len %d, want %d", chunkSize, decodedPCM.Len(), len(pcmInput))
		}
	}
}

func TestStreamPartialFramePadding(t *testing.T) {
	// Write 100 samples (200 bytes) = 1 full frame (160 bytes) + 40 bytes partial
	pcmInput := make([]byte, 200)
	var bitstreamBuf bytes.Buffer
	writer, err := g729.NewWriter(&bitstreamBuf, g729.ProfileFast())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	_, err = writer.Write(pcmInput)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Before close, only 1 frame (10 bytes) should be emitted
	if bitstreamBuf.Len() != 10 {
		t.Fatalf("expected 10 bytes before close, got %d", bitstreamBuf.Len())
	}

	// Close must flush the partial frame zero-padded, resulting in 20 bytes total (2 frames)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if bitstreamBuf.Len() != 20 {
		t.Fatalf("expected 20 bytes after close (2 frames), got %d", bitstreamBuf.Len())
	}
	// 200 bytes total = 100 samples. Frame 1 had 80 samples, so 20 original samples in frame 2.
	// 80 - 20 = 60 padded samples.
	if writer.PaddedSamples() != 60 {
		t.Errorf("expected 60 padded samples, got %d", writer.PaddedSamples())
	}
}

func TestStreamFlushDrop(t *testing.T) {
	// Write 100 samples (200 bytes) = 1 full frame (160 bytes) + 40 bytes partial
	pcmInput := make([]byte, 200)
	var bitstreamBuf bytes.Buffer
	writer, err := g729.NewWriter(&bitstreamBuf, g729.ProfileFast())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	writer.SetFlushMode(g729.FlushDrop)

	if _, err := writer.Write(pcmInput); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// FlushDrop must discard the remaining 40 bytes; only 1 frame (10 bytes) should be emitted
	if bitstreamBuf.Len() != 10 {
		t.Fatalf("expected 10 bytes after FlushDrop close, got %d", bitstreamBuf.Len())
	}
}

func TestStreamFlushError(t *testing.T) {
	// Write 100 samples (200 bytes) = 1 full frame (160 bytes) + 40 bytes partial
	pcmInput := make([]byte, 200)
	var bitstreamBuf bytes.Buffer
	writer, err := g729.NewWriter(&bitstreamBuf, g729.ProfileFast())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	writer.SetFlushMode(g729.FlushError)

	if _, err := writer.Write(pcmInput); err != nil {
		t.Fatalf("Write: %v", err)
	}
	err = writer.Close()
	if err != g729.ErrIncompleteFrame {
		t.Fatalf("expected ErrIncompleteFrame on Close, got: %v", err)
	}
}

func TestStreamReaderRejectsNon10ByteFrames(t *testing.T) {
	// 1. Incomplete frame (e.g. 5 bytes)
	badData := []byte{1, 2, 3, 4, 5}
	reader := g729.NewReader(bytes.NewReader(badData))
	out := make([]byte, 160)
	_, err := reader.Read(out)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected ErrUnexpectedEOF for 5-byte stream, got: %v", err)
	}

	// 2. 2-byte SID frame in raw CBR stream must be rejected
	sidData := []byte{0x55, 0xAA}
	reader2 := g729.NewReader(bytes.NewReader(sidData))
	_, err = reader2.Read(out)
	if err != io.ErrUnexpectedEOF {
		t.Errorf("expected ErrUnexpectedEOF for 2-byte SID stream, got: %v", err)
	}
}

// TestReaderClose verifies that Close delegates to the underlying io.Closer
// when present, and returns nil for non-closer readers.
func TestReaderClose(t *testing.T) {
	// bytes.Reader does not implement io.Closer — Close should return nil.
	r := g729.NewReader(bytes.NewReader([]byte{}))
	if err := r.Close(); err != nil {
		t.Fatalf("Close on non-closer: want nil, got %v", err)
	}

	// A reader wrapping an io.ReadCloser — Close must propagate the call.
	pr, pw := io.Pipe()
	pw.Close() // unblock any future read
	rCloser := g729.NewReader(pr)
	if err := rCloser.Close(); err != nil {
		t.Fatalf("Close on io.ReadCloser: want nil, got %v", err)
	}
}

// TestStreamReaderErrorSurfacedAfterPCMDrain verifies that a mid-stream read
// error is preserved and surfaced on the following Read call after buffered
// PCM has been drained — the io.Reader contract must not silently swallow it.
func TestStreamReaderErrorSurfacedAfterPCMDrain(t *testing.T) {
	// Build a bitstream of two valid speech frames, then append a truncated
	// (5-byte) tail so io.ReadFull returns io.ErrUnexpectedEOF mid-stream.
	var stream bytes.Buffer
	enc := g729.NewEncoder(g729.Config{Variant: g729.VariantG729A, EnableVAD: false})
	src := make([]int16, 80)
	for i := range src {
		// Simple ramp – any deterministic non-zero signal works.
		src[i] = int16((i * 200) - 8000)
	}
	var frame [10]byte
	for i := 0; i < 2; i++ {
		n, _, err := enc.Encode(frame[:], src)
		if err != nil || n != 10 {
			t.Fatalf("encoder setup: n=%d err=%v", n, err)
		}
		stream.Write(frame[:n])
	}
	// Truncated third frame — triggers io.ErrUnexpectedEOF after 2 good frames.
	stream.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00})

	reader := g729.NewReader(bytes.NewReader(stream.Bytes()))

	// First Read: request only 200 bytes so we don't exhaust the whole PCM.
	// This ensures the trailing bytes arrive later, hitting the pending path.
	buf := make([]byte, 200)
	n1, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("first Read returned err=%v (want nil), n=%d", err, n1)
	}
	if n1 == 0 {
		t.Fatalf("first Read returned 0 bytes")
	}

	// Drain remaining buffered PCM (2 frames = 320 bytes total) until the
	// underlying error surfaces. The truncated tail must cause a non-nil err.
	drain := make([]byte, 320)
	var sawErr error
	for attempts := 0; attempts < 10; attempts++ {
		n, err := reader.Read(drain)
		if err != nil {
			sawErr = err
			break
		}
		if n == 0 {
			break
		}
	}
	if sawErr != io.ErrUnexpectedEOF {
		t.Fatalf("expected ErrUnexpectedEOF surfaced after PCM drain, got %v", sawErr)
	}

	// Follow-up Read after the error must not resurrect it (state cleared).
	_, err = reader.Read(drain)
	if err == io.ErrUnexpectedEOF {
		t.Errorf("pendingErr should be cleared after first surfacing, got repeat: %v", err)
	}
}
